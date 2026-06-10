//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

const maxSendBytes = 50 << 20 // Telegram bot upload limit (~50 MB)

// registerSendTool gives the agent a send_media_telegram tool to push a local
// file back to the user's Telegram chat.
func (b *Bot) registerSendTool() {
	_ = b.sess.RegisterHostTool(shell3.HostTool{
		Name: "send_media_telegram",
		Description: "Send a local file from disk to the user via Telegram (image, document, audio, video, …). " +
			"Use it to deliver a file you produced or were asked to share. Uploaded as a document.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the file to send (absolute or relative to the working directory)."},
				"caption": map[string]any{"type": "string", "description": "Optional caption shown with the file."},
			},
			"required": []string{"path"},
		},
		Handler: b.sendMediaHandler,
	})
}

// sendMediaHandler implements send_media_telegram. Failures are returned as
// "error: …" tool-result strings (not Go errors), matching the engine's tools.
func (b *Bot) sendMediaHandler(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: invalid arguments: " + err.Error(), nil
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "error: path is required", nil
	}
	if !filepath.IsAbs(path) && b.workDir != "" {
		path = filepath.Join(b.workDir, path)
	}
	base := filepath.Base(path)
	if lower := strings.ToLower(base); lower == ".env" || strings.HasPrefix(lower, "ai-do-not-read") {
		return "error: refusing to send a credentials file", nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return "error: cannot read file: " + err.Error(), nil
	}
	if info.IsDir() {
		return "error: path is a directory, not a file", nil
	}
	if info.Size() > maxSendBytes {
		return fmt.Sprintf("error: file too large (%d MB, max 50 MB)", info.Size()>>20), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "error: cannot read file: " + err.Error(), nil
	}
	if err := b.client.SendDocument(ctx, b.chatID, base, data, args.Caption); err != nil {
		return "error: failed to send: " + err.Error(), nil
	}
	return "sent " + base + " to the user", nil
}
