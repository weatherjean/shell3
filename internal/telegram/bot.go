//go:build unix

package telegram

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Bot routes one Telegram chat to the shell3 runtime under the mail model:
// every inbound message runs in its OWN runtime session (a fresh one for a new
// message, the thread's session when it is a Telegram reply), exactly one
// main-agent turn runs at a time, sending always succeeds (mid-turn mail
// queues; replies into one thread drain as one batch turn), and
// background-job completions arrive as quiet mail turns whose only path to
// the user is the mail_user tool. There is no single long-lived "telegram"
// session.
type Bot struct {
	client tgClient
	rt     *shell3.Runtime
	chatID int64 // the single allowed chat

	workDir string // resolves relative paths for send_media_telegram
	// configDir is the loaded config directory. send_media_telegram refuses to
	// send anything inside it (media dir excepted) — by path AND by inode, so
	// a symlink or hardlink cannot launder a credentials file out. "" disables
	// only those two checks; the rest of safeOpen still applies.
	configDir string

	threads *ThreadIndex // persistent Telegram message_id → session id map

	mu           sync.Mutex         // guards the turn slot + the live/thread maps + wakeQueue
	cancelTurn   context.CancelFunc // non-nil while a turn runs (main turn only — never a job)
	turnActive   bool               // true from turn start until its goroutine ends
	turnHadVoice bool               // true when the in-flight turn was initiated by an audio/ attachment

	// live maps a store-session id → its live Session, kept while the session
	// has running jobs or is mid-turn and dropped + Closed when it goes fully
	// idle (retireOrKeep). A wake is only honored for a session still in here.
	live map[string]*shell3.Session
	// lastMsg maps a store-session id → the thread's latest Telegram message id,
	// so a wake-turn reply posts into the right thread.
	lastMsg map[string]string
	// lastMailed maps a store-session id → the last mail_user text it sent,
	// so an identical repeat (a looping model) is refused instead of posted.
	lastMailed map[string]string
	// wakeQueue holds session ids whose Wake arrived while the turn slot was
	// taken; drained FIFO after each turn (startNextWork).
	wakeQueue []string
	// mailQueue holds user messages that arrived while the turn slot was
	// taken. Sending always succeeds — mail waits its turn. Drained FIFO
	// after each turn, before wakes (the user outranks background mail);
	// consecutive replies into one thread drain as a single batch turn.
	mailQueue []inMail
	// pinned holds store-session ids that must never be retired (AdoptSession).
	// The persistent "cron" dispatch parent is pinned so its jobs and runs keep
	// resolving; cron completion mail never wakes it (it runs as a fresh quiet
	// turn via StartFreshTurn), so it never runs a turn of its own.
	pinned map[string]bool

	media     *MediaCaps // STT/describe/TTS/imagegen capabilities; nil when unconfigured
	voiceMode *ModeStore // per-chat inbound-voice-reply mode; nil when unconfigured

	askMu          sync.Mutex // guards voiceMenuMsgID (historical name; the ask keyboard is gone)
	voiceMenuMsgID string     // msgID of the most recent /voice menu, for its "vm|" callback edit

	runJob        func(name string) error             // fires a cron job by name; nil if no scheduler
	reload        func() (shell3.ReloadResult, error) // performs a full config reload; nil if unset
	pendingReload bool                                // set by the reload tool mid-turn; applied at end-of-turn

	cancelJob func(id string) error // cancels one task for /cancel <id>; nil if unwired

	// jobsList and jobTranscript back /jobs and /job <id>: jobsList is the
	// runtime-wide background-job snapshot render.Jobs/render.JobDetail need
	// (shell3.Session.Jobs() reports the whole job runtime, not one session's
	// share — the wiring closure supplies it via any convenient live session);
	// jobTranscript looks up one job's captured output/transcript by id. Either
	// may be nil.
	jobsList      func() []shell3.JobInfo
	jobTranscript func(id string) string

	runsRoot string // project dir holding runs/ (Parts.RunsRoot()); "" disables /runs
	// runIndex maps a /run_N tap index → run id, written whole by the last
	// /runs render (guarded by b.mu). Taps resolve ONLY against this map —
	// never a re-derived listing — so a stale tap errors instead of opening
	// the wrong run. Empty until /runs is first rendered; lost on restart.
	runIndex     map[int]string
	version      string                      // shell3 version string, reported by /status
	cronLastRuns func() map[string]time.Time // cron job name -> last run time, for /cron; nil renders "never"
}

