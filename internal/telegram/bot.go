//go:build unix

package telegram

import (
	"context"
	"strings"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	approvals       *approvalRegistry
	approvalTimeout time.Duration // 0 → 5 min default; set in tests

	dashURL    string
	cancelTurn context.CancelFunc

	// onUsage, if set, receives each completed turn's token totals (per turn,
	// not accumulated). Wired by the host to a dashboard usage store.
	onUsage func(prompt, completion, total int)
}

// SetUsageRecorder registers a callback invoked with each turn's token totals.
func (b *Bot) SetUsageRecorder(fn func(prompt, completion, total int)) { b.onUsage = fn }

// NewBot wires a Bot. sess must be the runtime's persistent "telegram" session.
// dashURL is the URL to the dashboard (empty to disable).
func NewBot(client tgClient, rt *shell3.Runtime, sess *shell3.Session, chatID int64, dashURL string) *Bot {
	b := &Bot{
		client:    client,
		rt:        rt,
		sess:      sess,
		chatID:    chatID,
		approvals: newApprovalRegistry(),
		dashURL:   dashURL,
	}
	_ = sess.SetApprover(b.approve)
	return b
}

// Run consumes inbound messages and the wake bus until ctx is cancelled.
func (b *Bot) Run(ctx context.Context) {
	go b.consumeWakes(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-b.client.Updates(ctx):
			if !ok {
				return
			}
			b.handleMsg(ctx, m)
		}
	}
}

// handleMsg routes one inbound message.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	if m.ChatID != b.chatID {
		return // unauthorized: drop silently
	}
	if m.Callback != nil {
		b.handleCallback(ctx, m.Callback) // defined in approval.go
		return
	}
	if strings.HasPrefix(m.Text, "/") {
		b.handleCommand(ctx, m) // defined in commands.go
		return
	}
	// Transform any attachments into a text note: save the files to /tmp and
	// tell the agent where they are + which tool to use. We never forward media
	// bytes into the model — the agent ingests files itself with its own tools.
	text := strings.TrimSpace(m.Text)
	if note := attachmentNote(saveAttachments(m.Media), b.hasTool("read_media")); note != "" {
		if text != "" {
			text += "\n\n" + note
		} else {
			text = note
		}
	} else if len(m.Media) > 0 && text == "" {
		b.sendReply(ctx, "⚠️ couldn't save that attachment.")
		return
	}
	if text == "" {
		return // nothing actionable
	}
	// HasQueuedInput reports inbox state. In the single-chat v1 flow, handleMsg
	// is serial, so a running turn blocks here until the channel drains.
	// HasQueuedInput catches the case where a wake/cron item is already queued.
	if b.sess.HasQueuedInput() {
		// A turn may be running; Interject never blocks and steers it.
		b.sess.Interject(text)
		return
	}
	_ = b.client.Typing(ctx, b.chatID)
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	reply := b.drainTurn(b.sess.Send(turnCtx, text))
	b.cancelTurn = nil
	cancel()
	b.sendReply(ctx, reply)
}

// hasTool reports whether the active agent has the named tool enabled.
func (b *Bot) hasTool(name string) bool {
	for _, t := range b.sess.Snapshot().Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// consumeWakes pushes results when the session wakes (subagent/cron results).
// Single-consumer note: rt.Events() is one channel; the bot is its only consumer
// here (single session). If a future front-end shares the Runtime, route by ev.Session.
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			if ev.Kind != shell3.Wake || ev.Session != b.sess.Name() {
				continue
			}
			reply := b.drainTurn(b.sess.RunQueued(ctx))
			if reply != "" {
				b.sendReply(ctx, reply)
			}
		}
	}
}
