//go:build unix

package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/strutil"
	"github.com/weatherjean/shell3/internal/shell3"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	workDir string // resolves relative paths for send_media_telegram

	mu         sync.Mutex         // guards cancelTurn + turnActive
	cancelTurn context.CancelFunc // non-nil while a turn runs
	turnActive bool               // true from turn start until its goroutine ends

	askMu   sync.Mutex           // guards pending + askSeq
	pending map[string]chan bool // on_tool_call Ask id → answer channel
	askSeq  int                  // monotonic id source for Ask

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
		client:  client,
		rt:      rt,
		sess:    sess,
		chatID:  chatID,
		pending: make(map[string]chan bool),
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
	go b.consumeCallbacks(ctx)
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

// truncate caps s at ~n bytes with an ellipsis, never splitting a UTF-8 rune
// (rune-unsafe slicing would send Telegram invalid text).
func truncate(s string, n int) string { return strutil.Truncate(s, n) }

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
// A Wake fires when the session's inbox gained an item while idle — a subagent
// or cron job finished (its capped result summary is already queued as a
// notice), or a bg_done landed. Running the queued turn lets the agent narrate
// the result, and the reply flows to the chat like any other turn. Cron jobs
// with notify=false deliver their notice quietly (no Wake), so the chat stays
// silent until the next user message.
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			// The runtime keys HostEvent.Session on the session's store ID
			// (Session.ID()), not its runtime name — match on ID.
			if ev.Session != b.sess.ID() || ev.Kind != shell3.Wake {
				continue
			}
			b.runWakeTurn(ctx)
		}
	}
}

// runWakeTurn runs a queued follow-up turn, taking the same turnActive/
// cancelTurn slot as handleMsg so (a) a user message arriving mid-wake-turn is
// Interjected instead of bouncing off Send's ErrBusy, and (b) /stop can cancel
// a wake turn. If a turn already holds the slot, the wake is dropped — the
// running turn's unwind re-emits a Wake while the inbox is non-empty, so the
// queued notice is never stranded.
func (b *Bot) runWakeTurn(ctx context.Context) {
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		return
	}
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.mu.Unlock()

	stopTyping := b.keepTyping(ctx)
	reply := b.drainTurn(b.sess.RunQueued(turnCtx))
	stopTyping()
	b.mu.Lock()
	b.cancelTurn = nil
	b.turnActive = false
	b.mu.Unlock()
	cancel()
	if reply != "" {
		b.sendReply(ctx, reply)
	}
}