// NewBot wires a Bot. It holds no session — sessions are created per message
// (handleMsg) and per cron dispatch. threads is the bot-lifetime thread index
// (constructed once by the host and kept across /reload).
func NewBot(client tgClient, rt *shell3.Runtime, chatID int64, threads *ThreadIndex) *Bot {
	return &Bot{
		client:     client,
		rt:         rt,
		chatID:     chatID,
		threads:    threads,
		live:       make(map[string]*shell3.Session),
		lastMsg:    make(map[string]string),
		lastMailed: make(map[string]string),
		pinned:     make(map[string]bool),
	}
}

// SetJobRunner wires /run <job> to the scheduler's manual fire. Written from
// the reload path (which can run on a turn goroutine) and read by /run on the
// update loop, so it goes through b.mu like the rest of the mutable wiring.
func (b *Bot) SetJobRunner(fn func(name string) error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.runJob = fn
}

// jobRunner returns the current /run handler (nil when no scheduler is armed).
func (b *Bot) jobRunner() func(name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runJob
}

// SetReloader wires /reload (and the reload tool) to the host's reload coordinator.
func (b *Bot) SetReloader(fn func() (shell3.ReloadResult, error)) { b.reload = fn }

// SetJobControl wires /cancel <id> to the runtime's background-job cancel —
// tears one down (cascading to a subagent's own bash_bg children, same as the
// agent's task_cancel tool). The zero-token path for a phone-only user to
// kill a runaway job.
func (b *Bot) SetJobControl(cancel func(id string) error) { b.cancelJob = cancel }

// SetJobsSource wires /jobs and /job <id> to the runtime's background-job
// list (render.Jobs) and per-job transcript/output lookup (render.JobDetail).
// A nil list disables both commands ("job control not available"); a nil
// transcript still renders /job <id> detail, just with no Output section.
func (b *Bot) SetJobsSource(list func() []shell3.JobInfo, transcript func(id string) string) {
	b.jobsList, b.jobTranscript = list, transcript
}

// SetRunsRoot sets the project directory holding runs/ (Parts.RunsRoot()), so
// /runs and /runs <id> can list/replay stored sessions via
// render.RunsList/RunReplay. "" (the zero value) disables both.
func (b *Bot) SetRunsRoot(root string) { b.runsRoot = root }

// SetVersion sets the shell3 version string /status reports via render.Status.
func (b *Bot) SetVersion(v string) { b.version = v }

// SetCronLastRuns wires /cron's "last run" column to the scheduler's run
// history, keyed by job name. nil (or a job missing from the returned map)
// renders as "never".
func (b *Bot) SetCronLastRuns(fn func() map[string]time.Time) { b.cronLastRuns = fn }

// SetMedia wires the bot's STT/describe/TTS capabilities and the per-chat
// inbound-voice-reply mode override. The host MUST call it at boot and again
// after every Runtime.Reload so transcription/description/speech use the fresh
// config. The image_generate host tool is NOT registered here — the host
// installs it via Runtime.SetSessionDecorator, which covers every session and
// post-reload re-application uniformly.
//
// Both fields go through b.mu: a reload writes them from whichever goroutine
// called it (the agent's reload tool applies at end of turn, after the turn
// slot is released), while the update loop reads them in /voice and a turn
// goroutine reads them in preflight/deliverReply.
func (b *Bot) SetMedia(c *MediaCaps, modeStore *ModeStore) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.media = c
	b.voiceMode = modeStore
}

// mediaCaps returns the current media capabilities and voice-mode store as one
// consistent pair — SetMedia publishes them together, so readers must take them
// together.
func (b *Bot) mediaCaps() (*MediaCaps, *ModeStore) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.media, b.voiceMode
}

// SetWorkDir sets the directory used to resolve relative paths passed to
// send_media_telegram (the agent's session workdir).
func (b *Bot) SetWorkDir(dir string) { b.workDir = dir }

