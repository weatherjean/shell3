//go:build unix

package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
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
func RegisterSendFileTool(sess hostToolRegistrar, workDir string) error {
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
		Handler: newSendFileHandler(workDir),
	})
}

// newSendFileHandler builds the send_file tool Handler rooted at workDir.
// Failures are returned as "error: …" tool-result strings (not Go errors),
// matching the engine's other host tools (see image_generate).
func newSendFileHandler(workDir string) func(ctx context.Context, argsJSON string) (string, error) {
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
		if !filepath.IsAbs(path) && workDir != "" {
			path = filepath.Join(workDir, path)
		}
		base := filepath.Base(path)

		// Refuse the `.env` beside shell3.yaml and dotenv siblings (.env.local,
		// …); mirrors the credential-file guard the config loader applies.
		if lb := strings.ToLower(base); lb == ".env" || strings.HasPrefix(lb, ".env.") {
			return "error: refusing to send a credentials file", nil
		}

		info, err := os.Stat(path)
		if err != nil {
			return "error: cannot read file: " + err.Error(), nil
		}
		if info.IsDir() {
			return "error: path is a directory, not a file", nil
		}
		if info.Size() > maxSendFileBytes {
			return fmt.Sprintf("error: file too large (%d MB, max %d MB)", info.Size()>>20, maxSendFileBytes>>20), nil
		}

		staged, err := stageMediaFile(path, base)
		if err != nil {
			return "error: " + err.Error(), nil
		}

		name := strings.TrimSpace(args.Name)
		if name == "" {
			name = base
		}
		if isImageExt(filepath.Ext(base)) {
			return fmt.Sprintf("sent %s — show it with ![](/api/media/%s)", base, staged), nil
		}
		return fmt.Sprintf("sent %s — give the user this link: [%s](/api/media/%s)", base, name, staged), nil
	}
}

// stageMediaFile copies the file at src into media.Dir() under a unique name
// that keeps base's extension, and returns that staged name (not a full
// path) — what /api/media/<name> expects.
func stageMediaFile(src, base string) (string, error) {
	dir, err := media.Dir()
	if err != nil {
		return "", err
	}
	staged := fmt.Sprintf("sent-%d-%s", time.Now().UnixNano(), base)

	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	out, err := os.Create(filepath.Join(dir, staged))
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return staged, nil
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
