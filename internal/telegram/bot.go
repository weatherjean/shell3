//go:build unix

package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	workDir string // resolves relative paths for send_media_telegram

	runsDir string // .shell3_project/runs dir; backs /stop's KillAll (empty if unwired)

	mu         sync.Mutex         // guards cancelTurn + turnActive
	cancelTurn context.CancelFunc // non-nil while a turn runs
	turnActive bool               // true from turn start until its goroutine ends

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
// the host tools. Must be called after NewBot AND after every Runtime.Reload
// (which rebuilds s.cfg and drops these). Safe only when idle.
func (b *Bot) decorateSession() {
	b.registerSendTool()
	b.registerReloadTool()
	b.registerStatusTool()
}

// RedecorateSession re-applies host tools after a reload rebuilt s.cfg.
// Exported for the host reload coordinator (different package).
func (b *Bot) RedecorateSession() { b.decorateSession() }

// NewBot wires a Bot. sess must be the runtime's persistent "telegram" session.
func NewBot(client tgClient, rt *shell3.Runtime, sess *shell3.Session, chatID int64) *Bot {
	b := &Bot{
		client: client,
		rt:     rt,
		sess:   sess,
		chatID: chatID,
	}
	b.decorateSession()
	return b
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

// SetRunsDir wires the project's .shell3_project/runs directory used by /stop to
// kill tracked bg jobs (a finished/empty value disables that path).
func (b *Bot) SetRunsDir(dir string) { b.runsDir = dir }

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
	// If this is a Telegram reply/quote, prepend the quoted message so the model
	// sees the context the user is responding to.
	text = withReplyContext(text, m.ReplyTo)
	// turnActive is the authoritative "a turn is running" signal. If one is
	// already in flight, steer it via Interject (never blocks) instead of
	// starting a second turn.
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		b.sess.Interject(text) // steer the running turn; never blocks
		return
	}
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.mu.Unlock()

	stopTyping := b.keepTyping(ctx)
	ch := b.sess.Send(turnCtx, text)
	go func() {
		reply := b.drainTurn(ch)
		stopTyping()
		b.mu.Lock()
		b.cancelTurn = nil
		b.turnActive = false
		b.mu.Unlock()
		cancel()
		b.sendReply(ctx, reply)
		b.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn
	}()
}

// withReplyContext prepends the replied-to message as a capped markdown
// blockquote so the model sees what the user is responding to. Returns text
// unchanged when there's no reply.
func withReplyContext(text, replyTo string) string {
	replyTo = strings.TrimSpace(replyTo)
	if replyTo == "" {
		return text
	}
	lines := strings.Split(truncate(replyTo, 1500), "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n") + "\n\n" + text
}

// truncate caps s at n bytes, appending an ellipsis when it cuts.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// keepTyping shows the "typing…" chat action and refreshes it every 4s (the
// action expires after ~5s) until the returned stop is called.
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

// consumeWakes handles out-of-turn runtime events for this session.
// Single-consumer note: rt.Events() is one channel; the bot is its only consumer
// here (single session). If a future front-end shares the Runtime, route by ev.Session.
//
//   - Notice (cron/host-dispatch result): shown verbatim as a system message — no
//     model turn, so the operator sees the actual result and nothing is injected
//     into the agent's context.
//   - Wake (e.g. a subagent agent_done or a bg_done the agent itself requested,
//     delivered via the session sink): runs a queued turn so the agent reacts.
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			if ev.Session != b.sess.Name() {
				continue
			}
			switch ev.Kind {
			case shell3.Notice:
				if ev.Text != "" {
					b.sendReply(ctx, "🔔 "+ev.Text)
				}
			case shell3.Wake:
				stopTyping := b.keepTyping(ctx)
				reply := b.drainTurn(b.sess.RunQueued(ctx))
				stopTyping()
				if reply != "" {
					b.sendReply(ctx, reply)
				}
			}
		}
	}
}