// SetConfigDir sets the config directory send_media_telegram refuses to send
// from (see safeOpen). Unset, the path- and inode-containment checks are
// skipped — so every front-end that registers the tool must supply it.
func (b *Bot) SetConfigDir(dir string) { b.configDir = dir }

// DecorateChatSession registers the bot's host tools (send_media_telegram,
// reload, status) on a main chat session. The host wires it into
// Runtime.SetSessionDecorator so every chat session — and every one rebuilt by
// a reload — gets the tools; subagent children (Headless) are skipped there.
func (b *Bot) DecorateChatSession(s *shell3.Session) {
	b.registerSendTool(s)
	b.registerMailTool(s)
	b.registerReloadTool(s)
	b.registerStatusTool(s)
}

// AdoptSession marks an externally-created session as live and pinned so its
// jobs and runs keep resolving. Used for the persistent "cron" dispatch
// parent, which has no inbound Telegram message and therefore no thread. Its
// cron completions are posted directly (PostCompletion with a cronJob name),
// so it is never woken and never runs a turn.
func (b *Bot) AdoptSession(s *shell3.Session) {
	b.mu.Lock()
	b.live[s.ID()] = s
	b.pinned[s.ID()] = true
	b.mu.Unlock()
}

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

// inMail is one received user message, attachments already saved, waiting for
// (or entering) its turn.
type inMail struct {
	m        Msg
	text     string
	saved    []savedFile
	hadVoice bool
}

// handleMsg routes one inbound message. It runs on the single update loop, so
// message handling is serialized; only the turn itself runs on its own
// goroutine.
func (b *Bot) handleMsg(ctx context.Context, m Msg) {
	if m.ChatID != b.chatID {
		return // unauthorized: drop silently
	}
	if strings.HasPrefix(m.Text, "/") {
		b.handleCommand(ctx, m) // defined in commands.go
		return
	}
	// A reply to a message the thread index doesn't know (a courtesy notice, a
	// cron post, a pre-index message) cannot be continued. The INTERFACE says
	// so with a fixed notice — no session, no model call, no guessing at
	// context that no longer exists.
	if m.ReplyToID != "" {
		if _, ok := b.threads.Lookup(m.ReplyToID); !ok {
			go func() {
				_, _ = b.client.SendReply(ctx, b.chatID,
					"🔗 can't continue from that message — it isn't part of a conversation I can resume. Send a fresh message, or reply within a thread that started from one of yours.", m.ID)
			}()
			return
		}
	}

	// Save attachments to the durable media dir — fast, local, no network — and
	// compute hadVoice from their MIME types (also fast). The slow half
	// (Transcribe/Describe, preflightText) runs inside the turn goroutine, never
	// on this loop.
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

	mail := inMail{m: m, text: text, saved: saved, hadVoice: hadVoice}

	// One main-agent turn at a time, globally. Mail semantics: sending always
	// succeeds — a message arriving mid-turn queues and is drained after the
	// running turn ends (replies into one thread drain as one batch turn).
	b.mu.Lock()
	if b.turnActive {
		b.mailQueue = append(b.mailQueue, mail)
		b.mu.Unlock()
		return
	}
	// Take the turn slot before resolving the session so a wake landing during
	// session creation queues instead of racing onto the slot.
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.turnHadVoice = hadVoice
	b.mu.Unlock()

	go b.runUserTurn(ctx, turnCtx, cancel, []inMail{mail})
}

