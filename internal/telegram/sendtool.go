//go:build unix

package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

const maxSendBytes = 50 << 20 // Telegram bot upload limit (~50 MB)
const maxPhotoBytes = 10 << 20

// mdInlineThreshold is the byte size under which a rendered render.* view is
// sent as a normal chat message (through sendReply's Markdown→HTML path)
// rather than a document — comfortably under Telegram's 4096-char message cap
// once HTML markup inflates it.
const mdInlineThreshold = 3500

// mdSummaryMax caps the text summary sent alongside a markdown document.
const mdSummaryMax = 400

// sendMarkdownDoc is the shared delivery path for every dash-view command
// (/status /jobs /job /cron /runs): md inline when small, or as a `name`
// document plus a capped text summary when it would blow past Telegram's
// message size.
func (b *Bot) sendMarkdownDoc(ctx context.Context, name, md string) {
	if len(md) <= mdInlineThreshold {
		b.sendReply(ctx, md)
		return
	}
	if _, err := b.client.SendDocument(ctx, b.chatID, name, []byte(md), ""); err != nil {
		b.sendReply(ctx, "⚠️ failed to send "+name+": "+err.Error())
		return
	}
	// Plain Send, not sendReply's Markdown->HTML path: the summary is an
	// arbitrary substring of the document, and running it through mdhtml could
	// inflate it (e.g. "# heading" -> "<b>heading</b>") past the byte cap.
	summary := strutil.Truncate(strings.TrimSpace(md), mdSummaryMax)
	if _, err := b.client.Send(ctx, b.chatID, summary); err != nil {
		b.sendReply(ctx, summary)
	}
}

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
		Handler: b.sendMediaHandler,
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
func (b *Bot) sendMediaHandler(ctx context.Context, argsJSON string) (string, error) {
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
	if !filepath.IsAbs(path) && b.workDir != "" {
		path = filepath.Join(b.workDir, path)
	}
	base := filepath.Base(path)
	// Refuse the `.env` beside shell3.yaml and dotenv siblings (.env.local, …);
	// mirrors the credential-file guard the config loader applies.
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
	data, err := os.ReadFile(path)
	if err != nil {
		return "error: cannot read file: " + err.Error(), nil
	}
	switch kind {
	case "photo":
		err = b.client.SendPhoto(ctx, b.chatID, base, data, args.Caption)
	case "voice":
		err = b.client.SendVoice(ctx, b.chatID, data, args.Caption)
	case "audio":
		err = b.client.SendAudio(ctx, b.chatID, base, data, args.Caption)
	case "video":
		err = b.client.SendVideo(ctx, b.chatID, base, data, args.Caption)
	default:
		_, err = b.client.SendDocument(ctx, b.chatID, base, data, args.Caption)
	}
	if err != nil {
		return "error: failed to send: " + err.Error(), nil
	}
	return "sent " + base + " to the user", nil
}
