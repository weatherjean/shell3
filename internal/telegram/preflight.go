//go:build unix

package telegram

import (
	"context"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// mediaNotice surfaces a media-capability failure to the chat as a compact
// ⚠️ line: the user should see WHY a voice note went untranscribed — not just
// watch the capability silently degrade. Provider errors can embed whole
// JSON bodies, so the text is capped.
func (b *Bot) mediaNotice(ctx context.Context, what string, err error) {
	b.sendReply(ctx, "⚠️ "+what+": "+strutil.Truncate(err.Error(), 300))
}

// preflightTimeout bounds each turn's media preflight (Transcribe
// network calls). It caps how long a hung media endpoint can hold up a turn
// or an interject-path goroutine; it never blocks the update loop itself,
// which never calls preflight directly (see handleMsg in bot.go).
const preflightTimeout = 60 * time.Second

// preflightText is the slow, network-calling half of preflight: it turns
// saved attachments into the text block injected into the user's turn. Per
// attachment, by MIME prefix:
//   - audio/ + b.media.Transcribe configured: transcribe it. Success injects
//     the transcript as a bare quoted line; failure injects a fixed
//     could-not-transcribe marker and sends the error to the chat as a ⚠️
//     notice. On success, if b.media.STTEcho is set, the transcript is also
//     echoed to the chat as a separate message (not part of the turn's
//     eventual reply).
//
// The existing attachmentNote is always appended below any injected lines so
// file paths survive for the agent's own tools (bash/read_media). When
// b.media is nil, or a capability's function field is nil, preflightText's
// output is byte-identical to plain attachmentNote(saved, ...) — today's
// behavior.
//
// ctx should carry a deadline (see preflightTimeout): a cancelled/expired ctx
// flows into Transcribe naturally, degrading exactly like a
// transcription failure — the path note is still always
// appended.
//
// Callers must never run this on the update loop (internal/telegram/bot.go's
// Bot.Run): it makes blocking network calls. It always runs on a turn or
// interject goroutine, both of which pass an already-timeout-wrapped ctx.
func (b *Bot) preflightText(ctx context.Context, saved []savedFile, sess *shell3.Session) string {
	// One snapshot for the whole preflight: a reload landing mid-loop must not
	// transcribe with a stale config.
	caps := b.mediaCaps()
	var lines []string
	for _, s := range saved {
		if !strings.HasPrefix(s.MIME, "audio/") {
			continue
		}
		if caps == nil || caps.Transcribe == nil {
			continue
		}
		transcript, err := caps.Transcribe(ctx, s.Path)
		if err != nil {
			lines = append(lines, "[voice note could not be transcribed]")
			b.mediaNotice(ctx, "voice transcription failed", err)
			continue
		}
		lines = append(lines, `"`+transcript+`"`)
		if caps.STTEcho {
			b.sendReply(ctx, `📝 "`+transcript+`"`)
		}
	}
	if note := attachmentNote(saved, b.hasTool(sess, "read_media")); note != "" {
		lines = append(lines, note)
	}
	return strings.Join(lines, "\n")
}
