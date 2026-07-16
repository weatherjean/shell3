package shell3

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/strutil"
)

// Session is a live, multi-turn conversation. Obtain one via [Runtime.Session];
// the zero value is not usable. It streams a per-Send channel of translated
// Events. Drain a Send channel to completion before calling any between-turns
// method (the full list is on [ErrBusy]).
//
// The underlying chat.Session runs in synchronous-sink mode: each turn's events
// are delivered inline on the turn goroutine, which translates them onto the
// current Send channel and closes it when the turn returns. "turn finished" is
// simply "the turn goroutine returned".
type Session struct {
	cfg      chat.Config
	sess     *chat.Session
	handlers map[string]chat.ToolHandler

	// asker is the session's on_tool_call confirm runner, threaded into every
	// turn's TurnConfig.Asker (see
	// turnConfig). nil keeps on_tool_call ask-verdicts denying.
	asker func(ctx context.Context, command, reason string) bool

	// sink is the JSONL audit log, opened by Runtime.Session
	// (SessionOpts.OutPath) when the path is non-empty.
	// route writes every internal chat.Event to it (lossless) before
	// translating to a public Event; Close writes the "end" line. nil when no
	// OutPath was configured. sinkCleanup closes the underlying file.
	sink        *chat.OutSink
	sinkCleanup func()

	// runtime and name link a runtime-hosted session back to its registry so
	// Close deregisters it. name is an internal auto-generated bookkeeping
	// label (registry key + job-parent tracking), not a public identifier.
	runtime *Runtime
	name    string

	// opts is the SessionOpts this session was built from.
	opts SessionOpts

	// closeOnce makes Close safe under concurrent invocation: a spawned
	// subagent goroutine calls child.Close() at the same time Runtime.Close may
	// close the same child from its session map. The body runs exactly once;
	// later callers return the recorded error.
	closeOnce sync.Once
	closeErr  error

	// mu guards the current turn's routing target and lifecycle handles.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns
	curDone    <-chan struct{}    // current turn ctx's Done; unblocks a send to an abandoned cur on Close
	turnCancel context.CancelFunc // cancels the in-flight turn (nil before the first Send)
	turnDone   chan struct{}      // closed when the turn goroutine returns (nil before the first Send)
	sawError   bool               // any turn emitted an error event; drives the audit "end" status
	// busy is true from Send until its turn goroutine finishes. It turns a
	// contract violation (overlapping Send/Clear/Rollback/SwitchAgent/Prune,
	// which would race on unsynchronized session state) into ErrBusy instead
	// of a data race.
	busy bool
	// closed is set by doClose so a late Send (e.g. a Wake-driven queued drain
	// racing session teardown) is rejected with ErrClosed instead of running a
	// turn against the ended store record.
	closed bool
	// safetyOff auto-allows on_tool_call ask verdicts without prompting (the
	// front-ends' disable_safety toggle). Consulted at ask time, so a mid-turn
	// toggle applies to the next ask.
	safetyOff bool
}

// writeStartLine writes the audit log's opening line for this session.
// Safe to call regardless of whether a sink was opened: returns immediately
// when s.sink is nil so callers need not guard the call.
func (s *Session) writeStartLine(label string) {
	if s.sink == nil {
		return
	}
	_, model := chat.SplitStatus(s.cfg.StatusLine)
	s.sink.WriteStart(label, s.cfg.ModeLabel, model, s.cfg.OutPath, s.cfg.Headless)
}

