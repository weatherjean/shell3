//go:build unix

package telegram

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/mdhtml"
)

// tgMaxMessage is the chunking budget in UTF-16 code units — the unit
// Telegram actually counts (its hard cap is 4096; 4000 leaves headroom for
// the HTML entities mdhtml adds).
const tgMaxMessage = 4000

// drainTurn consumes a turn's event channel and returns the assistant text.
// Only the turn's FINAL assistant message is the reply: text emitted before a
// tool call is progress narration ("Let me check…"), so each ToolCall resets
// the segment. narrationFallback keeps the last non-empty pre-tool segment as
// a fallback for turns that end on a tool call — user turns want it (the user
// should get SOMETHING); wake turns pass false, where an empty final segment
// means silence and a stale fragment must not post as agent mail. Errors
// always surface, appended after the reply. Channel close is the
// authoritative end-of-turn signal.
func (b *Bot) drainTurn(ch <-chan shell3.Event, narrationFallback bool) string {
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
	if reply == "" && narrationFallback {
		reply = last
	}
	return strings.TrimSpace(reply + errs.String())
}

// utf16Len is the length Telegram bills a string at: UTF-16 code units
// (astral-plane runes — emoji — count double).
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r > 0xFFFF {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// chunk splits s into pieces no longer than tgMaxMessage UTF-16 code units —
// the unit Telegram actually enforces its 4096 cap in (byte or rune counting
// under-splits emoji/CJK text and loses the chunk to a 400) — preferring
// newline boundaries and never cutting mid-rune.
func chunk(s string) []string {
	if utf16Len(s) <= tgMaxMessage {
		return []string{s}
	}
	var out []string
	for utf16Len(s) > tgMaxMessage {
		// Find the byte offset where the UTF-16 budget runs out.
		budget, cut := tgMaxMessage, len(s)
		for i, r := range s {
			w := 1
			if r > 0xFFFF {
				w = 2
			}
			if budget < w {
				cut = i
				break
			}
			budget -= w
		}
		// Prefer the last newline inside the window.
		if nl := strings.LastIndex(s[:cut], "\n"); nl > 0 {
			cut = nl
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
func (b *Bot) sendReply(ctx context.Context, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	for _, c := range chunk(text) {
		// Render the agent's Markdown to Telegram-safe HTML so bold/italics/code
		// show up. If Telegram still rejects it, fall back to the raw text.
		html := mdhtml.ToTelegramHTML(c)
		if _, err := b.client.SendHTML(ctx, b.chatID, html, opts...); err != nil {
			_, _ = b.client.Send(ctx, b.chatID, c, opts...)
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

func (b *Bot) postReply(ctx context.Context, sess *shell3.Session, replyTo string, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	chunks := chunk(text)
	if len(chunks) > replyMaxChunks {
		b.postChunk(ctx, sess, replyTo, chunks[0], opts...)
		if id, err := b.client.SendDocument(ctx, b.chatID, "reply.md", []byte(text), "full reply", opts...); err == nil {
			b.recordSent(sess, id)
			return
		}
		chunks = chunks[1:] // document failed: degrade to posting the rest
	}
	for _, c := range chunks {
		b.postChunk(ctx, sess, replyTo, c, opts...)
	}
}

// postChunk posts one chunk through the HTML→plain fallback path and records
// the sent id.
func (b *Bot) postChunk(ctx context.Context, sess *shell3.Session, replyTo string, c string, opts ...SendOpt) {
	html := mdhtml.ToTelegramHTML(c)
	var id string
	var err error
	if replyTo != "" {
		if id, err = b.client.SendHTMLReply(ctx, b.chatID, html, replyTo, opts...); err != nil {
			id, _ = b.client.SendReply(ctx, b.chatID, c, replyTo, opts...)
		}
	} else {
		if id, err = b.client.SendHTML(ctx, b.chatID, html, opts...); err != nil {
			id, _ = b.client.Send(ctx, b.chatID, c, opts...)
		}
	}
	b.recordSent(sess, id)
}

// recordSent advances the conversation's anchor to a message the bot just
// sent, so agent mail and completion posts thread onto the latest message.
// No-op for a failed send.
func (b *Bot) recordSent(sess *shell3.Session, msgID string) {
	if sess == nil || msgID == "" {
		return
	}
	b.setAnchor(msgID)
}
