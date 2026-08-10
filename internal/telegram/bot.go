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

// Bot routes one Telegram chat to the shell3 runtime as ONE long-lived
// conversation: every message — bare or reply — continues the same main
// session (a Telegram reply adds quoted context, it never forks), /new is the
// only way to start over, and the session id persists in the runs store so a
// restart resumes the same conversation. Exactly one main-agent turn runs at
// a time; sending always succeeds (mid-turn mail queues and drains as one
// batch turn). Background-job completions queue into the SAME conversation as
// quiet mail turns whose only path to the user is the mail_user tool.
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

	// current persists the main conversation's session id across restarts
	// (the runs store's threads table, key "current-session").
	current *ThreadIndex

	mu           sync.Mutex         // guards the turn slot, the main session, and the queues
	cancelTurn   context.CancelFunc // non-nil while a turn runs (main turn only — never a job)
	turnActive   bool               // true from turn start until its goroutine ends
	turnHadVoice bool               // true when the in-flight turn was initiated by an audio/ attachment
	turnQuiet    bool               // true while a QUIET (wake/mail) turn holds the slot — steering must not target it

	// main is THE conversation: one long-lived session every message
	// continues, resumed across restarts via the current marker and replaced
	// only by /new. Lazily created on first use.
	main *shell3.Session
	// mainAnchor is the conversation's latest chat message id — replies and
	// agent mail thread onto it.
	mainAnchor string
	// steerAnchor is the newest STEERED user message id, held separately so
	// the running turn's own reply (recordSent advances mainAnchor to the
	// bot's sent message) can't clobber it — the steer-catchup reply must
	// thread to the user's message. Consumed (cleared) when a catch-up turn
	// starts.
	steerAnchor string
	// mailed holds every mail_user text sent this turn: an identical repeat
	// (a looping model) is refused instead of posted, and the turn's reply is
	// suppressed when it duplicates a mail the user already has.
	mailed []string
	// wakePending marks a Wake that arrived while the turn slot was taken;
	// drained after each turn (startNextWork).
	wakePending bool
	// mailQueue holds user messages that arrived while the turn slot was
	// taken. Sending always succeeds — mail waits its turn; the whole queue
	// drains as one batch turn (it is all the same conversation).
	mailQueue []inMail
	// burst buffers text messages for a short debounce window so Telegram's
	// split-message fragments (a long message arrives as several updates)
	// merge into ONE turn instead of one turn plus steers. burstTimer fires
	// the flush; debounce overrides the default window (tests shorten it).
	burst      []inMail
	burstTimer *time.Timer
	debounce   time.Duration
	// cronID is the adopted cron dispatch parent's session id — the jobs/runs
	// source that never runs turns and is never the main conversation.
	cronID string
	// cron is the adopted parent handle (jobs/runs source for /status views).
	cron *shell3.Session

	media     *MediaCaps  // STT/describe/TTS/imagegen capabilities; nil when unconfigured
	voiceMode *ModeStore  // per-chat inbound-voice-reply mode; nil when unconfigured
	quietMode *QuietStore // the /quiet toggle's store; nil = never quiet

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

