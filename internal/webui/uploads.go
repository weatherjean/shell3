//go:build unix

package webui

import (
	"context"
	"encoding/base64"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Attachments the browser sends with a message. The composer inlines each file
// as a data: URI, so the whole upload arrives inside the chat request rather
// than through a separate endpoint.
//
// Every upload is written to the media dir before the turn, so it keeps a
// durable path the agent can re-read with `read_media` (or any bash tool)
// later in the conversation — the same guarantee the file store has always
// given generated images.

// maxUploadBytes caps one decoded attachment. Comfortably above a phone photo,
// far below anything that would blow out a model's context.
const maxUploadBytes = 20 << 20

// attachment is one saved upload.
type attachment struct {
	Path     string
	MIME     string
	Name     string
	IsImage  bool
	IsAudio  bool
	Caption  string // set when media.describe is configured
	SizeText string
}

// saveUploads writes every file part of a message to the media dir. Malformed
// or oversized parts are skipped with a note rather than failing the turn —
// losing one attachment beats losing the message.
func (s *Server) saveUploads(parts []chatPart) (saved []attachment, notes []string) {
	dir, err := media.Dir()
	if err != nil {
		return nil, []string{"could not open the media directory: " + err.Error()}
	}

	for _, part := range parts {
		if part.Type != "file" || part.URL == "" {
			continue
		}
		data, mimeType, err := decodeDataURI(part.URL)
		if err != nil {
			notes = append(notes, "skipped an attachment: "+err.Error())
			continue
		}
		if len(data) > maxUploadBytes {
			notes = append(notes, fmt.Sprintf("skipped an attachment: %s is over the %d MB limit",
				mimeType, maxUploadBytes>>20))
			continue
		}

		name := fmt.Sprintf("up-%d%s", time.Now().UnixNano(), extensionFor(mimeType))
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			notes = append(notes, "could not store an attachment: "+err.Error())
			continue
		}

		saved = append(saved, attachment{
			Path:     path,
			MIME:     mimeType,
			Name:     name,
			IsImage:  strings.HasPrefix(mimeType, "image/"),
			IsAudio:  strings.HasPrefix(mimeType, "audio/"),
			SizeText: humanBytes(len(data)),
		})
	}
	return saved, notes
}

// describeUploads captions images through the configured `media.describe`
// model, so a text-only main agent still knows what it was sent. No describe
// model configured leaves captions empty and the raw part is attached instead.
func (s *Server) describeUploads(ctx context.Context, uploads []attachment) []attachment {
	s.mu.Lock()
	clients := s.media
	s.mu.Unlock()
	if clients == nil || clients.Describe == nil {
		return uploads
	}

	// Captioning is a model call per image; bound it so a slow vision endpoint
	// cannot stall the turn indefinitely.
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	for i, up := range uploads {
		if !up.IsImage {
			continue
		}
		caption, err := clients.Describe(ctx, up.Path)
		if err != nil {
			s.log.Error("webui: describe failed", err)
			continue
		}
		uploads[i].Caption = strings.TrimSpace(caption)
	}
	return uploads
}

// promptWithUploads appends a note about each attachment to the user's text,
// so the agent knows what arrived and where it lives even when the model
// cannot see the bytes.
func promptWithUploads(prompt string, uploads []attachment, notes []string) string {
	if len(uploads) == 0 && len(notes) == 0 {
		return prompt
	}
	var b strings.Builder
	b.WriteString(prompt)

	for _, up := range uploads {
		b.WriteString("\n\n[attached: ")
		b.WriteString(up.MIME)
		b.WriteString(", ")
		b.WriteString(up.SizeText)
		b.WriteString(" — ")
		b.WriteString(up.Path)
		b.WriteString("]")
		if up.Caption != "" {
			b.WriteString("\n[image: ")
			b.WriteString(up.Caption)
			b.WriteString("]")
		}
	}
	for _, note := range notes {
		b.WriteString("\n\n[" + note + "]")
	}
	return b.String()
}

// sessionParts converts saved uploads into the multimodal parts the runtime
// takes. An image already captioned by `media.describe` is left out: the
// caption is in the prompt, and a text-only model would reject the bytes.
func sessionParts(uploads []attachment) []shell3.Part {
	var parts []shell3.Part
	for _, up := range uploads {
		switch {
		case up.IsImage && up.Caption == "":
			parts = append(parts, shell3.Part{Kind: shell3.PartImage, Path: up.Path})
		case up.IsAudio:
			parts = append(parts, shell3.Part{Kind: shell3.PartAudio, Path: up.Path})
		}
	}
	return parts
}

// decodeDataURI splits a `data:<mime>;base64,<payload>` URI. Only base64 data
// URIs are accepted — a remote URL would make the server fetch whatever the
// page pointed it at.
func decodeDataURI(uri string) ([]byte, string, error) {
	if !strings.HasPrefix(uri, "data:") {
		return nil, "", fmt.Errorf("attachments must be inlined, not linked")
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !found {
		return nil, "", fmt.Errorf("malformed data URI")
	}
	mimeType, isBase64 := strings.CutSuffix(header, ";base64")
	if !isBase64 {
		return nil, "", fmt.Errorf("attachments must be base64-encoded")
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, "", fmt.Errorf("attachment is not valid base64")
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return data, mimeType, nil
}

// extensionFor picks a file extension for a MIME type; the runtime routes
// Path-based parts by extension, so this has to be right for images.
func extensionFor(mimeType string) string {
	switch mimeType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/ogg":
		return ".ogg"
	}
	if exts, err := mime.ExtensionsByType(mimeType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
