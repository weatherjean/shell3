//go:build unix

package telegram

import (
	"context"
	"strings"

	"github.com/weatherjean/shell3/internal/mdpage"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/telegram/mdhtml"
)

// tgMaxMessage is the chunking budget in UTF-16 code units, what Telegram
// counts. Its cap is 4096; 4000 leaves headroom for mdhtml's entities.
const tgMaxMessage = 4000

// drainTurn consumes a turn's events and returns the assistant text. ToolCall
// separates provider assistant messages. Posted turns keep every non-empty
// message in order: a model can put the actual answer before a late tool call,
// and dropping that text makes a later aside look like the whole reply.
// Quiet wake turns pass keepSegments=false and retain only the final message,
// where an empty final segment means silence and stale narration must not post
// as mail. Errors return separately so the caller decides whether they surface
// — otherwise every flaky cron wake posts noise.
// Channel close is the authoritative end-of-turn signal.
//
// A non-nil p is driven as the progress bubble and resolved at the end, kept
// as a breadcrumb only if the turn errored; nil drains with no bubble.
func (c *conversation) drainTurn(ctx context.Context, ch <-chan shell3.Event, p *progressBubble, keepSegments bool) (reply, errText string, sawError bool) {
	var seg strings.Builder // current assistant message
	var completed []string  // non-empty messages before tool calls
	var errs strings.Builder
	for ev := range ch {
		switch ev.Kind {
		case shell3.Token:
			seg.WriteString(ev.Text)
		case shell3.ToolCall:
			if keepSegments {
				if s := strings.TrimSpace(seg.String()); s != "" {
					completed = append(completed, s)
				}
			}
			seg.Reset()
			if p != nil {
				p.add(ctx, toolLine(ev.ToolName, ev.ToolInput))
			}
		case shell3.ToolResult:
			if ev.ToolError && p != nil {
				p.markError()
				p.flush(ctx, false)
			}
		case shell3.Error:
			if ev.Err != nil {
				sawError = true
				errs.WriteString("\n⚠️ " + ev.Err.Error())
				if h := shell3.RecoveryHint(ev.Err); h != "" {
					errs.WriteString("\n💡 " + h)
				}
			}
		case shell3.Usage, shell3.Done:
			c.observeContextUsage(ctx, ev.PromptTokens)
		case shell3.Compacted:
			c.observeCompaction(ctx, ev.PromptTokens)
		}
	}
	if p != nil {
		p.finish(ctx, sawError)
	}
	reply = strings.TrimSpace(seg.String())
	if keepSegments {
		if reply != "" {
			completed = append(completed, reply)
		}
		reply = strings.Join(completed, "\n\n")
	}
	return reply, strings.TrimSpace(errs.String()), sawError
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

// chunk splits s at tgMaxMessage UTF-16 code units, the unit Telegram enforces
// its cap in — byte or rune counting under-splits emoji and CJK and loses the
// chunk to a 400 — preferring newlines and never cutting mid-rune.
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

// sendReply posts chunked, unthreaded text: notices that are not a turn reply.
func (c *conversation) sendReply(ctx context.Context, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	chatID := c.chatIDValue()
	for _, part := range chunk(text) {
		// Markdown to Telegram-safe HTML so formatting shows, falling back to
		// raw text if Telegram still rejects it.
		html := mdhtml.ToTelegramHTML(part)
		id, err := c.b.client.SendHTML(ctx, chatID, html, opts...)
		if err != nil {
			id, _ = c.b.client.Send(ctx, chatID, part, opts...)
		}
		// Still a message from the bot: remembering its id lets a user reply
		// in a group instead of retyping an @mention.
		c.rememberSent(id)
	}
}

// Replies beyond replyMaxChunks send one preview chunk and an HTML document.
const replyMaxChunks = 2

// overflowDocName is what the attachment is called in the chat. Fixed, so a
// long reply always looks the same in the message list.
const overflowDocName = "reply.html"

func (c *conversation) postReply(ctx context.Context, sess *shell3.Session, replyTo string, text string, opts ...SendOpt) {
	if text == "" {
		text = "(no output)"
	}
	chunks := chunk(text)
	if len(chunks) > replyMaxChunks {
		_ = c.postChunk(ctx, sess, replyTo, chunks[0], opts...)
		page := mdpage.Render("shell3 — full reply", text)
		if id, err := c.b.client.SendDocument(ctx, c.chatIDValue(), overflowDocName, page, "full reply", opts...); err == nil {
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
	chatID := c.chatIDValue()
	var id string
	var err error
	if replyTo != "" {
		if id, err = c.b.client.SendHTMLReply(ctx, chatID, html, replyTo, opts...); err != nil {
			id, err = c.b.client.SendReply(ctx, chatID, part, replyTo, opts...)
		}
	} else {
		if id, err = c.b.client.SendHTML(ctx, chatID, html, opts...); err != nil {
			id, err = c.b.client.Send(ctx, chatID, part, opts...)
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