// newSession wires a Session around an already-built chat.Config. The
// chat.Session runs in synchronous-sink mode: route translates each internal
// event and forwards it to the current Send channel inline on the turn
// goroutine. Split out from Start so tests can inject a fakellm-backed config.
func newSession(cfg chat.Config, opts SessionOpts) *Session {
	var storeID string
	var seed []llm.Message
	var resumedFrom string // non-empty when this session reattached to an existing run
	if cfg.Store != nil {
		// Reattach to the newest matching session when asked (and no explicit
		// ResumeID is given) so a front-end restart rejoins its conversation
		// instead of spawning a fresh run each boot.
		resumeID := opts.ResumeID
		if resumeID == "" && opts.ResumeLatest {
			if id, found, err := cfg.Store.LatestSession(cfg.WorkDir, cfg.ConfigPath); err != nil {
				chat.LogOrNoop(cfg.Log).Warn("resume-latest lookup failed", "error", err)
			} else if found {
				resumeID = id
			}
		}
		switch {
		case resumeID != "":
			storeID = resumeID
			resumedFrom = resumeID
			if msgs, err := cfg.Store.LoadMessages(resumeID); err == nil {
				seed = msgs
			} else {
				chat.LogOrNoop(cfg.Log).Warn("resume load failed", "session_id", resumeID, "error", err)
			}
		default:
			// Fresh run. Best-effort: a failed NewSession leaves storeID "" (no
			// persistence), logged at Warn so the silent non-persistence is
			// observable rather than vanishing.
			_, metaModel := chat.SplitStatus(cfg.StatusLine)
			if id, err := cfg.Store.NewSession(runs.Meta{
				Workdir:    cfg.WorkDir,
				ConfigPath: cfg.ConfigPath,
				Model:      metaModel,
			}); err == nil {
				storeID = id
			} else {
				chat.LogOrNoop(cfg.Log).Warn("start session failed", "error", err)
			}
		}
	}
	s := &Session{
		cfg:      cfg,
		handlers: chat.NewHandlers(),
		// Default to a no-op so Close is safe even when Start didn't open a
		// sink (and for tests that build a Session via newSession directly).
		sinkCleanup: func() {},
	}
	s.sess = chat.NewSession(chat.SessionOpts{
		StoreID:          storeID,
		InitialMessages:  seed,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
		Sink:             s.route,
		Store:            cfg.Store,
	})
	if resumedFrom != "" {
		if err := s.sess.RestoreReminders(); err != nil {
			chat.LogOrNoop(cfg.Log).Warn("restore reminders failed", "session_id", resumedFrom, "error", err)
		}
	}
	return s
}

// route is the chat.Session event sink. It runs synchronously on the in-flight
// turn goroutine, so all forwarding to a given Send channel happens-before that
// turn goroutine closes it — no separate drain, no close-ordering hazard. The
// select on curDone lets Close cancel the turn unblock a send to a Send channel
// the caller stopped reading. Events with no public equivalent are dropped.
//
// NOTE: curDone is the turn ctx's Done, which is also closed by an ordinary
// turn cancel (Ctrl-C/ESC), not just Close. So once a turn is cancelled this
// select MAY take the curDone branch and drop whatever it was delivering —
// INCLUDING the turn's terminal Done/Error event. Consumers must therefore
// treat channel close (see Send) as the authoritative end-of-turn
// signal; the terminal event is best-effort and can be absent on a cancel.
func (s *Session) route(ev chat.Event) {
	// Audit first, losslessly: the internal chat.Event keeps ToolCallID, system
	// reminders, and full untruncated content even though the public Event below
	// is a lossy projection. Independent of whether the event has a public form.
	if s.sink != nil {
		s.sink.WriteChatEvent(ev)
	}
	if ev.Kind == chat.EventError {
		s.mu.Lock()
		s.sawError = true
		s.mu.Unlock()
	}
	pub, ok := translate(ev)
	if !ok {
		return
	}
	// IsCustomTool can't be resolved in the pure translate (it has no config);
	// resolve it here against the session's current agent custom-tool set.
	if pub.Kind == ToolCall && s.cfg.CustomToolNames[pub.ToolName] {
		pub.IsCustomTool = true
	}
	s.mu.Lock()
	cur, done := s.cur, s.curDone
	s.mu.Unlock()
	if cur == nil {
		return
	}
	select {
	case cur <- pub:
	case <-done:
	}
}

// Interject delivers text to the session outside the Send contract: during a
// running turn it is injected at the next round boundary as a system reminder
// ("user interjected …"), letting the model course-correct mid-task; while
// idle it queues and is drained at the start of the next turn. Interject never
// fails, never blocks on a running turn, and is safe to call from any
// goroutine — it is the chat-message path for front-ends (a bot's
// incoming message), while Send remains the strict
// turn-starting call.
//
// Optional parts attach media: each invalid part is dropped — Interject never
// fails — and a bracketed "[attachment dropped: <error>]" note is appended to
// the queued text so the drop is visible to both the model and the audit
// reminder.
func (s *Session) Interject(text string, parts ...Part) {
	var cps []llm.ContentPart
	for _, p := range parts {
		cp, err := s.loadPart(p)
		if err != nil {
			text += "\n[attachment dropped: " + err.Error() + "]"
			continue
		}
		cps = append(cps, cp)
	}
	s.sess.Interject(text, cps...)
	// Idle steering must prod the host to run a turn; a busy session's running
	// turn drains the inbox itself, so don't wake (avoids a redundant turn).
	// Benign TOCTOU: isBusy() may flip between this check and the running turn
	// ending — worst case a missed wake (the next Send drains the item anyway)
	// or a spurious wake (RunQueued no-ops on an already-drained inbox). Same
	// reasoning as subagent delivery.
	if !s.isBusy() {
		s.wake()
	}
}

