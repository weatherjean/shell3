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

	"github.com/weatherjean/shell3/internal/mediadir"
	"github.com/weatherjean/shell3/internal/paths"
)

// safeOpen resolves path and opens it for sending, refusing anything that is
// not a plain file the user meant to receive. It is the whole security
// argument for send_media_telegram, so each step states what it does and does
// NOT close:
//
//  1. The requested name, then the symlink-resolved name, are matched against
//     the dotenv pattern — catching the obvious case, and a symlink whose own
//     name is clean but whose target is not.
//  2. The resolved path is checked against the config directory, catching a
//     symlink into the config tree, exempting the media dir: it lives under
//     the config dir but holds exactly what this tool exists to send back.
//  3. Opened O_NOFOLLOW|O_NONBLOCK. O_NOFOLLOW refuses only if the FINAL
//     component is a symlink at open time — parents are still followed, and a
//     hardlink is not a symlink at all — so this alone is not race-free.
//     O_NONBLOCK stops a writerless FIFO parking the turn forever; host tools
//     have no timeout of their own.
//  4. From here everything reads the fd, not the path, so nothing can be
//     swapped underneath: the type must be a regular file, and the fd's
//     (dev, ino) is compared against every regular file in the config tree,
//     media dir aside. A hardlink's resolved path lies outside the config dir
//     by construction but is never a different inode, so this walk — not step
//     2 — is what closes the hardlink route to .env or the kit.
//
// The caller bounds the read itself: a pre-read Stat's size can be stale, and
// is 0 for a character device.
func safeOpen(path, workDir, configDir string) (*os.File, os.FileInfo, error) {
	if !filepath.IsAbs(path) {
		if workDir == "" {
			return nil, nil, errors.New("cannot resolve a relative path without a working directory")
		}
		path = filepath.Join(workDir, path)
	}
	// The .env beside the kit and its siblings, mirroring the loader's guard.
	// On the unresolved name this catches the obvious case; a symlink is
	// resolved and re-checked below, before anything is read.
	if paths.IsCredentialFile(filepath.Base(path)) {
		return nil, nil, errors.New("refusing to send a credentials file")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot read file: %w", err)
	}
	if paths.IsCredentialFile(filepath.Base(resolved)) {
		return nil, nil, errors.New("refusing to send a credentials file")
	}

	// mediadir.Dir() creates the directory too, so EvalSymlinks can succeed.
	mediaResolved := ""
	if mdir, merr := mediadir.Dir(); merr == nil {
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
			// Every check below depends on it, so skipping would let a file
			// through with both containment and the inode walk disabled.
			return nil, nil, fmt.Errorf("cannot verify file safety: config directory could not be resolved: %w", cerr)
		}
		cfgResolved = r
		// $SHELL3_MEDIA_DIR pointed at or above the config dir is a
		// misconfiguration, and exempting it would make the whole tree
		// sendable, so the exemption is not trusted there.
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

// pathWithin reports whether path is p itself or lies under directory p.
func pathWithin(path, p string) bool {
	rel, err := filepath.Rel(p, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// configTreeInodeMatch walks cfgResolved for a regular file whose (dev, ino)
// matches the fd about to be sent. Only the media dir is skipped — generated
// images and attachments legitimately live there — and everything else,
// .shell3_project included, is compared. Symlinks are not followed: their
// inode can never match a regular file's, and following risks escaping or
// looping. isDotenv lets the caller give the more specific refusal. A walk
// error fails closed, except an entry that vanished — gone is proof it is not
// the file being matched.
func configTreeInodeMatch(cfgResolved, mediaResolved string, target *syscall.Stat_t) (matched, isDotenv bool, err error) {
	// skip is only ever the media subtree. Equal to or containing cfgResolved,
	// WalkDir's first callback would SkipDir everything, so walk it all.
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
			isDotenv = paths.IsCredentialFile(d.Name())
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return false, false, walkErr
	}
	return matched, isDotenv, nil
}

// readLimited reads in fully under ctx, so a stalled read — a non-blocking
// FIFO whose writer stalls — is cancellable rather than parking the turn, and
// refuses more than limit bytes whatever a pre-read Stat said: a file can grow
// between the two, and a character device reports 0 and never reaches EOF.
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
