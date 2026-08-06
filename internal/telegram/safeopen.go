//go:build unix

package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/media"
)

// safeOpen resolves path and opens it for sending, refusing everything that
// is not a plain file the user meant to receive. It is the whole security
// argument for send_media_telegram, so it states exactly what each step does
// and does not close:
//
//  1. The requested name, and then the symlink-resolved name, are checked
//     against the dotenv pattern (catches the obvious case and a symlink
//     whose own name is clean but whose target is not).
//  2. The resolved path is checked against the config directory by path
//     (catches a symlink into the config tree), exempting the media
//     directory — it lives under the config dir but holds exactly the
//     generated images and attachments this tool exists to send back.
//  3. The file is opened with O_NOFOLLOW|O_NONBLOCK. O_NOFOLLOW refuses only
//     if the FINAL path component is itself a symlink at open time — parent
//     directories are still followed, and a hardlink (a different name for
//     the same inode) is not a symlink at all, so this does not make the
//     sequence race-free by itself. O_NONBLOCK keeps a FIFO with no writer
//     from parking the turn forever (host tools have no timeout of their
//     own — the 120s cap is bash's).
//  4. From here everything reads the opened fd, not the path, so nothing can
//     be swapped underneath these checks: the type must be a regular file
//     (rejects FIFOs, devices, sockets), and the fd's (dev, ino) is compared
//     against every regular file anywhere in the config tree (skipping only
//     the media dir). A hardlink's resolved path lies outside the config dir
//     by construction but is never a different inode, so this inode walk —
//     not the path check in step 2 — is what actually closes the hardlink
//     route to `.env`, shell3.yaml, or a hook script.
//
// The caller bounds the read itself (readLimited): the size a pre-read Stat
// reports can be stale, and is 0 for a character device like /dev/zero.
func safeOpen(path, workDir, configDir string) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		if workDir == "" {
			return nil, nil, errors.New("cannot resolve a relative path without a working directory")
		}
		path = filepath.Join(workDir, path)
	}
	// Refuse the `.env` beside shell3.yaml and dotenv siblings (.env.local,
	// …); mirrors the credential-file guard the config loader applies. This
	// check on the unresolved name catches the obvious case, but a symlink
	// can point anywhere — resolved below, and checked again there, before
	// anything is read.
	if isDotenvName(filepath.Base(path)) {
		return nil, nil, errors.New("refusing to send a credentials file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read file: %w", err)
	}
	if isDotenvName(filepath.Base(resolved)) {
		return nil, nil, errors.New("refusing to send a credentials file")
	}

	// media.Dir() also creates the directory, so this both resolves it and
	// guarantees EvalSymlinks below can succeed.
	mediaResolved := ""
	if mdir, merr := media.Dir(); merr == nil {
		if r, rerr := filepath.EvalSymlinks(mdir); rerr == nil {
			mediaResolved = r
		} else {
			mediaResolved = mdir
		}
	}

	cfgResolved := ""
	if configDir != "" {
		r, cerr := filepath.EvalSymlinks(configDir)
		if cerr != nil {
			// configDir was supplied but could not be resolved — every check
			// below depends on it, so running with it silently skipped would
			// let a file through with both the path containment check and the
			// inode walk disabled. Fail closed like every other error here.
			return nil, nil, fmt.Errorf("cannot verify file safety: config directory could not be resolved: %w", cerr)
		}
		cfgResolved = r
		// If $SHELL3_MEDIA_DIR has been pointed at (or above) the config dir
		// itself — a misconfiguration, not the normal <configDir>/media
		// layout — exempting it would make the whole config tree sendable.
		// Refuse to trust the exemption in that case.
		mediaCoversCfg := mediaResolved != "" && pathWithin(cfgResolved, mediaResolved)
		inMedia := mediaResolved != "" && pathWithin(resolved, mediaResolved)
		if pathWithin(resolved, cfgResolved) && (mediaCoversCfg || !inMedia) {
			return nil, nil, errors.New("refusing to send a file from the config directory")
		}
	}

	in, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read file: %w", err)
	}
	info, err := in.Stat()
	if err != nil {
		_ = in.Close()
		return nil, nil, fmt.Errorf("cannot read file: %w", err)
	}
	if info.IsDir() {
		_ = in.Close()
		return nil, nil, errors.New("path is a directory, not a file")
	}
	if !info.Mode().IsRegular() {
		_ = in.Close()
		return nil, nil, errors.New("refusing to send a non-regular file (device, socket, or FIFO)")
	}
	if cfgResolved != "" {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			matched, isDotenv, werr := configTreeInodeMatch(cfgResolved, mediaResolved, st)
			if werr != nil {
				_ = in.Close()
				return nil, nil, fmt.Errorf("cannot verify file safety: %w", werr)
			}
			if matched {
				_ = in.Close()
				if isDotenv {
					return nil, nil, errors.New("refusing to send a credentials file")
				}
				return nil, nil, errors.New("refusing to send a file from the config directory")
			}
		}
	}
	return in, info, nil
}