// wake emits a Wake for this session on the runtime bus (no-op without a
// runtime). Reachable from any goroutine via Interject, so it snapshots
// s.runtime under s.mu — mirroring WakeEvents — to avoid racing doClose's nil
// of s.runtime. The lock is not held across emit.
func (s *Session) wake() {
	if rt := s.runtimeHandle(); rt != nil {
		rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
	}
}

// RunQueued runs one turn seeded from the session's queued inbox items — the
// host's response to a Wake event. With an empty inbox (or a turn already in
// flight, which will itself drain the inbox) it returns an already-closed
// channel and starts no turn. Same ErrBusy contract as Send otherwise.
// closedEvents returns an already-closed event channel, carrying a single
// Error event when err is non-nil — the shape every rejected/no-op turn
// request returns.
func closedEvents(err error) <-chan Event {
	ch := make(chan Event, 1)
	if err != nil {
		ch <- Event{Kind: Error, Err: err}
	}
	close(ch)
	return ch
}

func (s *Session) RunQueued(ctx context.Context) <-chan Event {
	if s.isBusy() || !s.sess.HasInbox() {
		return closedEvents(nil)
	}
	// The turn loop drains the inbox at its top (the reminder + attachments
	// injection point), so an empty-prompt turn consumes the queued items as its
	// initiating input.
	return s.Send(ctx, "")
}

// HasQueuedInput reports whether interjected items are waiting (e.g. steering
// that arrived during a turn's final round). A host can call RunQueued to run a
// turn that consumes them.
func (s *Session) HasQueuedInput() bool { return s.sess.HasInbox() }

// Send runs one turn for prompt and returns a channel of that turn's events,
// closed when the turn ends (the deferred close(out) below always runs).
// Channel close is the authoritative end-of-turn signal: a terminal Done/Error
// event is emitted before close on a best-effort basis but may be dropped on
// cancel (see route), so consumers must bind end-of-turn UI/state transitions
// to close, not to receiving Done/Error.
//
// Single-turn-at-a-time contract: the caller MUST drain the returned channel
// to completion before calling Send again or any between-turns method (the
// full list is on [ErrBusy]). Those methods read and mutate unsynchronized
// session state (messages, cfg) and assume exactly one turn is active. The
// contract is enforced: a Send while a turn is in flight does not start a
// turn — it returns a channel that emits a single Error event carrying
// ErrBusy and closes. A Send after Close is rejected the same way with
// [ErrClosed].
//
// SendParts is the media-carrying variant; Send is SendParts with no parts.
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
	return s.SendParts(ctx, prompt, nil)
}