// runUserTurn runs one user-initiated turn over batch (one message, or several
// queued replies into the same thread). The turn slot is held on entry; this
// goroutine owns delivery, retirement, slot release, and starting the next
// queued work.
func (b *Bot) runUserTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, batch []inMail) {
	head, last := batch[0], batch[len(batch)-1]
	sess, err := b.sessionFor(head.m)
	if err != nil {
		b.releaseSlot(cancel)
		b.sendReply(ctx, "⚠️ could not start a session: "+err.Error())
		// Work queued against the held slot during the failed creation would
		// otherwise wait for the next event — drain it now.
		b.startNextWork(ctx)
		return
	}
	if last.m.ID != head.m.ID {
		b.track(sess, last.m.ID) // the reply anchors at the newest message
	}

	hadVoice := false
	for _, mail := range batch {
		if mail.hadVoice {
			hadVoice = true
		}
	}
	b.mu.Lock()
	b.turnHadVoice = hadVoice
	b.mu.Unlock()

	composeText := func(pctx context.Context) string {
		parts := make([]string, 0, len(batch))
		for _, mail := range batch {
			out := mail.text
			if injected := b.preflightText(pctx, mail.saved, sess); injected != "" {
				if out != "" {
					out += "\n\n" + injected
				} else {
					out = injected
				}
			}
			parts = append(parts, withReplyContext(out, mail.m.ReplyTo))
		}
		return strings.Join(parts, "\n\n")
	}

	stopTyping := b.keepTyping(ctx)
	// Preflight runs first, inside the turn goroutine, under turnCtx: /stop's
	// cancelTurn aborts a hung transcription/description just like the model
	// call, and the timeout bounds it independently of /stop.
	pctx, pcancel := context.WithTimeout(turnCtx, preflightTimeout)
	finalText := composeText(pctx)
	pcancel()
	reply := b.drainTurn(sess.Send(turnCtx, finalText))
	stopTyping()
	b.mu.Lock()
	turnVoice := b.turnHadVoice
	b.mu.Unlock()
	// A user-initiated turn always answers ("" renders as "(no output)").
	// The turn slot is held through delivery AND retirement: while it is held
	// no other goroutine can claim this session (a mid-delivery user message
	// queues, a Wake queues, startNextWork waits), so retireOrKeep can decide
	// to Close an idle session with no concurrent turn to abort. Only then is
	// the slot released.
	b.deliverReply(ctx, reply, turnVoice, sess, last.m.ID)
	b.retireAndRelease(sess, cancel)
	b.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn (needs a free slot)
	b.startNextWork(ctx)
}

// sessionFor resolves the session an inbound message runs in: a Telegram reply
// to a recorded message resumes (or reuses the live instance of) that thread's
// session; a non-reply starts a fresh session. Replies to UNKNOWN ids never
// reach here — handleMsg answers them with a fixed can't-continue notice. The
// chosen session is recorded against m.ID as the thread anchor and marked live.
func (b *Bot) sessionFor(m Msg) (*shell3.Session, error) {
	if m.ReplyToID != "" {
		if id, ok := b.threads.Lookup(m.ReplyToID); ok {
			b.mu.Lock()
			s, isLive := b.live[id]
			b.mu.Unlock()
			if isLive {
				b.track(s, m.ID)
				return s, nil
			}
			s, err := b.rt.Session(shell3.SessionOpts{
				ResumeID: id, WorkDir: b.workDir,
			})
			if err != nil {
				return nil, err
			}
			b.track(s, m.ID)
			return s, nil
		}
	}
	s, err := b.rt.Session(shell3.SessionOpts{WorkDir: b.workDir})
	if err != nil {
		return nil, err
	}
	b.track(s, m.ID)
	return s, nil
}

// track records the message↔session mapping (thread anchor) and marks the
// session live.
func (b *Bot) track(s *shell3.Session, msgID string) {
	b.threads.Record(msgID, s.ID())
	b.mu.Lock()
	b.live[s.ID()] = s
	b.lastMsg[s.ID()] = msgID
	b.mu.Unlock()
}

// afterTurn runs the end-of-turn housekeeping for a wake turn: retire the
// session (while the slot is still held), release the slot, then start the next
// queued work (user mail first, then wakes). Entry REQUIRES the turn slot still
// held — retiring before releasing it is what closes the retire-vs-new-turn
// race.
func (b *Bot) afterTurn(ctx context.Context, sess *shell3.Session, cancel context.CancelFunc) {
	b.retireAndRelease(sess, cancel)
	b.startNextWork(ctx)
}

// startNextWork starts the next queued unit once the slot is free: queued user
// mail first (the user outranks background mail), then queued wakes.
func (b *Bot) startNextWork(ctx context.Context) {
	if b.drainNextMail(ctx) {
		return
	}
	b.startNextWake(ctx)
}

