//go:build unix

package telegram

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/mdhtml"
)

const tgMaxMessage = 4096

// drainTurn consumes a turn's event channel and returns the assistant text.
// Only the turn's FINAL assistant message is the reply: text emitted before a
// tool call is progress narration ("Let me check…"), so each ToolCall resets
// the segment (keeping the last non-empty one as a fallback for turns that end
// on a tool call). Errors always surface, appended after the reply. Channel
// close is the authoritative end-of-turn signal.
func (b *Bot) drainTurn(ch <-chan shell3.Event) string {
	var seg strings.Builder // current assistant segment
	var last string         // last non-empty completed segment
	var errs strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case shell3.Token:
			seg.WriteString(ev.Text)
		case shell3.ToolCall:
			if s := strings.TrimSpace(seg.String()); s != "" {
				last = s
			}
			seg.Reset()
		case shell3.Error:
			if ev.Err != nil {
				errs.WriteString("\n⚠️ " + ev.Err.Error())
				if h := shell3.RecoveryHint(ev.Err); h != "" {
					errs.WriteString("\n💡 " + h)
				}
			}
		}
	}
	reply := strings.TrimSpace(seg.String())
	if reply == "" {
		reply = last
	}
	return strings.TrimSpace(reply + errs.String())
}

// chunk splits s into pieces no longer than tgMaxMessage bytes, preferring
// newline boundaries. Cuts land on rune boundaries — a mid-rune split would
// send Telegram invalid UTF-8, which it rejects with a 400 (losing the chunk).
func chunk(s string) []string {
	const max = tgMaxMessage
	if len(s) <= max {
		return []string{s}
	}
	var out []string
	for len(s) > max {
		cut := strings.LastIndex(s[:max], "\n")
		if cut <= 0 {
			cut = max
			for cut > 0 && !utf8.RuneStart(s[cut]) {
				cut--
			}
		}
		out = append(out, s[:cut])
		s = strings.TrimPrefix(s[cut:], "\n")
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}

// sendReply posts text to the chat, chunked and unthreaded. Used for notices
// (errors, acks, media captions) that are not a thread's turn reply.
func (b *Bot) sendReply(ctx context.Context, text string) {
	if text == "" {
		text = "(no output)"
	}
	for _, c := range chunk(text) {
		// Render the agent's Markdown to Telegram-safe HTML so bold/italics/code
		// show up. If Telegram still rejects it, fall back to the raw text.
		html := mdhtml.ToTelegramHTML(c)
		if _, err := b.client.SendHTML(ctx, b.chatID, html); err != nil {
			_, _ = b.client.Send(ctx, b.chatID, c)
		}
	}
}

// postReply posts a thread's turn reply, chunked. When replyTo != 0 each chunk
// is a Telegram reply to it (threading the conversation); every sent message id
// is recorded against sess so the thread's anchor advances and follow-up wakes
// reply to the latest message. replyTo == "" (the adopted cron session with no
// inbound message) posts plain.
// replyMaxChunks caps how many chat bubbles one reply may occupy. A longer
// reply posts its first chunk plus the full text as a reply.md document — the
// chat stays readable and the phone gets one ping, not twenty-five.
const replyMaxChunks = 2

func (b *Bot) postReply(ctx context.Context, sess *shell3.Session, replyTo string, text string) {
	if text == "" {
		text = "(no output)"
	}
	chunks := chunk(text)
	if len(chunks) > replyMaxChunks {
		b.postChunk(ctx, sess, replyTo, chunks[0])
		if id, err := b.client.SendDocument(ctx, b.chatID, "reply.md", []byte(text), "full reply"); err == nil {
			b.recordSent(sess, id)
			return
		}
		chunks = chunks[1:] // document failed: degrade to posting the rest
	}
	for _, c := range chunks {
		b.postChunk(ctx, sess, replyTo, c)
	}
}

// postChunk posts one chunk through the HTML→plain fallback path and records
// the sent id.
func (b *Bot) postChunk(ctx context.Context, sess *shell3.Session, replyTo string, c string) {
	html := mdhtml.ToTelegramHTML(c)
	var id string
	var err error
	if replyTo != "" {
		if id, err = b.client.SendHTMLReply(ctx, b.chatID, html, replyTo); err != nil {
			id, _ = b.client.SendReply(ctx, b.chatID, c, replyTo)
		}
	} else {
		if id, err = b.client.SendHTML(ctx, b.chatID, html); err != nil {
			id, _ = b.client.Send(ctx, b.chatID, c)
		}
	}
	b.recordSent(sess, id)
}

// recordSent advances a thread's anchor to a message the bot just sent, so a
// user reply to the bot's own message resumes the same session and a follow-up
// wake replies to the latest message. No-op for an adopted/plain session or a
// failed send.
func (b *Bot) recordSent(sess *shell3.Session, msgID string) {
	if sess == nil || msgID == "" {
		return
	}
	b.threads.Record(msgID, sess.ID())
	b.mu.Lock()
	b.lastMsg[sess.ID()] = msgID
	b.mu.Unlock()
}
