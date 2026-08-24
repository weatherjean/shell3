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
// return separately so the caller decides whether they surface (a wake turn
// that has nothing to say stays silent even when its provider hiccuped —
// otherwise every flaky cron wake posts ✉️ ⚠️ noise). Channel close is the
// authoritative end-of-turn signal.
func (c *conversation) drainTurn(ch <-chan shell3.Event, narrationFallback bool) (reply, errText string) {
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
	reply = strings.TrimSpace(seg.String())
	if reply == "" && narrationFallback {
		reply = last
	}
	return reply, strings.TrimSpace(errs.String())
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
func (c *conversation) sendReply(ctx context.Context, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	for _, part := range chunk(text) {
		// Render the agent's Markdown to Telegram-safe HTML so bold/italics/code
		// show up. If Telegram still rejects it, fall back to the raw text.
		html := mdhtml.ToTelegramHTML(part)
		id, err := c.b.client.SendHTML(ctx, c.chatID, html, opts...)
		if err != nil {
			id, _ = c.b.client.Send(ctx, c.chatID, part, opts...)
		}
		// A notice is still a message from the bot: remembering its id lets a
		// user reply to it in a group instead of retyping an @mention.
		c.rememberSent(id)
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

func (c *conversation) postReply(ctx context.Context, sess *shell3.Session, replyTo string, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	chunks := chunk(text)
	if len(chunks) > replyMaxChunks {
		_ = c.postChunk(ctx, sess, replyTo, chunks[0], opts...)
		if id, err := c.b.client.SendDocument(ctx, c.chatID, "reply.md", []byte(text), "full reply", opts...); err == nil {
			c.recordSent(sess, id)
			return
		}
		chunks = chunks[1:] // document failed: degrade to posting the rest
	}
	for _, part := range chunks {
		_ = c.postChunk(ctx, sess, replyTo, part, opts...)
	}
}

// postChunk posts one chunk through the HTML→plain fallback path and records
// the sent id. The returned error is the PLAIN fallback's — non-nil means
// neither rendering reached the transport, i.e. the chunk was not delivered.
// Most callers ignore it (a turn reply has no redelivery path); the
// completion router uses it to keep an undelivered post's outbox row.
func (c *conversation) postChunk(ctx context.Context, sess *shell3.Session, replyTo string, part string, opts ...SendOpt) error {
	html := mdhtml.ToTelegramHTML(part)
	var id string
	var err error
	if replyTo != "" {
		if id, err = c.b.client.SendHTMLReply(ctx, c.chatID, html, replyTo, opts...); err != nil {
			id, err = c.b.client.SendReply(ctx, c.chatID, part, replyTo, opts...)
		}
	} else {
		if id, err = c.b.client.SendHTML(ctx, c.chatID, html, opts...); err != nil {
			id, err = c.b.client.Send(ctx, c.chatID, part, opts...)
		}
	}
	c.recordSent(sess, id)
	return err
}

// recordSent advances the conversation's anchor to a message the bot just
// sent, so agent mail and completion posts thread onto the latest message.
// No-op for a failed send.
func (c *conversation) recordSent(sess *shell3.Session, msgID string) {
	if msgID == "" {
		return
	}
	// Remember the id whatever the session: in a group, a REPLY to one of the
	// bot's own messages is how a user says "this is for you" without typing
	// an @mention, and trigger.go answers that question from this ring.
	c.rememberSent(msgID)
	if sess == nil {
		return
	}
	c.setAnchor(msgID)
}