// drainNextMail pops the oldest queued user message — plus, when it is a reply,
// every other queued reply into the same thread — and runs the batch as one
// user turn. Returns false when the queue is empty or a turn is already
// running. Grouping resolves thread identity through the persistent thread
// index (not live-session pointers), so replies into a retired thread still
// batch correctly and resume it via sessionFor's ResumeID path.
func (b *Bot) drainNextMail(ctx context.Context) bool {
	b.mu.Lock()
	if b.turnActive || len(b.mailQueue) == 0 {
		b.mu.Unlock()
		return false
	}
	head := b.mailQueue[0]
	rest := b.mailQueue[1:]
	batch := []inMail{head}
	if head.m.ReplyToID != "" {
		if want, ok := b.threads.Lookup(head.m.ReplyToID); ok {
			kept := rest[:0]
			for _, mail := range rest {
				if mail.m.ReplyToID != "" {
					if got, ok := b.threads.Lookup(mail.m.ReplyToID); ok && got == want {
						batch = append(batch, mail)
						continue
					}
				}
				kept = append(kept, mail)
			}
			rest = kept
		}
	}
	b.mailQueue = append([]inMail{}, rest...)
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	go b.runUserTurn(ctx, turnCtx, cancel, batch)
	return true
}

// retireAndRelease retires sess and then releases the turn slot, in that order.
// Because retireOrKeep runs while the slot is STILL held, no concurrent turn can
// have claimed (or be mid-claim on) the session while its close is decided — a
// user follow-up is courtesy-dropped, a Wake queues, and startNextWake waits, so
// there is no window in which a claimed session is Closed out from under a turn.
func (b *Bot) retireAndRelease(sess *shell3.Session, cancel context.CancelFunc) {
	b.retireOrKeep(sess)
	b.releaseSlot(cancel)
}

// releaseSlot clears the turn slot (turnActive/cancelTurn) and cancels the
// turn ctx. Cancelling a turn whose model call already finished is harmless.
func (b *Bot) releaseSlot(cancel context.CancelFunc) {
	b.mu.Lock()
	b.cancelTurn = nil
	b.turnActive = false
	b.mu.Unlock()
	cancel()
}

// retireOrKeep closes a session that has gone fully idle (no running jobs, no
// queued inbox) so its store record ends and it stops receiving wakes; a
// session with live jobs (or queued input a wake will drain) stays open to
// receive their completions as thread follow-ups.
func (b *Bot) retireOrKeep(sess *shell3.Session) {
	b.mu.Lock()
	pinned := b.pinned[sess.ID()]
	b.mu.Unlock()
	if pinned {
		return // adopted sessions (cron parent) outlive their idle windows
	}
	if b.sessionHasRunningJob(sess) {
		return
	}
	// The inbox re-check and the live-map delete form ONE b.mu critical
	// section, pairing with WakeOwner's locked deliver: either the completion
	// note queues first (we keep the session; its Wake drains it) or the
	// delete lands first (WakeOwner sees the session gone and starts a fresh
	// turn instead). Without the shared lock a note could queue into a session
	// this close is about to end.
	b.mu.Lock()
	if sess.HasQueuedInput() {
		b.mu.Unlock()
		return // a wake will drain it
	}
	delete(b.live, sess.ID())
	delete(b.lastMsg, sess.ID())
	delete(b.lastMailed, sess.ID())
	b.mu.Unlock()
	_ = sess.Close()
}

// sessionHasRunningJob reports whether sess has a background job (bash_bg or
// subagent) still running. Session.Jobs() lists ALL runtime jobs, so filter by
// the parent registry name (JobInfo.ParentID == Session.Name()).
func (b *Bot) sessionHasRunningJob(sess *shell3.Session) bool {
	for _, j := range sess.Jobs() {
		if j.ParentID == sess.Name() && !j.Done {
			return true
		}
	}
	return false
}