// SendParts runs one turn for prompt with media attachments. Same channel and
// ErrBusy contract as Send. Invalid parts (see Part) reject the whole call:
// the returned channel emits a single Error event carrying the first part's
// error and closes, without starting a turn — the session stays usable.
// Loading happens up front on the caller's goroutine (a Path part reads the
// file here, not on the turn goroutine), and therefore happens even when the
// call is subsequently rejected with ErrBusy.
func (s *Session) SendParts(ctx context.Context, prompt string, parts []Part) <-chan Event {
	cps, err := s.loadParts(parts)
	if err != nil {
		return closedEvents(err)
	}
	out := make(chan Event)
	turnCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	if s.busy || s.closed {
		err := ErrBusy
		if s.closed {
			err = ErrClosed
		}
		s.mu.Unlock()
		cancel()
		return closedEvents(err)
	}
	s.busy = true
	s.cur = out
	s.curDone = turnCtx.Done()
	s.turnCancel = cancel
	s.turnDone = done
	// Capture the runtime here (under the busy gate, after the ErrBusy
	// early-return) so the turn goroutine doesn't read s.runtime — doClose may
	// nil it concurrently once `done` closes (see the big defer's wake below).
	rt := s.runtime
	// Snapshot the turn config while still holding s.mu: the cfg-mutating
	// methods (SwitchAgent, SetParam, Clear, RegisterHostTool) hold s.mu, so
	// taking the copy inside the busy-gated critical section makes "busy set"
	// and "cfg read" atomic with respect to them — a mutator that slipped past
	// its isBusy check either lands wholly before this copy or is serialized
	// after it.
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	go func() {
		// route forwards events to out during the turn; once the turn returns no
		// further forwarding can happen, so clearing cur, clearing busy, and
		// closing out here is race-free (all run on this goroutine, strictly
		// after the turn).
		defer func() {
			s.mu.Lock()
			if s.cur == out {
				s.cur = nil
			}
			s.busy = false
			s.mu.Unlock()
			close(out)
			// Steering (or a subagent result) that arrived during the turn's final
			// round was queued but never drained — there was no next round boundary.
			// The session is now idle with a non-empty inbox, so Wake the host to run
			// a follow-up turn (RunQueued). Uses the captured rt, not s.runtime, to
			// avoid racing doClose's nil of s.runtime. Emitted after busy is cleared
			// so a host's RunQueued isn't rejected as busy.
			if rt != nil && s.sess.HasInbox() {
				rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
			}
			cancel() // release the child ctx
		}()
		defer close(done)
		s.sess.RunParts(turnCtx, tc, prompt, cps)
	}()
	return out
}

// SetSafetyOff toggles auto-allowing on_tool_call ask verdicts for this
// session — the host-side switch behind the front-ends' disable_safety
// command. While on, an ask verdict runs without prompting a human; block
// verdicts are unaffected. Off by default; takes effect immediately,
// including for asks fired later in an in-flight turn.
func (s *Session) SetSafetyOff(off bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.safetyOff = off
}

// SafetyOff reports whether ask verdicts are currently auto-allowed.
func (s *Session) SafetyOff() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.safetyOff
}

// runtimeHandle snapshots s.runtime under s.mu. Every accessor reachable from
// outside the turn goroutine must use this instead of reading s.runtime
// directly: doClose nils the field concurrently (under the same mutex).
func (s *Session) runtimeHandle() *Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime
}

// isBusy reports whether a turn is in flight (see Send's contract).
func (s *Session) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// withIdle runs fn while holding s.mu, with the busy gate checked inside the
// same critical section. Send sets busy under the same mutex, so a between-
// turns mutator that uses this can never interleave with a turn starting —
// the ErrBusy contract is enforced, not advisory. fn must not re-lock s.mu.
func (s *Session) withIdle(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.busy {
		return ErrBusy
	}
	return fn()
}

// ID returns the store session id (rolls on /clear; "" with no store).
func (s *Session) ID() string {
	return s.sess.ID()
}

// WakeEvents exposes the owning Runtime's out-of-turn event bus (Wake) so a
// single-session front-end created via Start can consume wakes for this session
// without holding a separate *Runtime handle. Returns nil when the session has
// no runtime (e.g. a closed session), in which case a host select on it simply
// never fires. Multi-session hosts should use Runtime.Events() directly.
func (s *Session) WakeEvents() <-chan HostEvent {
	rt := s.runtimeHandle()
	if rt == nil {
		return nil
	}
	return rt.Events()
}

// Close ends the conversation: cancels any in-flight turn, waits for it to
// finish (so its deferred history persist runs against the still-open store),
// then ends the store session and releases the config.
//
// Concurrency: Close must not be called concurrently with itself. A sequential
// second Close is a safe no-op: the turn-cancel and join are idempotent and
// the store, sink, and cleanup paths guard against double execution.
//
// For Start-owned sessions (the common single-session case), Close also tears
// down the private Runtime that Start created — the LLM client, store, and
// proxy spawner. For Runtime-hosted sessions created via
// Runtime.Session, Close deregisters the session from its Runtime (the shared
// parts remain alive for the other sessions).
//
// Close is robust to an abandoned Send channel: cancelling the turn ctx unblocks
// route's send to an unread channel (its curDone select fires), so the turn
// unwinds and the join below can't wedge. Draining the channel is still the
// supported pattern, but Close does not require it.
//
// Returns the store's EndSession error if ending the persisted session fails;
// the other best-effort teardown steps (turn cancel, cleanup) do not contribute
// to the returned error.
func (s *Session) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.doClose() })
	return s.closeErr
}