// isDotenvName reports whether a filename is `.env` or a dotenv sibling
// (.env.local, .env.production, …), case-insensitively.
func isDotenvName(name string) bool {
	lb := strings.ToLower(name)
	return lb == ".env" || strings.HasPrefix(lb, ".env.")
}

// pathWithin reports whether path is p itself or lies under directory p.
func pathWithin(path, p string) bool {
	rel, err := filepath.Rel(p, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// configTreeInodeMatch walks cfgResolved looking for a regular file whose
// (dev, ino) matches target — the fd about to be sent. It skips only the
// media directory (generated images and saved attachments legitimately live
// there and must not be flagged); everything else under the config dir,
// .shell3_project included, is compared. Symlinks are not followed: their own
// inode can never match a regular file's, and following them risks escaping
// the tree or looping. isDotenv reports whether the matched file's own name
// is dotenv-shaped, so the caller can give the more specific refusal. A walk
// error fails closed, except an entry that vanished mid-walk — gone is proof
// it isn't the file being matched.
func configTreeInodeMatch(cfgResolved, mediaResolved string, target *syscall.Stat_t) (matched, isDotenv bool, err error) {
	// skip is only ever the media dir's own subtree. If it equals or contains
	// cfgResolved, WalkDir's first callback would SkipDir the whole tree, so
	// refuse the exemption and walk everything.
	skip := filepath.Clean(mediaResolved)
	if mediaResolved == "" || pathWithin(cfgResolved, mediaResolved) {
		skip = ""
	}
	walkErr := filepath.WalkDir(cfgResolved, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if skip != "" && filepath.Clean(p) == skip {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			if errors.Is(ferr, fs.ErrNotExist) {
				return nil
			}
			return ferr
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		if st.Dev == target.Dev && st.Ino == target.Ino {
			matched = true
			isDotenv = isDotenvName(d.Name())
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, false, walkErr
	}
	return matched, isDotenv, nil
}

// readLimited reads in fully, honouring ctx — a stalled read (a FIFO opened
// non-blocking whose writer stalls mid-stream) is cancellable by /stop rather
// than parking the turn — and refusing more than limit bytes regardless of
// what a pre-read Stat reported: a file can grow between the two, and a
// character device reports size 0 while never reaching EOF.
func readLimited(ctx context.Context, in *os.File, limit int64) ([]byte, error) {
	var out []byte
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			if int64(len(out))+int64(n) > limit {
				return nil, fmt.Errorf("file too large (max %d MB)", limit>>20)
			}
			out = append(out, buf[:n]...)
		}
		if rerr != nil {
			if rerr == io.EOF {
				return out, nil
			}
			if errors.Is(rerr, syscall.EAGAIN) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				continue
			}
			return nil, rerr
		}
	}
}
