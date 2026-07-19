//go:build unix

package telegram

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/heartbeat"
	"github.com/weatherjean/shell3/internal/media"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Bot routes one Telegram chat to one shell3 Session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	sess   *shell3.Session
	chatID int64 // the single allowed chat

	workDir string // resolves relative paths for send_media_telegram

	mu           sync.Mutex         // guards cancelTurn + turnActive + turnHadVoice
	cancelTurn   context.CancelFunc // non-nil while a turn runs
	turnActive   bool               // true from turn start until its goroutine ends
	turnHadVoice bool               // true when the in-flight turn included (or was steered by) an audio/ attachment

	media     *media.Clients   // STT/describe/TTS/imagegen capabilities; nil when unconfigured
	voiceMode *media.ModeStore // per-chat inbound-voice-reply mode; nil when unconfigured

	askMu          sync.Mutex           // guards pending + askSeq + voiceMenuMsgID
	pending        map[string]chan bool // tool-call hook Ask id → answer channel
	askSeq         int                  // monotonic id source for Ask
	voiceMenuMsgID int                  // msgID of the most recent /voice menu, for its "vm|" callback edit

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

// SetMedia wires the bot's STT/describe/TTS capabilities and the per-chat
// inbound-voice-reply mode override. The host MUST call it at boot and again
// after every Runtime.Reload so transcription/description/speech use the
// fresh config. The image_generate host tool is NOT registered here — the
// host installs it via Runtime.SetSessionDecorator, which covers the main
// session, every subagent child session, and post-reload re-application
// uniformly.
func (b *Bot) SetMedia(c *media.Clients, modeStore *media.ModeStore) {
	b.media = c
	b.voiceMode = modeStore
}

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
	// Save attachments to /tmp — fast, local, no network — and compute hadVoice
	// from their MIME types (also fast). The slow half (Transcribe/Describe,
	// preflight.go's preflightText) never runs on this loop: it's deferred into
	// the turn or interject goroutine below, so a hung media endpoint can never
	// stall Bot.Run's update loop (and /stop stays servicable).
	text := strings.TrimSpace(m.Text)
	saved := saveAttachments(m.Media)
	hadVoice := preflightScan(saved)
	if len(saved) == 0 {
		if len(m.Media) > 0 && text == "" {
			b.sendReply(ctx, "⚠️ couldn't save that attachment.")
			return
		}
		if text == "" {
			return // nothing actionable
		}
	}
	// composeText runs preflightText (network) then applies the same
	// text-vs-injected composition and reply-quote wrapping handleMsg used to
	// do inline. Whenever saved is empty, preflightText is a no-op (no lines,
	// no note) so this degrades to plain withReplyContext(text, ...).
	composeText := func(pctx context.Context) string {
		out := text
		if injected := b.preflightText(pctx, saved); injected != "" {
			if out != "" {
				out += "\n\n" + injected
			} else {
				out = injected
			}
		}
		return withReplyContext(out, m.ReplyTo)
	}

	// turnActive is the authoritative "a turn is running" signal. If one is
	// already in flight, steer it via Interject (never blocks) instead of
	// starting a second turn.
	b.mu.Lock()
	if b.turnActive {
		if hadVoice {
			b.turnHadVoice = true // a voice interjection counts for inbound TTS mode too
		}
		b.mu.Unlock()
		// Preflight's network calls must not run on the update loop even for the
		// interject path: run them on their own goroutine, timed out against the
		// bot's lifetime ctx (not the running turn's turnCtx — Interject itself
		// never blocks the running turn).
		go func() {
			pctx, pcancel := context.WithTimeout(ctx, preflightTimeout)
			defer pcancel()
			b.sess.Interject(composeText(pctx))
		}()
		return
	}
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.turnHadVoice = hadVoice
	b.mu.Unlock()

	stopTyping := b.keepTyping(ctx)
	go func() {
		// Preflight runs first, inside the turn goroutine, under turnCtx: /stop's
		// cancelTurn aborts a hung transcription/description just like it aborts
		// the model call, and the timeout bounds it independently of /stop.
		pctx, pcancel := context.WithTimeout(turnCtx, preflightTimeout)
		finalText := composeText(pctx)
		pcancel()
		ch := b.sess.Send(turnCtx, finalText)
		reply := b.drainTurn(ch)
		stopTyping()
		b.mu.Lock()
		b.cancelTurn = nil
		b.turnActive = false
		b.mu.Unlock()
		cancel()
		// A user-initiated turn always answers ("" renders as "(no output)");
		// only a stray edge sentinel is stripped.
		reply, _ = b.stripHeartbeat(reply)
		b.mu.Lock()
		turnVoice := b.turnHadVoice
		b.mu.Unlock()
		b.deliverReply(ctx, reply, turnVoice)
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
	lines := strings.Split(strutil.Truncate(replyTo, 1500), "\n")
	for i, ln := range lines {
		lines[i] = "> " + ln
	}
	return strings.Join(lines, "\n") + "\n\n" + text
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

// Busy reports whether the session is mid-turn or has running background
// jobs. It is the heartbeat ticker's skip signal: a tick that lands while the
// agent is working is dropped, not queued (the next tick covers it).
func (b *Bot) Busy() bool {
	b.mu.Lock()
	active := b.turnActive
	b.mu.Unlock()
	if active {
		return true
	}
	for _, j := range b.sess.Jobs() {
		if !j.Done {
			return true
		}
	}
	return false
}

// stripHeartbeat applies the HEARTBEAT_OK suppression when a heartbeat is
// configured: the sentinel is stripped from the reply's edge, and drop is true
// when nothing else remained (the turn needs no message). Without a heartbeat
// config the reply passes through untouched.
func (b *Bot) stripHeartbeat(reply string) (out string, drop bool) {
	if b.rt.HeartbeatConfig() == nil {
		return reply, false
	}
	return heartbeat.Strip(reply)
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
	if reply, drop := b.stripHeartbeat(reply); !drop && reply != "" {
		b.sendReply(ctx, reply)
	}
}
