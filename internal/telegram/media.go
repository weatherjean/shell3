//go:build unix

package telegram

import (
	"strings"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// audioMIMEs are the audio types the engine ingests as input_audio. Telegram
// voice notes are audio/ogg (Opus), passed through untranscoded.
var audioMIMEs = map[string]bool{
	"audio/wav": true, "audio/x-wav": true, "audio/mpeg": true, "audio/mp3": true,
	"audio/ogg": true, "audio/opus": true, "audio/oga": true, "audio/x-opus+ogg": true,
}

// mediaToParts converts resolved Telegram attachments to shell3 parts,
// dropping anything the engine can't ingest (images/audio only).
func mediaToParts(media []Media) []shell3.Part {
	var parts []shell3.Part
	for _, m := range media {
		mime := m.MIME
		if i := strings.IndexByte(mime, ';'); i >= 0 {
			mime = strings.TrimSpace(mime[:i])
		}
		switch {
		case strings.HasPrefix(mime, "image/"):
			parts = append(parts, shell3.Part{Kind: shell3.PartImage, Data: m.Bytes, MIME: mime})
		case audioMIMEs[mime]:
			parts = append(parts, shell3.Part{Kind: shell3.PartAudio, Data: m.Bytes, MIME: mime})
		default:
			// unsupported (e.g. PDF, video) — drop.
		}
	}
	return parts
}
