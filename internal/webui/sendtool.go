//go:build unix

package webui

import (
	"context"
	"encoding/json"
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
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// maxSendFileBytes caps what send_file will stage — a var (not a const) so
// tests can shrink it rather than manufacture a 50 MiB fixture.
var maxSendFileBytes int64 = 50 << 20

// hostToolRegistrar is the narrow registration surface RegisterSendFileTool
// needs, satisfied by *shell3.Session. Declaring it here (rather than taking
// *shell3.Session directly) keeps this file's tests independent of a live
// Session.
type hostToolRegistrar interface {
	RegisterHostTool(t shell3.HostTool) error
	Headless() bool
}

// RegisterSendFileTool gives sess a send_file tool that stages a local file
// into media.Dir() and hands back a ready-made /api/media/ link. It is a
// no-op for a headless session (subagent, cron job): there is no chat for a
// link to appear in, and no user on the other end to receive one.
func RegisterSendFileTool(sess hostToolRegistrar, workDir, configDir string) error {
	if sess.Headless() {
		return nil
	}
	return sess.RegisterHostTool(shell3.HostTool{
		Name: "send_file",
		Description: "Hand the user a local file (report, export, generated asset, …) by staging it and " +
			"returning a link the chat UI renders. Use it whenever you need to deliver a file, not just describe it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Path to the file to send (absolute or relative to the working directory)."},
				"name": map[string]any{"type": "string", "description": "Optional display name for the link (defaults to the file's base name)."},
			},
			"required": []string{"path"},
		},
		Handler: newSendFileHandler(workDir, configDir),
	})
}