// NewBot wires a Bot around ONE long-lived conversation. current persists
// which store session that is (constructed once by the host and kept across
// /reload).
func NewBot(client tgClient, rt *shell3.Runtime, chatID int64, current *ThreadIndex) *Bot {
	return &Bot{
		client:  client,
		rt:      rt,
		chatID:  chatID,
		current: current,
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

// SetQuiet installs the /quiet toggle's store. Nil (or an unset path) means
// quiet can never turn on — every post rings.
func (b *Bot) SetQuiet(s *QuietStore) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.quietMode = s
}

// isQuiet reports whether the /quiet toggle is on. Agent-initiated posts
// (⏰/🔔/✉️) send silently under it; replies to the user's own messages and
// ⚠️ failures always ring.
func (b *Bot) isQuiet() bool {
	b.mu.Lock()
	s := b.quietMode
	b.mu.Unlock()
	return s.Get() // nil-safe: a nil store reads as off
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

// AdoptSession registers the externally-created cron dispatch parent: the
// jobs/runs source that runs no turns, is never woken, and is never the main
// conversation.
func (b *Bot) AdoptSession(s *shell3.Session) {
	b.mu.Lock()
	b.cronID = s.ID()
	b.cron = s
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

	// Text messages ride a short debounce window first: Telegram splits a
	// long message into several updates arriving back to back, and the burst
	// merges them into ONE turn. Media messages flush the pending burst and
	// dispatch immediately (their preflight belongs on a turn goroutine).
	if len(saved) == 0 {
		b.mu.Lock()
		b.burst = append(b.burst, mail)
		if b.burstTimer == nil {
			window := b.debounce
			if window <= 0 {
				window = burstWindow
			}
			b.burstTimer = time.AfterFunc(window, func() { b.flushBurst(ctx) })
		}
		b.mu.Unlock()
		return
	}
	b.flushBurst(ctx)
	b.dispatchMail(ctx, []inMail{mail})
}

// burstWindow is the default text-message debounce.
const burstWindow = 400 * time.Millisecond

// flushBurst dispatches the buffered text burst, if any.
func (b *Bot) flushBurst(ctx context.Context) {
	b.mu.Lock()
	batch := b.burst
	if b.burstTimer != nil {
		b.burstTimer.Stop()
	}
	b.burst, b.burstTimer = nil, nil
	b.mu.Unlock()
	if len(batch) > 0 {
		b.dispatchMail(ctx, batch)
	}
}

// dispatchMail routes a message batch: mid-turn TEXT steers the running turn
// (injected at the next round boundary — "stop, wrong file" redirects work in
// flight instead of waiting behind it; a steer landing after the final
// boundary is answered by startNextWork's catch-up turn), media queues, and
// an idle bot runs the batch as one user turn.
func (b *Bot) dispatchMail(ctx context.Context, batch []inMail) {
	if len(batch) == 0 {
		return
	}
	hasMedia := false
	hadVoice := false
	for _, mail := range batch {
		hasMedia = hasMedia || len(mail.saved) > 0
		hadVoice = hadVoice || mail.hadVoice
	}
	b.mu.Lock()
	if b.turnActive {
		if !hasMedia && !b.turnQuiet && b.main != nil {
			sess := b.main
			b.mainAnchor = batch[len(batch)-1].m.ID
			b.steerAnchor = b.mainAnchor
			b.mu.Unlock()
			parts := make([]string, 0, len(batch))
			for _, mail := range batch {
				parts = append(parts, withReplyContext(mail.text, mail.m.ReplyTo))
			}
			sess.Interject(strings.Join(parts, "\n\n"))
			return
		}
		b.mailQueue = append(b.mailQueue, batch...)
		b.mu.Unlock()
		return
	}
	// Take the turn slot before resolving the session so a wake landing during
	// session creation queues instead of racing onto the slot.
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.turnHadVoice = hadVoice
	b.mu.Unlock()

	go b.runUserTurn(ctx, turnCtx, cancel, batch)
}

// runUserTurn runs one user-initiated turn over batch (one message, or the
// queued backlog). The turn slot is held on entry; this goroutine owns
// delivery, slot release, and starting the next queued work.
func (b *Bot) runUserTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, batch []inMail) {
	last := batch[len(batch)-1]
	sess, err := b.mainSession()
	if err != nil {
		b.releaseSlot(cancel)
		b.sendReply(ctx, "⚠️ could not start a session: "+err.Error())
		// Work queued against the held slot during the failed creation would
		// otherwise wait for the next event — drain it now.
		b.startNextWork(ctx)
		return
	}
	b.setAnchor(last.m.ID) // the reply anchors at the newest message

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
	reply, _ := b.drainTurnProgress(ctx, sess.Send(turnCtx, finalText))
	stopTyping()
	b.mu.Lock()
	turnVoice := b.turnHadVoice
	b.mu.Unlock()
	// A user-initiated turn always answers ("" renders as "(no output)").
	// The turn slot is held through delivery: while it is held no other
	// goroutine can start a turn (a mid-delivery user message queues, a Wake
	// queues, startNextWork waits).
	b.deliverReply(ctx, reply, turnVoice, sess, last.m.ID)
	b.releaseSlot(cancel)
	b.applyPendingReload(ctx) // self-evolution: agent edited config + called reload this turn (needs a free slot)
	b.startNextWork(ctx)
}

// mainSession returns THE conversation's session, creating it on first use:
// the persisted current-marker resumes the previous run's session (so a
// restart continues the same conversation); with no marker — or a marker the
// janitor swept — a fresh session starts and is recorded as current.
func (b *Bot) mainSession() (*shell3.Session, error) {
	b.mu.Lock()
	if b.main != nil {
		s := b.main
		b.mu.Unlock()
		return s, nil
	}
	b.mu.Unlock()

	var sess *shell3.Session
	if id, ok := b.current.Current(); ok {
		if s, err := b.rt.Session(shell3.SessionOpts{ResumeID: id, WorkDir: b.workDir}); err == nil {
			sess = s
		}
	}
	if sess == nil {
		s, err := b.rt.Session(shell3.SessionOpts{WorkDir: b.workDir})
		if err != nil {
			return nil, err
		}
		sess = s
	}

	b.mu.Lock()
	if b.main != nil { // lost a create race — keep the winner
		winner := b.main
		b.mu.Unlock()
		_ = sess.Close()
		return winner, nil
	}
	b.main = sess
	b.mu.Unlock()
	b.current.SetCurrent(sess.ID())
	return sess, nil
}

// mainIfLive returns the main session iff it already exists (nil otherwise) —
// for paths that must not create one as a side effect.
func (b *Bot) mainIfLive() *shell3.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.main
}

