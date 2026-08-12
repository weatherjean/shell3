//go:build unix

// media.go bundles the runtime's media capabilities with the one
// shell3.yaml default the Telegram front-end reads but *media.Clients does
// not carry (STTEcho). The bot keeps it here rather than pushing
// Telegram-shaped fields back into the shared media package.
package telegram

import (
	"github.com/weatherjean/shell3/internal/media"
)

// MediaCaps is the bot's view of the media capabilities: the runtime's
// media.Clients (Transcribe) plus the config default governing when the bot
// echoes a transcript.
type MediaCaps struct {
	media.Clients

	// STTEcho mirrors media.stt echo: whether an inbound voice note's
	// transcript is echoed back to the chat before the model turn runs.
	STTEcho bool
}
