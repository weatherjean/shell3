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
	workDir    string // resolves relative paths for send_media_telegram
	cancelTurn context.CancelFunc

	// onUsage, if set, receives each completed turn's token totals (per turn,
	// not accumulated). Wired by the host to a dashboard usage store.
	onUsage func(prompt, completion, total int)

	runJob        func(name string) error             // fires a cron job by name; nil if no scheduler
	reload        func() (shell3.ReloadResult, error) // performs a full config reload; nil if unset
	pendingReload bool                                // set by the reload tool mid-turn; applied at end-of-turn
}

// SetUsageRecorder registers a callback invoked with each turn's token totals.
func (b *Bot) SetUsageRecorder(fn func(prompt, completion, total int)) { b.onUsage = fn }

// SetJobRunner wires /run <job> to the scheduler's manual fire.
func (b *Bot) SetJobRunner(fn func(name string) error) { b.runJob = fn }

// SetReloader wires /reload (and the reload tool) to the host's reload coordinator.
func (b *Bot) SetReloader(fn func() (shell3.ReloadResult, error)) { b.reload = fn }

// decorateSession (re)applies the bot's host-level session customizations:
// the approval hook and host tools. Must be called after NewBot AND after every
// Runtime.Reload (which rebuilds s.cfg and drops these). Safe only when idle.
func (b *Bot) decorateSession() {
	_ = b.sess.SetApprover(b.approve)
	b.registerSendTool()
	b.registerReloadTool()
	b.registerStatusTool()
}

// RedecorateSession re-applies host tools + approver after a reload rebuilt s.cfg.
// Exported for the host reload coordinator (different package).
func (b *Bot) RedecorateSession() { b.decorateSession() }

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
	b.decorateSession()
	return b
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

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
	stopTyping := b.keepTyping(ctx)
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	reply := b.drainTurn(b.sess.Send(turnCtx, text))
	b.cancelTurn = nil
	cancel()
	stopTyping()
	b.sendReply(ctx, reply)
	b.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn
}

// keepTyping shows the "typing…" chat action and refreshes it every 4s until
// the returned stop is called. Telegram's chat action only lasts ~5s, so a long
// turn needs periodic re-sending or the indicator vanishes mid-turn.
func (b *Bot) keepTyping(ctx context.Context) (stop func()) {
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		_ = b.client.Typing(tctx, b.chatID)
		t := time.NewTicker(4 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-tctx.Done():
				return
			case <-t.C:
				_ = b.client.Typing(tctx, b.chatID)
			}
		}
	}()
	return cancel
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
			stopTyping := b.keepTyping(ctx)
			reply := b.drainTurn(b.sess.RunQueued(ctx))
			stopTyping()
			if reply != "" {
				b.sendReply(ctx, reply)
			}
		}
	}
}
