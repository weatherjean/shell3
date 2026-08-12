//go:build unix

// media.go bundles the runtime's media capabilities with the two shell3.yaml
// defaults the Telegram front-end reads but *media.Clients does not carry
// (STTEcho/TTSMode/Speech.VoiceCompatible). The bot keeps them here rather
// than pushing Telegram-shaped fields back into the shared media package.
package telegram

import (
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/media"
)

// MediaCaps is the bot's view of the media capabilities: the runtime's
// media.Clients (Transcribe/Speak) plus the config defaults
// governing when the bot echoes a transcript and when it speaks a reply.
type MediaCaps struct {
	media.Clients

	// STTEcho mirrors media.stt echo: whether an inbound voice note's
	// transcript is echoed back to the chat before the model turn runs.
	STTEcho bool
	// TTSMode mirrors media.tts mode: the configured default
	// ("off"/"inbound"/"always") for when outbound replies are synthesized.
	// The persisted /voice override (ModeStore) layers on top of it.
	TTSMode string
}

// voiceCompatible reports whether a synthesized speech file can be sent as a
// Telegram voice bubble rather than a plain audio document. media's speaker
// muxes both the opus and ogg tts formats into an .ogg container and gives
// every other format its own extension, so the container is the whole test.
func voiceCompatible(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".ogg")
}
