//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
)

const maxSendBytes = 50 << 20 // Telegram bot upload limit (~50 MB)
const maxPhotoBytes = 10 << 20

// registerSendTool gives the agent a send_media_telegram tool to push a local
// file back to the user's Telegram chat.
func (b *Bot) registerSendTool(s *shell3.Session) {
	_ = s.RegisterHostTool(shell3.HostTool{
		Name: "send_media_telegram",
		Description: "Send a local file from disk to the user via Telegram (image, document, audio, video, …). " +
			"Use it to deliver a file you produced or were asked to share.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "Path to the file to send (absolute or relative to the working directory)."},
				"caption": map[string]any{"type": "string", "description": "Optional caption shown with the file."},
				"kind": map[string]any{
					"type": "string",
					"description": "photo|voice|audio|video|document (default document). voice requires .ogg/.opus. " +
						"video requires .mp4/.webm/.mov. " +
						"photo is recompressed by Telegram (~1280px) — use document for pixel-exact delivery.",
				},
			},
			"required": []string{"path"},
		},
		// The room is resolved per CALL, not at registration: a session's
		// room is what makes "send this to the user" mean one chat rather
		// than another, and a session can be registered before its room's
		// conversation is the live one (restart, /new).
		Handler: func(ctx context.Context, argsJSON string) (string, error) {
			return b.sendMediaHandler(ctx, s, argsJSON)
		},
	})
}

// validateKind checks whether a file with the given extension and size may
// be sent as the requested kind. ext should include the leading dot and may
// be any case. Returns nil if the kind/ext/size combination is acceptable.
func validateKind(kind, ext string, size int64) error {
	ext = strings.ToLower(ext)
	switch kind {
	case "document":
		return nil
	case "photo":
		switch ext {
		case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		default:
			return fmt.Errorf("error: kind=photo requires an image file (jpg, jpeg, png, gif, webp)")
		}
		if size > maxPhotoBytes {
			return fmt.Errorf("error: kind=photo requires an image file under 10 MB")
		}
		return nil
	case "voice":
		if ext != ".ogg" && ext != ".opus" {
			return fmt.Errorf("error: kind=voice requires an .ogg/.opus file — use kind=audio for mp3")
		}
		return nil
	case "audio":
		switch ext {
		case ".mp3", ".m4a", ".ogg", ".opus", ".wav":
			return nil
		default:
			return fmt.Errorf("error: kind=audio requires an audio file (mp3, m4a, ogg, opus, wav)")
		}
	case "video":
		switch ext {
		case ".mp4", ".webm", ".mov":
			return nil
		default:
			return fmt.Errorf("error: kind=video requires an .mp4/.webm/.mov file")
		}
	default:
		return fmt.Errorf("error: kind must be photo, voice, audio, video, or document")
	}
}

// sendMediaHandler implements send_media_telegram. Failures are returned as
// "error: …" tool-result strings (not Go errors), matching the engine's tools.
func (b *Bot) sendMediaHandler(ctx context.Context, sess *shell3.Session, argsJSON string) (string, error) {
	c := b.roomOrHome(sess.ID())
	var args struct {
		Path    string `json:"path"`
		Caption string `json:"caption"`
		Kind    string `json:"kind"`
	}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "error: invalid arguments: " + err.Error(), nil
	}
	path := strings.TrimSpace(args.Path)
	if path == "" {
		return "error: path is required", nil
	}
	base := filepath.Base(path)
	// safeOpen carries the whole security argument (symlink laundering,
	// config-tree containment, hardlinks by inode, non-regular files); read
	// its doc before changing anything here.
	in, info, err := safeOpen(path, b.workDir, b.configDir)
	if err != nil {
		return "error: " + err.Error(), nil
	}
	defer in.Close()
	if info.Size() > maxSendBytes {
		return fmt.Sprintf("error: file too large (%d MB, max 50 MB)", info.Size()>>20), nil
	}
	kind := strings.TrimSpace(args.Kind)
	if kind == "" {
		kind = "document"
	}
	if err := validateKind(kind, filepath.Ext(base), info.Size()); err != nil {
		return err.Error(), nil
	}
	// Bounded again at read time: the Stat above is an optimization, not the
	// defense — the file can grow, and a character device reports size 0.
	data, err := readLimited(ctx, in, maxSendBytes)
	if err != nil {
		return "error: cannot read file: " + err.Error(), nil
	}
	switch kind {
	case "photo":
		err = b.client.SendPhoto(ctx, c.chatID, base, data, args.Caption)
	case "voice":
		err = b.client.SendVoice(ctx, c.chatID, data, args.Caption)
	case "audio":
		err = b.client.SendAudio(ctx, c.chatID, base, data, args.Caption)
	case "video":
		err = b.client.SendVideo(ctx, c.chatID, base, data, args.Caption)
	default:
		_, err = b.client.SendDocument(ctx, c.chatID, base, data, args.Caption)
	}
	if err != nil {
		return "error: failed to send: " + err.Error(), nil
	}
	return "sent " + base + " to the user", nil
}