// doClose runs the teardown exactly once (guarded by closeOnce in Close).
func (s *Session) doClose() error {
	// Cancel any in-flight turn so it stops streaming and runs its deferred
	// history persist, then join it before ending the store session so a
	// cancelled turn isn't still writing to the store as EndSession runs.
	// closed is set first so a Send racing this teardown (e.g. a Wake-driven
	// queued drain) is rejected instead of starting a turn on the ended record.
	s.mu.Lock()
	s.closed = true
	cancel := s.turnCancel
	done := s.turnDone
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done // turn goroutine (and its deferred history persist) has finished
	}
	s.sess.End(chat.StatusOK)
	var endErr error
	if s.cfg.Store != nil {
		endErr = s.cfg.Store.EndSession(s.sess.ID())
	}
	// Flush the audit log: by here the turn goroutine has joined, so no route
	// call can still be writing to the sink. Then release the file and config.
	if s.sink != nil {
		status := "ok"
		s.mu.Lock()
		if s.sawError {
			status = "error"
		}
		s.mu.Unlock()
		s.sink.WriteEnd(status)
	}
	s.sinkCleanup()
	// Capture and nil s.runtime under s.mu so a concurrent WakeEvents() reader
	// (a public accessor bot binaries call) never races this write. Only the
	// field access is locked: rt.forget/rt.cleanup run after the unlock, since
	// holding s.mu across them is unnecessary and could invite a lock-order
	// deadlock with the runtime's own locking.
	s.mu.Lock()
	rt := s.runtime
	s.runtime = nil
	s.mu.Unlock()
	if rt != nil {
		rt.forget(s.name)
	}
	return endErr
}

// RollbackHint returns a short suggestion to roll back the last turn when err
// looks like a provider HTTP 400 (Bad Request) — which usually means the last
// turn left the conversation in a state the model rejects (e.g. a bad tool
// message or unsupported content), and undoing it recovers. Returns "" for
// other errors (auth 401, rate-limit 429, network, 5xx), where rollback would
// not help. Front-ends append it to the error they show, naming their own
// /rollback command.
func RollbackHint(err error) string {
	if err == nil {
		return ""
	}
	const hint = "This usually means the last turn left the conversation in a state the model rejects — /rollback will likely fix it."
	// Preferred: the adapter wraps provider API errors in llm.StatusError.
	var se *llm.StatusError
	if errors.As(err, &se) {
		if se.Code == 400 {
			return hint
		}
		return ""
	}
	// Fallback for errors that lost the typed shell (e.g. proxies or paths that
	// stringified the error).
	s := err.Error()
	if strings.Contains(s, "400 Bad Request") || strings.Contains(s, `"http_code":"400"`) {
		return hint
	}
	return ""
}

// turnConfig locks s.mu and derives the per-turn config; see turnConfigLocked.
// Test-only convenience — SendParts snapshots inside its own critical section.
func (s *Session) turnConfig() chat.TurnConfig {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.turnConfigLocked()
}

