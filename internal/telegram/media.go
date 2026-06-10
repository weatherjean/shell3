//go:build unix

package telegram

import (
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// mediaToParts converts resolved Telegram attachments to shell3 parts,
// dropping anything the engine can't ingest (images/audio only).
func mediaToParts(media []Media) []shell3.Part {
	var parts []shell3.Part
	for _, m := range media {
		switch {
		case strings.HasPrefix(m.MIME, "image/"):
			parts = append(parts, shell3.Part{Kind: shell3.PartImage, Data: m.Bytes, MIME: m.MIME})
		case m.MIME == "audio/wav", m.MIME == "audio/x-wav", m.MIME == "audio/mpeg", m.MIME == "audio/mp3":
			parts = append(parts, shell3.Part{Kind: shell3.PartAudio, Data: m.Bytes, MIME: m.MIME})
		default:
			// unsupported (e.g. OGG voice, PDF) — drop. See note re: voice transcoding.
		}
	}
	return parts
}