// newSendFileHandler builds the send_file tool Handler rooted at workDir,
// refusing anything under configDir. Failures are returned as "error: …"
// tool-result strings (not Go errors), matching the engine's other host
// tools (see image_generate).
//
// Defense in depth, in order — this comment is the security argument, so it
// states exactly what each step does and does not close:
//
//  1. The requested name, and then the symlink-resolved name, are checked
//     against the dotenv pattern (catches the obvious case and a symlink
//     whose own name is clean but whose target is not).
//  2. The resolved path is checked against the config directory by path
//     (catches a symlink into the config tree), exempting the media
//     directory — it lives under the config dir but is send_file's own
//     staging area, and refusing it would break the tool's main use case.
//  3. The file is opened with O_NOFOLLOW|O_NONBLOCK. O_NOFOLLOW refuses only
//     if the FINAL path component is itself a symlink at open time — parent
//     directories are still followed, and a hardlink (a different name for
//     the same inode) or a freshly created regular file is not a symlink at
//     all, so this does not make the sequence race-free by itself.
//     O_NONBLOCK keeps opening a FIFO with no reader-ready writer from
//     parking the call.
//  4. From here everything reads the opened fd, not the path, so nothing
//     can be swapped underneath these checks: the type must be a regular
//     file (rejects FIFOs, devices, sockets — none of which are files to
//     "send"), the size is checked as a cheap optimization, and the fd's
//     (dev, ino) is compared against every regular file anywhere in the
//     config tree (skipping only the media dir — see configTreeInodeMatch).
//     A hardlink's resolved path lies outside the config dir by construction,
//     but it is never a different inode, so this inode walk — not the
//     path-based check in step 2 — is what actually closes the hardlink
//     route, for a nested `.env`, shell3.yaml, a hook script, or anything
//     else in the tree.
//  5. The copy itself is bounded again at write time (the pre-copy Stat in
//     step 4 can be stale by the time the copy runs) and aborts if ctx is
//     cancelled, so a stalled read cannot park the turn either.
func newSendFileHandler(workDir, configDir string) func(ctx context.Context, argsJSON string) (string, error) {
	return func(ctx context.Context, argsJSON string) (string, error) {
		var args struct {
			Path string `json:"path"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "error: invalid arguments: " + err.Error(), nil
		}
		path := strings.TrimSpace(args.Path)
		if path == "" {
			return "error: path is required", nil
		}
		if !filepath.IsAbs(path) {
			if workDir == "" {
				return "error: cannot resolve a relative path without a working directory", nil
			}
			path = filepath.Join(workDir, path)
		}
		base := filepath.Base(path)

		// Refuse the `.env` beside shell3.yaml and dotenv siblings (.env.local,
		// …); mirrors the credential-file guard the config loader applies. This
		// check on the unresolved name catches the obvious case, but a symlink
		// can point anywhere — resolved below, and checked again there, before
		// anything is read.
		if isDotenvName(base) {
			return "error: refusing to send a credentials file", nil
		}

		// Resolve symlinks before doing anything that reads the file: a
		// symlink whose own name is clean (e.g. report.txt -> ~/.shell3/.env)
		// would otherwise sail past the check above.
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return "error: cannot read file: " + err.Error(), nil
		}
		if isDotenvName(filepath.Base(resolved)) {
			return "error: refusing to send a credentials file", nil
		}

		// media.Dir() also creates the directory, so this both resolves it
		// and guarantees EvalSymlinks below can succeed.
		mediaDir, err := media.Dir()
		if err != nil {
			return "error: cannot resolve media directory: " + err.Error(), nil
		}
		mediaResolved, err := filepath.EvalSymlinks(mediaDir)
		if err != nil {
			mediaResolved = mediaDir
		}

		// Refuse anything inside the config directory outright — shell3.yaml,
		// hooks, and every other non-.env file there is already visible via
		// the Files view, and none of it belongs behind a general file-send —
		// except the media directory itself, which is send_file's own
		// staging area and legitimately lives under the config dir.
		var cfgResolved string
		haveCfg := false
		if configDir != "" {
			if r, err := filepath.EvalSymlinks(configDir); err == nil {
				cfgResolved = r
				haveCfg = true
				// If $SHELL3_MEDIA_DIR has been pointed at (or above) the
				// config dir itself — a misconfiguration, not the normal
				// <configDir>/media layout — exempting it would make
				// pathWithin(resolved, mediaResolved) true for the whole
				// config tree, silently defeating this check (and the inode
				// walk below, whose skip root would equal its own walk
				// root) for everything in it. Refuse rather than trust the
				// exemption in that case.
				mediaCoversCfg := pathWithin(cfgResolved, mediaResolved)
				if pathWithin(resolved, cfgResolved) && (mediaCoversCfg || !pathWithin(resolved, mediaResolved)) {
					return "error: refusing to send a file from the config directory (the Files view already shows it)", nil
				}
			} else {
				// configDir was supplied but could not be resolved (a
				// transient stat failure, a vanished mount, …) — every
				// check below this point depends on cfgResolved, so running
				// with it silently skipped would mean an ordinary file
				// sails through with the path-containment check and the
				// inode walk both disabled. Fail closed like every other
				// error path in this handler.
				return "error: cannot verify file safety: config directory could not be resolved: " + err.Error(), nil
			}
		}

		// Open once, with O_NOFOLLOW so the final path component cannot be a
		// symlink at open time, and O_NONBLOCK so a FIFO with no writer
		// cannot park the call; validate every remaining check (type, size,
		// hardlink identity) against the resulting fd rather than the path.
		in, err := os.OpenFile(resolved, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
		if err != nil {
			return "error: cannot read file: " + err.Error(), nil
		}
		defer in.Close()
		info, err := in.Stat()
		if err != nil {
			return "error: cannot read file: " + err.Error(), nil
		}
		if info.IsDir() {
			return "error: path is a directory, not a file", nil
		}
		if !info.Mode().IsRegular() {
			return "error: refusing to send a non-regular file (device, socket, or FIFO)", nil
		}
		if info.Size() > maxSendFileBytes {
			return fmt.Sprintf("error: file too large (%d MB, max %d MB)", info.Size()>>20, maxSendFileBytes>>20), nil
		}

		// A hardlink to a file anywhere in the config tree has a clean name
		// and a resolved path outside the config dir, but it is never a
		// different inode — catch it by walking the tree and comparing
		// (dev, ino) against the opened fd. A walk error fails closed
		// (refuses) rather than silently allowing.
		if haveCfg {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				matched, isDotenv, werr := configTreeInodeMatch(cfgResolved, mediaResolved, st)
				if werr != nil {
					return "error: cannot verify file safety: " + werr.Error(), nil
				}
				if matched {
					if isDotenv {
						return "error: refusing to send a credentials file", nil
					}
					return "error: refusing to send a file from the config directory (the Files view already shows it)", nil
				}
			}
		}

		// base is just as model-controlled as the optional display name (the
		// model has bash and picks the filename), so it goes through the
		// same sanitizeLinkName before being interpolated into the returned
		// text — a filename containing markdown link syntax could otherwise
		// fabricate a link to an attacker-controlled host. It is applied
		// before staging (not just before display) because the staged name
		// is itself echoed back inside the rendered /api/media/ URL, so an
		// unsanitized suffix there is exactly as exploitable as one in the
		// prose; using dispBase for both keeps the returned URL and the
		// file it actually names in agreement.
		dispBase := sanitizeLinkName(base)
		if dispBase == "" {
			dispBase = "file"
		}

		staged, err := stageMediaFile(ctx, in, dispBase, maxSendFileBytes)
		if err != nil {
			return "error: " + err.Error(), nil
		}

		name := sanitizeLinkName(strings.TrimSpace(args.Name))
		if name == "" {
			name = dispBase
		}
		if isImageExt(filepath.Ext(base)) {
			return fmt.Sprintf("sent %s — show it with ![%s](/api/media/%s)", dispBase, name, staged), nil
		}
		return fmt.Sprintf("sent %s — give the user this link: [%s](/api/media/%s)", dispBase, name, staged), nil
	}
}

// pathWithin reports whether path is p itself or lies under directory p.
func pathWithin(path, p string) bool {
	rel, err := filepath.Rel(p, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// configTreeInodeMatch walks cfgResolved (the resolved config directory)
// looking for a regular file whose (dev, ino) matches target — the fd of
// the file send_file is about to serve. It skips only the media directory
// (send_file's own staging area: generated images and earlier sends
// legitimately live there and must not be flagged) — nothing else under
// cfgResolved is exempt, including <configDir>/.shell3_project (the web
// session-token hashes live there): a hardlink into it must be refused
// exactly like a direct path there already is, and the dir is small enough
// that walking it costs nothing. This only covers <configDir>/.shell3_project,
// which is where webui's own state lives (internal/webui/server.go); the
// runs store (shell3.db) is opened at <CWD>/.shell3_project
// (internal/agentsetup/agentsetup.go, via paths.NewLocal(opts.CWD)), and
// whenever the agent's workdir differs from the config dir — the ordinary
// --config case — shell3.db sits outside both this walk and the step-2 path
// check. That is not a gap in this defense: shell3.db under the workdir is
// exactly the kind of file send_file exists to deliver, not one this check
// is meant to catch. Symlinks encountered during the walk are not followed — their
// own inode can never match a regular file's, and following them risks
// escaping the tree or looping. isDotenv reports whether the matched
// file's own name is dotenv-shaped, so the caller can give the more
// specific "credentials file" refusal. A walk error fails closed (the
// caller refuses), except an entry that has vanished between being listed
// and being stat'd (entryVanished) — gone is proof it isn't the file being
// matched, not evidence to distrust the whole walk.
func configTreeInodeMatch(cfgResolved, mediaResolved string, target *syscall.Stat_t) (matched, isDotenv bool, err error) {
	// skip is only ever the media dir's own subtree. If mediaResolved
	// equals or contains cfgResolved (SHELL3_MEDIA_DIR misconfigured onto
	// the config dir itself), skip would equal — or sit above — the walk
	// root, and WalkDir's very first callback (the root itself) would
	// SkipDir before anything is compared, silently exempting the entire
	// tree. Refuse the exemption in that case by walking everything.
	skip := filepath.Clean(mediaResolved)
	if pathWithin(cfgResolved, mediaResolved) {
		skip = ""
	}
	walkErr := filepath.WalkDir(cfgResolved, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if entryVanished(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			if filepath.Clean(p) == skip {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			if entryVanished(ferr) {
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

// entryVanished reports whether err (from WalkDir's own traversal error or
// a DirEntry.Info() call during the config-tree inode walk) means the file
// disappeared between being listed and being stat'd — an agent temp file
// or editor swap file removed mid-walk. A vanished file cannot be the fd
// being matched, so the walk should skip it and continue rather than fail
// the whole (unrelated) send closed.
func entryVanished(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}

// copyLimited copies from in to out, honouring ctx — a stalled read (a FIFO
// opened non-blocking with a writer that stalls mid-stream) can be
// cancelled by /api/stop rather than parking the turn indefinitely — and
// refusing to write more than limit bytes, regardless of what the pre-copy
// Stat reported: a file's on-disk size can grow between Stat and this copy,
// so Stat is an optimization, not the defense.
func copyLimited(ctx context.Context, out io.Writer, in *os.File, limit int64) error {
	buf := make([]byte, 32*1024)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := in.Read(buf)
		if n > 0 {
			total += int64(n)
			if total > limit {
				return fmt.Errorf("exceeds the %d MB size limit", limit>>20)
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if rerr != nil {
			if rerr == io.EOF {
				return nil
			}
			if errors.Is(rerr, syscall.EAGAIN) {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(20 * time.Millisecond):
				}
				continue
			}
			return rerr
		}
	}
}

// stageMediaFile copies in (an already-open, already-validated file) into
// media.Dir() under a unique name that keeps base's extension, bounded at
// limit bytes and cancellable via ctx (see copyLimited), and returns that
// staged name (not a full path) — what /api/media/<name> expects. A partial
// staged file is removed on any copy failure.
func stageMediaFile(ctx context.Context, in *os.File, base string, limit int64) (string, error) {
	dir, err := media.Dir()
	if err != nil {
		return "", err
	}
	staged := fmt.Sprintf("sent-%d-%s", time.Now().UnixNano(), base)
	dest := filepath.Join(dir, staged)

	out, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	if err := copyLimited(ctx, out, in, limit); err != nil {
		out.Close()
		os.Remove(dest)
		return "", err
	}
	if err := out.Close(); err != nil {
		os.Remove(dest)
		return "", err
	}
	return staged, nil
}

// sanitizeLinkName strips characters that would let the model-supplied
// display name inject its own markdown syntax into the returned link (an
// unescaped name renders whatever the model puts in it, up to a fabricated
// link to another host) and caps its length on a rune boundary — a byte cap
// can split a multi-byte rune and emit invalid UTF-8 into the response.
func sanitizeLinkName(name string) string {
	const maxLinkNameRunes = 200
	name = strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\r', '[', ']', '(', ')':
			return -1
		}
		return r
	}, name)
	name, _ = strutil.CutRunes(name, maxLinkNameRunes)
	return name
}

// isDotenvName reports whether base (a file's base name, any case) is `.env`
// or a dotenv sibling like `.env.local` — the credential-file guard applied
// to both the requested path and its symlink-resolved target.
func isDotenvName(base string) bool {
	lb := strings.ToLower(base)
	return lb == ".env" || strings.HasPrefix(lb, ".env.")
}

// isImageExt reports whether ext (with leading dot, any case) names an image
// type the chat UI can render inline as ![](...) rather than a plain link.
func isImageExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	default:
		return false
	}
}