// setAnchor advances the conversation's latest chat message id.
func (b *Bot) setAnchor(msgID string) {
	if msgID == "" {
		return
	}
	b.mu.Lock()
	b.mainAnchor = msgID
	b.mu.Unlock()
}

// afterTurn runs the end-of-turn housekeeping for a wake turn: release the
// slot, then start the next queued work (user mail first, then wakes). The
// main session never retires — it IS the conversation.
func (b *Bot) afterTurn(ctx context.Context, cancel context.CancelFunc) {
	b.releaseSlot(cancel)
	b.startNextWork(ctx)
}

// startNextWork starts the next queued unit once the slot is free: queued user
// mail first (the user outranks background mail), then a steer catch-up (a
// steer that missed its turn's last round boundary still gets an answered
// turn), then pending wakes.
func (b *Bot) startNextWork(ctx context.Context) {
	if b.drainNextMail(ctx) {
		return
	}
	if b.startSteerCatchup(ctx) {
		return
	}
	b.startNextWake(ctx)
}

// startSteerCatchup runs a POSTED turn over user steering that landed in the
// session inbox after the previous turn's final round boundary. Unlike a wake
// (quiet) turn, its reply is delivered — the queued text is the user talking.
func (b *Bot) startSteerCatchup(ctx context.Context) bool {
	b.mu.Lock()
	if b.turnActive || b.main == nil || !b.main.HasQueuedSteer() {
		b.mu.Unlock()
		return false
	}
	sess := b.main
	anchor := b.takeSteerAnchorLocked()
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	go b.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
	return true
}

// takeSteerAnchorLocked consumes the pending steer anchor — the newest
// steered user message id — falling back to the conversation anchor when no
// steer recorded one. Caller must hold b.mu.
func (b *Bot) takeSteerAnchorLocked() string {
	anchor := b.steerAnchor
	b.steerAnchor = ""
	if anchor == "" {
		anchor = b.mainAnchor
	}
	return anchor
}