// consumeWakes routes out-of-turn runtime events to the session that owns them.
// A Wake fires when a session's inbox gains an item while idle — a subagent
// finished, or a bash_bg landed. Running the queued turn lets that session's
// agent narrate the result back into its thread.
func (b *Bot) consumeWakes(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-b.rt.Events():
			if !ok {
				return
			}
			if ev.Kind != shell3.Wake {
				continue
			}
			b.dispatchWake(ctx, ev.Session)
		}
	}
}

// dispatchWake handles a Wake for store-session id: a wake for a retired/unknown
// session is dropped; if the turn slot is taken the id queues FIFO; otherwise
// its wake turn runs now.
func (b *Bot) dispatchWake(ctx context.Context, id string) {
	b.mu.Lock()
	sess, isLive := b.live[id]
	if !isLive {
		b.mu.Unlock()
		return
	}
	if b.turnActive {
		if !contains(b.wakeQueue, id) {
			b.wakeQueue = append(b.wakeQueue, id)
		}
		b.mu.Unlock()
		return
	}
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	go func() {
		b.runWakeTurn(turnCtx, sess)
		b.afterTurn(ctx, sess, cancel)
	}()
}

// startNextWake pops the first still-live queued wake id and runs its wake turn
// on a new goroutine, if the slot is free. Called after every turn ends.
func (b *Bot) startNextWake(ctx context.Context) {
	b.mu.Lock()
	if b.turnActive {
		b.mu.Unlock()
		return
	}
	var sess *shell3.Session
	for len(b.wakeQueue) > 0 {
		cand := b.wakeQueue[0]
		b.wakeQueue = b.wakeQueue[1:]
		if s, ok := b.live[cand]; ok {
			sess = s
			break
		}
	}
	if sess == nil {
		b.mu.Unlock()
		return
	}
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	go func() {
		b.runWakeTurn(turnCtx, sess)
		b.afterTurn(ctx, sess, cancel)
	}()
}

// takeSlotLocked marks a turn active and returns its ctx + cancel. Caller holds
// b.mu.
func (b *Bot) takeSlotLocked(ctx context.Context) (context.Context, context.CancelFunc) {
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.turnHadVoice = false
	return turnCtx, cancel
}

// runWakeTurn runs one queued mail turn on sess — QUIETLY. The turn's reply is
// not posted anywhere: background mail (a completion, a cron result) reaches
// the user only when the agent sends it with mail_user. The transcript still
// persists in the runs store. The turn slot is held on entry and stays held on
// return — the caller's afterTurn retires the session and releases the slot,
// in that order, so the slot spans the turn + retirement (see
// retireAndRelease). /stop can cancel it via the shared turn slot. Only
// ordinary threaded sessions (a subagent/bash_bg completion) and fresh
// StartFreshTurn sessions run wake turns; the pinned cron session is never
// woken.
func (b *Bot) runWakeTurn(turnCtx context.Context, sess *shell3.Session) {
	_ = b.drainTurn(sess.RunQueued(turnCtx))
}

// withReplyContext prepends the replied-to message as a capped markdown
// blockquote so the model sees what the user is responding to. Returns text
// unchanged when there's no reply. Kept for both the fresh-session fallback
// (unknown reply id) and known-thread replies — the model benefits from seeing
// the quoted portion either way.
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

// The Bot implements shell3.CompletionHost: floor/direct completion posts land
// as ⏰/🔔 chat posts (threaded into the owning conversation when one is
// live), wake verdicts resume the owning session or start a fresh main-agent
// turn. All three methods are invoked on job-runtime goroutines, so network
// sends run on their own goroutines and never stall the job runtime.
var _ shell3.CompletionHost = (*Bot)(nil)

// PostCompletion posts a completion message to the chat. p.CronJob != ""
// posts "⏰ <cronJob>: <text>" (a cron origin), otherwise "🔔 <text>". When
// p.OwnerID names a live threaded session the post lands as a reply in that
// thread and advances its anchor — replying to it continues the conversation.
// Cron and ownerless posts are standalone; replying to one gets the fixed
// can't-continue notice.
func (b *Bot) PostCompletion(p shell3.CompletionPost) {
	cronJob, ownerID, text := p.CronJob, p.OwnerID, p.Text
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	switch {
	case cronJob != "":
		text = fmt.Sprintf("⏰ %s: %s", cronJob, text)
	case strings.HasPrefix(text, "⚠️"):
		// The runtime's failure-floor text carries its own marker.
	default:
		text = "🔔 " + text
	}
	var sess *shell3.Session
	var replyTo string
	b.mu.Lock()
	if s, ok := b.live[ownerID]; ok && ownerID != "" && !b.pinned[ownerID] {
		sess, replyTo = s, b.lastMsg[ownerID]
	}
	b.mu.Unlock()
	go func() {
		ctx := context.Background()
		if sess != nil && replyTo != "" {
			b.postReply(ctx, sess, replyTo, text)
			return
		}
		b.sendReply(ctx, text)
	}()
}

