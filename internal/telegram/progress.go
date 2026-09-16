//go:build unix

// progress.go renders a posted turn's activity as ONE self-editing chat bubble:
// posted (silently) as soon as the turn starts, edited in place as tools run
// (throttled for Telegram's flood limits), and deleted once the turn's real
// reply is delivered — mid-turn you see that the agent is working, afterwards
// the chat history stays clean. A turn that ends in an error keeps the bubble
// as a breadcrumb.
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

// start posts the initial working marker before the first model event. A
// reasoning-heavy round may take minutes before its first tool call; waiting
// for that call made the progress bubble flash only as the final answer landed.
func (p *progressBubble) start(ctx context.Context) {
	p.dirty = true
	p.flush(ctx, true)
}

// add records one tool line and posts/edits the bubble, throttled.
func (p *progressBubble) add(ctx context.Context, line string) {
	p.total++
	p.lines = append(p.lines, line)
	if len(p.lines) > progressMaxLines {
		p.lines = p.lines[len(p.lines)-progressMaxLines:]
	}
	p.dirty = true
	// Show the first actual tool immediately even when start just posted the
	// marker; later rapid calls use the ordinary edit throttle.
	p.flush(ctx, p.total == 1)
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
	chatID := p.c.chatIDValue()
	if p.msgID == "" {
		// Silent: a progress bubble must never ring the phone.
		id, err := p.c.b.client.Send(ctx, chatID, text, SendOpt{Silent: true})
		if err != nil {
			return // no bubble this turn; adds keep trying is pointless — stay quiet
		}
		p.msgID = id
	} else if err := p.c.b.client.EditPlain(ctx, chatID, p.msgID, text); err != nil {
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
	_ = p.c.b.client.DeleteMessage(ctx, p.c.chatIDValue(), p.msgID)
}

// toolLine renders one tool call as a compact single line.
func toolLine(name, rawArgs string) string {
	if name == "shell3" {
		var args struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal([]byte(rawArgs), &args)
		return "⚙️ " + hostActionLabel(args.Action)
	}
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

func hostActionLabel(action string) string {
	switch action {
	case "status":
		return "Checking shell3 host status"
	case "validate":
		return "Validating shell3 configuration"
	case "reload":
		return "Reloading shell3 configuration"
	case "restart":
		return "Requesting shell3 restart"
	case "poll":
		return "Scheduling a progress check"
	case "cancel_poll":
		return "Cancelling a progress check"
	default:
		return "Shell3 host action"
	}
}

// Host outcomes replace their pending line. Never echo arbitrary tool output
// into the bubble; detailed errors remain in the tool result and host context.
func (p *progressBubble) hostResult(ctx context.Context, output string, failed bool) {
	if len(p.lines) == 0 {
		return
	}
	var result struct {
		Action  string `json:"action"`
		OK      *bool  `json:"ok"`
		Restart string `json:"restart"`
	}
	if err := json.Unmarshal([]byte(output), &result); err != nil && !failed {
		return
	}
	if failed || (result.OK != nil && !*result.OK) {
		p.markError()
	} else {
		label := map[string]string{
			"status": "Shell3 host status checked", "validate": "Shell3 configuration valid",
			"reload": "Shell3 configuration reloaded for future turns", "poll": "Progress check scheduled",
			"cancel_poll": "Progress check cancelled",
		}[result.Action]
		if result.Action == "restart" {
			label = "Shell3 restart queued — after this reply"
			if result.Restart == "already_queued" {
				label = "Shell3 restart already queued — after this reply"
			}
		}
		if label == "" {
			return
		}
		p.lines[len(p.lines)-1] = "✓ " + label
		p.dirty = true
	}
	p.flush(ctx, true)
}

// drainTurnProgress drains a POSTED turn: the shared drain with a progress
// bubble, every assistant text segment retained, and any turn error appended
// to the reply (a posted turn always answers, so the user learns what went
// wrong).
func (c *conversation) drainTurnProgress(ctx context.Context, ch <-chan shell3.Event) (reply string, sawError bool) {
	p := &progressBubble{c: c}
	p.start(ctx)
	reply, errText, sawError := c.drainTurn(ctx, ch, p)
	if errText != "" {
		reply = strings.TrimSpace(reply + "\n" + errText)
	}
	return reply, sawError
}