// drainNextMail drains the WHOLE queued-mail backlog as one batch turn — it
// is all the same conversation. Returns false when the queue is empty or a
// turn is already running.
func (b *Bot) drainNextMail(ctx context.Context) bool {
	b.mu.Lock()
	if b.turnActive || len(b.mailQueue) == 0 {
		b.mu.Unlock()
		return false
	}
	batch := b.mailQueue
	b.mailQueue = nil
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	go b.runUserTurn(ctx, turnCtx, cancel, batch)
	return true
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

// dispatchWake handles a Wake for store-session id. Only the main
// conversation runs wake turns; anything else (the cron parent, a /new'd-away
// session draining old jobs) is dropped — its completions re-route to the
// current main via the CompletionHost. If the turn slot is taken the wake is
// marked pending and drained after the running turn.
func (b *Bot) dispatchWake(ctx context.Context, id string) {
	b.mu.Lock()
	if b.main == nil || b.main.ID() != id {
		b.mu.Unlock()
		return
	}
	sess := b.main
	if b.turnActive {
		b.wakePending = true
		b.mu.Unlock()
		return
	}
	// User steering in the inbox upgrades the wake to a POSTED turn: the
	// user spoke (perhaps a steer that raced the previous turn's end), so the
	// reply must be delivered — RunQueued drains notices alongside it.
	if sess.HasQueuedSteer() {
		anchor := b.takeSteerAnchorLocked()
		turnCtx, cancel := b.takeSlotLocked(ctx)
		b.mu.Unlock()
		go b.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
		return
	}
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.turnQuiet = true
	b.mu.Unlock()
	go func() {
		b.runWakeTurn(turnCtx, sess)
		b.afterTurn(ctx, cancel)
	}()
}

// runPostedQueuedTurn is startSteerCatchup's turn body: drain the session
// inbox and DELIVER the reply (the queued input includes the user speaking).
func (b *Bot) runPostedQueuedTurn(ctx, turnCtx context.Context, cancel context.CancelFunc, sess *shell3.Session, anchor string) {
	stopTyping := b.keepTyping(ctx)
	reply, _ := b.drainTurnProgress(ctx, sess.RunQueued(turnCtx))
	stopTyping()
	b.deliverReply(ctx, reply, false, sess, anchor)
	b.releaseSlot(cancel)
	b.applyPendingReload(ctx)
	b.startNextWork(ctx)
}

// startNextWake runs a pending wake turn on the main session if the slot is
// free. Called after every turn ends. Queued user steering upgrades it to a
// posted turn (see dispatchWake).
func (b *Bot) startNextWake(ctx context.Context) {
	b.mu.Lock()
	if b.turnActive || !b.wakePending || b.main == nil {
		b.mu.Unlock()
		return
	}
	b.wakePending = false
	sess := b.main
	if !sess.HasQueuedInput() {
		b.mu.Unlock()
		return // already drained by the turn that just ran
	}
	if sess.HasQueuedSteer() {
		anchor := b.takeSteerAnchorLocked()
		turnCtx, cancel := b.takeSlotLocked(ctx)
		b.mu.Unlock()
		go b.runPostedQueuedTurn(ctx, turnCtx, cancel, sess, anchor)
		return
	}
	turnCtx, cancel := b.takeSlotLocked(ctx)
	b.turnQuiet = true
	b.mu.Unlock()
	go func() {
		b.runWakeTurn(turnCtx, sess)
		b.afterTurn(ctx, cancel)
	}()
}

// takeSlotLocked marks a turn active and returns its ctx + cancel. Caller holds
// b.mu.
func (b *Bot) takeSlotLocked(ctx context.Context) (context.Context, context.CancelFunc) {
	turnCtx, cancel := context.WithCancel(ctx)
	b.cancelTurn = cancel
	b.turnActive = true
	b.turnQuiet = false
	b.turnHadVoice = false
	// The mail_user dedupe guards against a looping model WITHIN a turn —
	// reset per turn, or a cron job legitimately mailing the same text on
	// consecutive runs would be silently suppressed.
	b.mailed = nil
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

// The Bot implements shell3.CompletionHost. With ONE conversation, delivery
// is simple: floor/direct posts thread onto the conversation's anchor, and
// every agent-bound completion — whatever session spawned it — queues into
// the CURRENT main session (WakeOwner for the main session itself,
// StartFreshTurn as the catch-all for cron and /new'd-away owners). All three
// methods are invoked on job-runtime goroutines, so network sends run on
// their own goroutines and never stall the job runtime.
var _ shell3.CompletionHost = (*Bot)(nil)

// PostCompletion posts a completion message to the chat, threaded onto the
// conversation. p.CronJob != "" posts "⏰ <cronJob>: <text>", otherwise
// "🔔 <text>" (⚠️ failure floors carry their own marker).
func (b *Bot) PostCompletion(p shell3.CompletionPost) {
	text := p.Text
	if strings.TrimSpace(text) == "" {
		text = "(no output)"
	}
	failure := strings.HasPrefix(text, "⚠️")
	switch {
	case p.CronJob != "":
		text = fmt.Sprintf("⏰ %s: %s", p.CronJob, text)
	case failure:
		// The runtime's failure-floor text carries its own marker.
	default:
		text = "🔔 " + text
	}
	// Under /quiet, background posts arrive without a ping — except failures,
	// which always ring.
	var opts []SendOpt
	if b.isQuiet() && !failure {
		opts = append(opts, SendOpt{Silent: true})
	}
	b.mu.Lock()
	sess, replyTo := b.main, b.mainAnchor
	b.mu.Unlock()
	go func() {
		ctx := context.Background()
		if sess != nil && replyTo != "" {
			b.postReply(ctx, sess, replyTo, text, opts...)
			return
		}
		b.sendReply(ctx, text, opts...)
	}()
}

// WakeOwner delivers note into the owning session iff it is the current main
// conversation. Any other owner — the cron parent, a session /new left
// behind — returns false, and the router's StartFreshTurn fallback lands the
// note in the current conversation instead. Nothing is ever lost.
func (b *Bot) WakeOwner(ownerID, note string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.main == nil || b.main.ID() != ownerID {
		return false
	}
	b.main.NotifyText(note) // queue + Wake; consumeWakes runs the quiet turn
	return true
}

// StartFreshTurn queues note into the main conversation (creating it if this
// is the very first activity) and wakes it — the catch-all delivery for
// completions whose owner isn't the current conversation (cron results,
// orphans, jobs outliving a /new).
func (b *Bot) StartFreshTurn(note string) {
	sess, err := b.mainSession()
	if err != nil {
		// Degrade to a raw post rather than dropping the completion.
		var opts []SendOpt
		if b.isQuiet() {
			opts = append(opts, SendOpt{Silent: true})
		}
		go b.sendReply(context.Background(), "🔔 "+note, opts...)
		return
	}
	sess.NotifyText(note)
}

// renderInbox reports what is queued while (or since) a turn runs: the user's
// pending mail, wake-queued sessions, and live sessions holding undrained
// agent mail. Zero-token, deterministic — the /inbox command.
func (b *Bot) renderInbox() string {
	b.mu.Lock()
	mail := append([]inMail{}, b.mailQueue...)
	agentMail := b.main != nil && b.main.HasQueuedInput()
	active := b.turnActive
	b.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("📥 inbox\n")
	if len(mail) == 0 && !agentMail {
		if active {
			return "📥 inbox empty — a turn is running, nothing queued behind it"
		}
		return "📥 inbox empty"
	}
	for _, m := range mail {
		text := strings.TrimSpace(m.text)
		if text == "" {
			text = "(attachment)"
		}
		fmt.Fprintf(&sb, "· from you: %s\n", strutil.Truncate(text, 80))
	}
	if agentMail {
		sb.WriteString("· agent mail waiting for its turn\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// anyLiveSession returns a session for host code that wants one but doesn't
// care which — the main conversation when it exists, else the adopted cron
// parent. Callers must tolerate nil (render.Status does).
func (b *Bot) anyLiveSession() *shell3.Session {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.main != nil {
		return b.main
	}
	return b.cron
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