// WakeOwner delivers note into the owning session and wakes it, iff that
// session is still live (and not the pinned cron parent). The live-check and
// the queueing run under b.mu as one critical section, pairing with
// retireOrKeep's locked re-check — a completion can land or the session can
// retire, never both. Returns false when the owner is gone; the caller then
// falls back to StartFreshTurn.
func (b *Bot) WakeOwner(ownerID, note string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	sess, ok := b.live[ownerID]
	if !ok || b.pinned[ownerID] {
		return false
	}
	sess.NotifyText(note) // queue + Wake; consumeWakes threads the turn's reply
	return true
}

// StartFreshTurn runs a fresh main-agent session over note — a completion with
// no live owner (cron results, orphans). The
// turn goes through the normal wake machinery, so it serializes on the
// one-turn-at-a-time slot (queueing FIFO behind an active turn, never
// dropped), and its reply posts as a new replyable thread.
func (b *Bot) StartFreshTurn(note string) {
	sess, err := b.rt.Session(shell3.SessionOpts{WorkDir: b.workDir})
	if err != nil {
		// Degrade to a raw post rather than dropping the completion.
		go b.sendReply(context.Background(), "🔔 "+note)
		return
	}
	b.mu.Lock()
	b.live[sess.ID()] = sess
	b.mu.Unlock()
	sess.NotifyText(note)
}

// renderInbox reports what is queued while (or since) a turn runs: the user's
// pending mail, wake-queued sessions, and live sessions holding undrained
// agent mail. Zero-token, deterministic — the /inbox command.
func (b *Bot) renderInbox() string {
	b.mu.Lock()
	mail := append([]inMail{}, b.mailQueue...)
	wakes := append([]string{}, b.wakeQueue...)
	var pending []string
	for id, s := range b.live {
		if !b.pinned[id] && s.HasQueuedInput() {
			pending = append(pending, id)
		}
	}
	active := b.turnActive
	b.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("📥 inbox\n")
	if len(mail) == 0 && len(wakes) == 0 && len(pending) == 0 {
		if active {
			return "📥 inbox empty — a turn is running, nothing queued behind it"
		}
		return "📥 inbox empty"
	}
	for _, m := range mail {
		label := "new thread"
		if m.m.ReplyToID != "" {
			label = "reply"
		}
		text := strings.TrimSpace(m.text)
		if text == "" {
			text = "(attachment)"
		}
		fmt.Fprintf(&sb, "· from you (%s): %s\n", label, strutil.Truncate(text, 80))
	}
	for _, id := range wakes {
		fmt.Fprintf(&sb, "· agent mail queued for its turn (session %s)\n", strutil.Truncate(id, 24))
	}
	for _, id := range pending {
		if contains(wakes, id) {
			continue
		}
		fmt.Fprintf(&sb, "· agent mail waiting in session %s\n", strutil.Truncate(id, 24))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// anyLiveSession returns some live session for host code that wants one but
// doesn't care which — e.g. /status's render.Status snapshot. Returns nil
// when nothing is live; callers must tolerate that (render.Status does).
func (b *Bot) anyLiveSession() *shell3.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.live {
		return s
	}
	return nil
}

// hasTool reports whether sess's active agent has the named tool enabled.
func (b *Bot) hasTool(sess *shell3.Session, name string) bool {
	if sess == nil {
		return false
	}
	for _, t := range sess.Snapshot().Tools {
		if t.Name == name {
			return true
		}
	}
	return false
}

// contains reports whether ss contains s.
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