// turnConfigLocked derives the per-turn config from the current cfg. Built
// fresh each turn so SwitchAgent's mutations to cfg take effect on the next
// Send. Caller must hold s.mu: cfg and the session wiring fields it reads are
// mutated by the mu-holding between-turns methods.
func (s *Session) turnConfigLocked() chat.TurnConfig {
	cfg := s.cfg
	tc := chat.NewTurnConfig(cfg, s.handlers)
	baseAsker := s.asker
	// t.headless for the on_tool_call chain: no attached asker means an ask
	// verdict would degrade to deny. Per-session, recomputed every turn.
	tc.HeadlessAsk = baseAsker == nil
	tc.Asker = func(ctx context.Context, command, reason string) bool {
		// SafetyOff is read at ask time (not turn start) so a mid-turn
		// disable_safety toggle applies to the very next ask.
		if s.SafetyOff() {
			return true
		}
		if baseAsker == nil {
			return false // no human available: ask degrades to deny
		}
		return baseAsker(ctx, command, reason)
	}
	if s.runtime != nil && s.runtime.jobs != nil {
		rt := s.runtime
		parent := s
		tc.StartBashBg = func(command, workdir string, argv, env []string) (string, error) {
			return rt.jobs.startCommand(parent, command, workdir, argv, env)
		}
		allowed := cfg.Subagents // the active agent's tools.subagents allowlist
		tc.StartSubagent = func(agent, prompt, desc string) (string, error) {
			// Enforce the allowlist the task tool's schema advertises: only the
			// names in tools.subagents may be spawned, never an arbitrary declared
			// agent. An empty allowlist means this agent may not delegate at all.
			if !slices.Contains(allowed, agent) {
				if len(allowed) == 0 {
					return "", errors.New("this agent has no subagents configured (tools.subagents is empty)")
				}
				return "", fmt.Errorf("subagent_type %q is not allowed for this agent; allowed subagents: %s", agent, strings.Join(allowed, ", "))
			}
			// Single-level delegation is enforced by construction: subagents are
			// never granted the task tool, so this closure only runs on top-level
			// sessions.
			return rt.jobs.startSubagent(parent, agent, prompt, desc, subagentOpts{})
		}
		tc.ListJobs = func() string {
			return rt.jobs.formatJobList()
		}
		tc.JobStatus = func(id string) string {
			return rt.jobs.formatJobStatus(id)
		}
		tc.CancelJob = func(id string) string {
			return rt.jobs.formatJobCancel(id)
		}
	}
	return tc
}

// Compact forces one context compaction now (= /compact): it summarises the
// head of the conversation and keeps the recent tail, exactly like the
// automatic compact_at path, and returns the estimated prompt tokens
// before/after. ErrBusy while a turn is in flight; chat.ErrNothingToCompact
// when history is too small to have a summarisable head (history untouched on
// any error). Unlike the other between-turns mutators it does NOT run under
// withIdle — the summarisation round-trip can take minutes and must not hold
// s.mu; instead it takes the FULL turn lifecycle (mirroring SendParts): the
// busy gate, plus turnCancel/turnDone registration so /stop can abort the LLM
// call and doClose cancels+joins before ending the store session — the
// compaction rolls the runs session, so teardown must never race it.
func (s *Session) Compact(ctx context.Context) (before, after int, err error) {
	cctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	s.mu.Lock()
	if s.busy || s.closed {
		err := ErrBusy
		if s.closed {
			err = ErrClosed
		}
		s.mu.Unlock()
		cancel()
		return 0, 0, err
	}
	s.busy = true
	s.turnCancel = cancel
	s.turnDone = done
	// Capture the runtime under the busy gate (doClose nils it concurrently once
	// done closes) and snapshot the turn config inside the same critical section,
	// same as SendParts: cfg mutators (SwitchAgent, SetParam, Clear) hold s.mu,
	// so they serialize wholly before or after this compaction.
	rt := s.runtime
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.busy = false
		s.mu.Unlock()
		close(done) // doClose may be blocked on this join; store state is final
		// A notice queued while the busy gate was held had its Wake bounced off
		// RunQueued's ErrBusy; re-emit exactly as SendParts' unwind does, so a
		// completion that landed mid-compaction is never stranded.
		if rt != nil && s.sess.HasInbox() {
			rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
		}
		cancel() // release the child ctx
	}()
	return chat.CompactStandalone(cctx, tc, s.sess)
}

// CompactReplyText renders a Session.Compact result as the chat reply. Both
// front-ends (Telegram, web) send exactly this string, so the error mapping
// and wording live in one place next to the API they describe.
func CompactReplyText(before, after int, err error) string {
	switch {
	case errors.Is(err, chat.ErrNothingToCompact):
		return "nothing to compact"
	case errors.Is(err, ErrBusy):
		return "a turn is in flight — try /compact when it finishes"
	case err != nil:
		return "compact failed: " + err.Error()
	}
	return fmt.Sprintf("🗜 compacted: %s → %s tokens",
		strutil.FormatTokens(before), strutil.FormatTokens(after))
}

// Clear resets the conversation context (= /clear): drops all history and
// re-stamps the system prompt with a fresh timestamp. Returns ErrBusy while a
// turn is in flight (see Send's contract).
func (s *Session) Clear() error {
	return s.withIdle(s.clearLocked)
}

