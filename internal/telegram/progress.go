//go:build unix

// progress.go renders a turn's tool activity as ONE self-editing chat bubble:
// posted (silently) when the first tool fires, edited in place as tools run
// (throttled for Telegram's flood limits), and deleted once the turn's real
// reply is delivered — mid-turn you see what the agent is doing, afterwards
// the chat history stays clean. A turn that ends in an error keeps the bubble
// as a breadcrumb. Wake (quiet) turns show no bubble.
package telegram

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// progressEditInterval is the minimum spacing between bubble edits.
const progressEditInterval = 1500 * time.Millisecond

// progressMaxLines caps how many tool lines the bubble shows; older lines
// scroll off with a counter.
const progressMaxLines = 6

// progressBubble tracks one turn's tool-activity message. Not goroutine-safe:
// owned by the turn goroutine draining the event channel.
type progressBubble struct {
	c        *conversation
	msgID    string // "" until first post
	lines    []string
	total    int
	lastEdit time.Time
	dirty    bool
}

// add records one tool line and posts/edits the bubble, throttled.
func (p *progressBubble) add(ctx context.Context, line string) {
	p.total++
	p.lines = append(p.lines, line)
	if len(p.lines) > progressMaxLines {
		p.lines = p.lines[len(p.lines)-progressMaxLines:]
	}
	p.dirty = true
	p.flush(ctx, false)
}

// markError flags the last line as failed.
func (p *progressBubble) markError() {
	if len(p.lines) == 0 {
		return
	}
	p.lines[len(p.lines)-1] = "❌ " + strings.TrimPrefix(p.lines[len(p.lines)-1], "⚙️ ")
	p.dirty = true
}

// flush pushes the current state out, respecting the edit throttle unless
// force is set.
func (p *progressBubble) flush(ctx context.Context, force bool) {
	if !p.dirty {
		return
	}
	if !force && p.msgID != "" && time.Since(p.lastEdit) < progressEditInterval {
		return // next add or the final flush will carry it
	}
	text := p.render()
	if p.msgID == "" {
		// Silent: a progress bubble must never ring the phone.
		id, err := p.c.b.client.Send(ctx, p.c.chatID, text, SendOpt{Silent: true})
		if err != nil {
			return // no bubble this turn; adds keep trying is pointless — stay quiet
		}
		p.msgID = id
	} else if err := p.c.b.client.EditPlain(ctx, p.c.chatID, p.msgID, text); err != nil {
		return
	}
	p.lastEdit = time.Now()
	p.dirty = false
}

// render draws the bubble text.
func (p *progressBubble) render() string {
	var sb strings.Builder
	sb.WriteString("⚙️ working…")
	if p.total > len(p.lines) {
		sb.WriteString(" (" + strconv.Itoa(p.total) + " steps)")
	}
	for _, l := range p.lines {
		sb.WriteString("\n")
		sb.WriteString(l)
	}
	return sb.String()
}

// finish resolves the bubble at turn end: deleted after a clean turn, kept
// (with a final flush) as a breadcrumb after an error.
func (p *progressBubble) finish(ctx context.Context, keep bool) {
	if p.msgID == "" {
		return
	}
	if keep {
		p.flush(ctx, true)
		return
	}
	_ = p.c.b.client.DeleteMessage(ctx, p.c.chatID, p.msgID)
}

// toolLine renders one tool call as a compact single line.
func toolLine(name, rawArgs string) string {
	detail := ""
	var args map[string]any
	if json.Unmarshal([]byte(rawArgs), &args) == nil {
		for _, key := range []string{"command", "path", "description", "text", "query"} {
			if v, ok := args[key].(string); ok && strings.TrimSpace(v) != "" {
				detail = v
				break
			}
		}
	}
	line := "⚙️ " + name
	if detail != "" {
		line += " — " + strutil.Truncate(strings.Join(strings.Fields(detail), " "), 64)
	}
	return line
}

// drainTurnProgress drains a POSTED turn: the shared drain with a progress
// bubble, the narration fallback on, and any turn error appended to the reply
// (a posted turn always answers, so the user learns what went wrong).
func (c *conversation) drainTurnProgress(ctx context.Context, ch <-chan shell3.Event) (reply string, sawError bool) {
	reply, errText, sawError := c.drainTurn(ctx, ch, &progressBubble{c: c}, true)
	if errText != "" {
		reply = strings.TrimSpace(reply + "\n" + errText)
	}
	return reply, sawError
}