// clearLocked is Clear's body; caller (withIdle) holds s.mu.
func (s *Session) clearLocked() error {
	// Refuse while background work runs: a completing job would deliver its
	// notice into the NEW session — stale work leaking across the boundary the
	// user just drew. Same guard as Runtime.Reload (errIfRunning); /stop is the
	// explicit kill switch. Reading s.runtime directly is safe here (s.mu is
	// held; the helper is nil-safe for runtime-less test sessions).
	if s.runtime != nil {
		if err := s.runtime.jobs.errIfRunning("/clear"); err != nil {
			return err
		}
	}
	// Drop queued-but-undelivered notices from the conversation being cleared.
	s.sess.DropInbox()
	s.sess.SetMessages(nil)
	// Rotate onto a fresh store session: end the conversation just cleared (its
	// turns were already persisted per-turn, so it becomes a finished past
	// session) and open a new row that subsequent turns record under. Without
	// this, /clear only empties the in-memory buffer and the open session lingers
	// at the top of the dashboard's Runs list. Best-effort — a store hiccup logs
	// and leaves the live id untouched rather than dropping persistence.
	if s.cfg.Store != nil {
		if old := s.sess.ID(); old != "" {
			if err := s.cfg.Store.EndSession(old); err != nil {
				chat.LogOrNoop(s.cfg.Log).Warn("clear: end session failed", "session_id", old, "error", err)
			}
		}
		_, clearModel := chat.SplitStatus(s.cfg.StatusLine)
		if id, err := s.cfg.Store.NewSession(runs.Meta{
			Workdir:    s.cfg.WorkDir,
			ConfigPath: s.cfg.ConfigPath,
			Model:      clearModel,
		}); err == nil {
			s.sess.SetID(id)
		} else {
			chat.LogOrNoop(s.cfg.Log).Warn("clear: start session failed", "error", err)
		}
	}
	if s.cfg.RefreshPrompt != nil {
		// RefreshPrompt rebuilds the bare Lua system prompt; re-assemble the host
		// standing reminders for the new session id. s.mu is already held (see
		// withIdle), guarding the dashboard's concurrent Snapshot read.
		s.cfg.Personality.SystemPrompt = s.cfg.RefreshPrompt()
		s.applyHostReminders()
	}
	return nil
}

// Rollback drops the last turn from context (= /rollback). ok is false when
// there was nothing to remove. Returns ErrBusy while a turn is in flight (see
// Send's contract).
func (s *Session) Rollback() (ok bool, err error) {
	err = s.withIdle(func() error {
		msgs := s.sess.Messages()
		pruned := chat.PruneLastTurn(msgs)
		if len(pruned) == len(msgs) {
			return nil
		}
		s.sess.SetMessages(pruned)
		ok = true
		return nil
	})
	return ok, err
}

// SwitchAgent activates the configured agent named name for subsequent Sends
// (a front-end's agent-switch action). Switching swaps the agent's model client,
// system prompt, tool set, custom-tool routing, skills, status
// line, and context window while keeping conversation history. Returns an error
// for an unknown agent or when the config declares no agents, and ErrBusy
// while a turn is in flight: it mutates cfg in place, which the next Send's
// turnConfig reads (see Send's contract).
func (s *Session) SwitchAgent(name string) error {
	return s.withIdle(func() error {
		if s.cfg.SwitchAgent == nil {
			return errors.New("shell3: no agents configured")
		}
		rt, err := s.cfg.SwitchAgent(name)
		if err != nil {
			return err
		}
		// ApplyActiveAgent swaps in the new agent's prompt + toggles; re-assemble
		// the host standing reminders for the new active agent (whose Environment
		// toggle may differ).
		// s.mu is held throughout (withIdle) — guards the dashboard's Snapshot
		// read and a concurrent Close's nil of s.runtime.
		s.cfg.ApplyActiveAgent(rt)
		s.applyHostReminders()
		return nil
	})
}

// AgentNames returns the configured agent names in declaration order — the set
// SwitchAgent accepts. A caller can cycle (Tab-style) by finding ActiveAgent in
// this list and switching to the next entry. Empty or single-element means no
// switching is available.
func (s *Session) AgentNames() []string { return s.cfg.AgentNames }

// ActiveAgent returns the name of the currently active agent.
func (s *Session) ActiveAgent() string { return s.cfg.ModeLabel }

// Name returns the session's runtime key (e.g. "telegram", or a generated
// "sN"). Front-ends use it to label the session they attached to.
func (s *Session) Name() string { return s.name }
