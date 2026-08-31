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
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/runs"
)

// Session is a live, multi-turn conversation from [Runtime.Session]; the zero
// value is unusable. Each Send returns its own event channel — drain it to
// completion before any between-turns method (listed on [ErrBusy]).
//
// The underlying chat.Session uses a synchronous sink: events are translated
// inline on the turn goroutine, which closes the channel when it returns, so
// "turn finished" is exactly "the turn goroutine returned".
type Session struct {
	cfg      chat.Config
	sess     *chat.Session
	handlers map[string]chat.ToolHandler

	// runtime and name link back to the registry so Close deregisters. name is
	// internal bookkeeping (registry key, job parent), not a public id.
	runtime *Runtime
	name    string

	// opts is the SessionOpts this session was built from.
	opts SessionOpts

	// closeOnce makes Close concurrency-safe: a subagent goroutine may call
	// child.Close() while Runtime.Close closes the same child from its map.
	closeOnce sync.Once
	closeErr  error

	// mu guards the current turn's routing target and lifecycle handles.
	mu         sync.Mutex
	cur        chan Event         // current Send's channel; nil between turns
	curDone    <-chan struct{}    // current turn ctx's Done; unblocks a send to an abandoned cur on Close
	turnCancel context.CancelFunc // cancels the in-flight turn (nil before the first Send)
	turnDone   chan struct{}      // closed when the turn goroutine returns (nil before the first Send)
	sawError   bool               // any turn emitted an error event; drives the audit "end" status
	// busy spans Send until its turn goroutine finishes, turning an
	// overlapping Send/Clear/Compact into ErrBusy instead of a data race.
	busy bool
	// closed is set by doClose so a late Send — a Wake-driven drain racing
	// teardown — is rejected rather than run against the ended store record.
	closed bool
}

// newSession wires a Session around a built chat.Config. Split out from Start
// so tests can inject a fakellm-backed config.
func newSession(cfg chat.Config, opts SessionOpts) *Session {
	var storeID string
	var seed []llm.Message
	var seedTokens int     // persisted provider-reported prompt tokens for a resumed session
	var resumedFrom string // non-empty when this session reattached to an existing run
	if cfg.Store != nil {
		// A front-end resolves the id from its OWN thread marker: "newest
		// session matching this workdir" is not a conversation identity, and
		// with two front-ends live it follows whichever spoke last.
		resumeID := opts.ResumeID
		switch {
		case resumeID != "":
			storeID = resumeID
			resumedFrom = resumeID
			if msgs, err := cfg.Store.LoadMessages(resumeID); err == nil {
				seed = msgs
			} else {
				chat.LogOrNoop(cfg.Log).Warn("resume load failed", "session_id", resumeID, "error", err)
			}
			// Restore the persisted gauge so the first resumed turn's
			// prune/compaction fires; 0 falls back to the estimate.
			seedTokens = cfg.Store.LastPromptTokens(resumeID)
		default:
			// Best-effort: a failed NewSession leaves storeID "" and no
			// persistence, logged so it is observable rather than silent.
			_, metaModel := chat.SplitStatus(cfg.StatusLine)
			if id, err := cfg.Store.NewSession(runs.Meta{
				Workdir:   cfg.WorkDir,
				ConfigDir: cfg.ConfigDir,
				Model:     metaModel,
				ParentID:  opts.ParentID,
				Agent:     opts.Agent,
				CronJob:   opts.CronJob,
			}); err == nil {
				storeID = id
			} else {
				chat.LogOrNoop(cfg.Log).Warn("start session failed", "error", err)
			}
		}
	}
	// Carry the dispatch identity into cfg so a compaction rollover, which
	// runs mid-turn from cfg alone, stamps the rolled session with the same
	// attribution instead of losing it at the boundary.
	cfg.Agent = opts.Agent
	cfg.ParentID = opts.ParentID
	cfg.CronJob = opts.CronJob
	s := &Session{
		cfg:      cfg,
		handlers: chat.NewHandlers(),
	}
	s.sess = chat.NewSession(chat.SessionOpts{
		StoreID:             storeID,
		InitialMessages:     seed,
		InitialPromptTokens: seedTokens,
		ContextWindowFor:    func(string) int { return cfg.ContextWindow },
		Sink:                s.route,
		OnEvent:             cfg.OnEvent,
		Store:               cfg.Store,
	})
	if resumedFrom != "" {
		if err := s.sess.RestoreReminders(); err != nil {
			chat.LogOrNoop(cfg.Log).Warn("restore reminders failed", "session_id", resumedFrom, "error", err)
		}
	}
	return s
}

// route is the chat.Session sink, running on the turn goroutine, so every
// forward to a Send channel happens-before that goroutine closes it. The
// curDone select unblocks a send to a channel the caller stopped reading.
// Events with no public equivalent are dropped.
//
// NOTE: curDone is the turn ctx's Done, closed by an ordinary cancel as well
// as by Close, so a cancelled turn MAY drop whatever this was delivering —
// the terminal Done/Error included. Channel close is the authoritative
// end-of-turn signal; the terminal event is best-effort.
func (s *Session) route(ev chat.Event) {
	if ev.Kind == chat.EventError {
		s.mu.Lock()
		s.sawError = true
		s.mu.Unlock()
	}
	pub, ok := translate(ev)
	if !ok {
		return
	}
	// translate has no config, so IsHostTool resolves here.
	if pub.Kind == ToolCall && s.cfg.HostToolNames[pub.ToolName] {
		pub.IsHostTool = true
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

// Interject delivers text outside the Send contract: mid-turn it is injected
// at the next round boundary as a system reminder, so the model can
// course-correct; while idle it queues for the next turn. It never fails,
// never blocks, and is safe from any goroutine — the front-end message path,
// where Send is the strict turn-starting call. Text only: media arriving
// mid-turn waits for the next turn rather than injecting mid-flight.
func (s *Session) Interject(text string) {
	s.sess.Interject(text)
	// Idle steering must prod the host; a running turn drains the inbox
	// itself. The TOCTOU is benign: worst case a missed wake (the next Send
	// drains it anyway) or a spurious one (RunQueued no-ops when drained).
	if !s.isBusy() {
		s.wake()
	}
}

// wake emits a Wake on the runtime bus, no-op without a runtime. Reachable
// from any goroutine, so it snapshots s.runtime under s.mu to avoid racing
// doClose's nil of it; the lock is not held across emit.
func (s *Session) wake() {
	if rt := s.runtimeHandle(); rt != nil {
		rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
	}
}

// closedEvents is the shape every rejected or no-op turn request returns: a
// closed channel, carrying one Error event when err is non-nil.
func closedEvents(err error) <-chan Event {
	ch := make(chan Event, 1)
	if err != nil {
		ch <- Event{Kind: Error, Err: err}
	}
	close(ch)
	return ch
}

// RunQueued answers a Wake by running one turn over the queued inbox. An
// empty inbox, or a turn already in flight (which drains it itself), starts
// no turn and returns a closed channel. Otherwise Send's ErrBusy contract.
func (s *Session) RunQueued(ctx context.Context) <-chan Event {
	if s.isBusy() || !s.sess.HasInbox() {
		return closedEvents(nil)
	}
	// The turn loop drains the inbox at its top, so an empty-prompt turn
	// consumes the queued items as its initiating input.
	return s.Send(ctx, "")
}

// HasQueuedInput reports interjected items waiting — steering that arrived
// during a turn's final round. RunQueued consumes them.
func (s *Session) HasQueuedInput() bool { return s.sess.HasInbox() }

// HasQueuedSteer reports whether queued USER steering (not host notices) is
// waiting — see chat.Session.HasSteer.
func (s *Session) HasQueuedSteer() bool { return s.sess.HasSteer() }

// Headless reports no human attached (subagent children, cron jobs).
// Registrars skip tools that need one: send_media_telegram has nowhere to
// send a file without a live chat session.
func (s *Session) Headless() bool { return s.opts.Headless }

// Send runs one turn and returns its event channel, closed when the turn
// ends. Close is the authoritative end-of-turn signal — the terminal
// Done/Error is best-effort and may be dropped on cancel (see route) — so
// bind UI and state transitions to close, not to receiving it.
//
// One turn at a time: drain the channel before another Send or any
// between-turns method (listed on [ErrBusy]), which read and mutate
// unsynchronized session state. The contract is enforced, not assumed: a Send
// mid-turn returns ErrBusy, one after Close returns [ErrClosed].
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event {
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
	// Capture under the busy gate so the turn goroutine never reads s.runtime:
	// doClose nils it once `done` closes.
	rt := s.runtime
	// Snapshot the turn config still holding s.mu, so "busy set" and "cfg
	// read" are atomic against the cfg mutators (Clear, RegisterHostTool):
	// one that slipped past its isBusy check lands wholly before or after.
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	go func() {
		// No forwarding can happen once the turn returns, so clearing cur and
		// busy and closing out here is race-free.
		defer func() {
			s.mu.Lock()
			if s.cur == out {
				s.cur = nil
			}
			s.busy = false
			s.mu.Unlock()
			close(out)
			// Steering or a subagent result that arrived in the final round
			// queued with no boundary left to drain it. The session is idle
			// with a non-empty inbox, so Wake the host for a follow-up turn —
			// after busy clears, or RunQueued would bounce off ErrBusy.
			if rt != nil && s.sess.HasInbox() {
				rt.emit(HostEvent{Session: s.sess.ID(), Kind: Wake})
			}
			cancel() // release the child ctx
		}()
		defer close(done)
		s.sess.Run(turnCtx, tc, prompt)
	}()
	return out
}

// runtimeHandle snapshots s.runtime under s.mu. Every accessor reachable from
// outside the turn goroutine must use it: doClose nils the field concurrently.
func (s *Session) runtimeHandle() *Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtime
}

// sessionStore returns the Parts-generation store bound to s. Subagent
// sessions deliberately keep that generation across reloads, so child jobs
// must not reach through Runtime.store for a newer handle.
func sessionStore(s *Session) *runs.Store {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.Store
}

// isBusy reports whether a turn is in flight (see Send's contract).
func (s *Session) isBusy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.busy
}

// ID is the store session id; it rolls when compaction starts a new one, and
// is "" with no store.
func (s *Session) ID() string {
	return s.sess.ID()
}

// Close cancels any in-flight turn and waits for it, so its deferred history
// persist runs against the still-open store, then ends the store session and
// releases the config. A sequential second Close is a safe no-op; concurrent
// Closes are not supported.
//
// A Start-owned session also tears down the private Runtime Start created
// (client, store, proxy spawner); a Runtime-hosted one only deregisters, and
// the shared parts stay alive for the other sessions.
//
// An abandoned Send channel is fine: cancelling the turn ctx unblocks route's
// send to it, so the join below cannot wedge. Draining is still the supported
// pattern. Only EndSession's error is returned; the best-effort teardown
// steps do not contribute to it.
func (s *Session) Close() error {
	s.closeOnce.Do(func() { s.closeErr = s.doClose() })
	return s.closeErr
}

// doClose runs the teardown exactly once (guarded by closeOnce in Close).
func (s *Session) doClose() error {
	// Cancel, then join before EndSession, so a cancelled turn is not still
	// writing to the store as it runs. closed is set first, so a Send racing
	// teardown is rejected rather than starting a turn on the ended record.
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
	// Nil s.runtime under s.mu so a concurrent WakeEvents() reader never races
	// the write. Only the field access is locked — holding s.mu across
	// rt.forget could deadlock against the runtime's own locking.
	s.mu.Lock()
	rt := s.runtime
	s.runtime = nil
	s.mu.Unlock()
	if rt != nil {
		rt.forget(s.name)
	}
	return endErr
}

// RecoveryHint suggests a remedy for a provider HTTP 400, which usually means
// the last turn left the conversation in a state the model rejects. "" for
// every other error (401, 429, network, 5xx), where rewriting history would
// not help. Front-ends append it to the error they show.
func RecoveryHint(err error) string {
	if err == nil {
		return ""
	}
	const hint = "This usually means the last turn left the conversation in a state the model rejects. Starting a new conversation (for Telegram, /new) will normally clear it."
	var se *llm.StatusError
	if errors.As(err, &se) {
		if se.Code == 400 {
			return hint
		}
		return ""
	}
	// Fallback for errors that lost the typed shell (a proxy stringified it).
	s := err.Error()
	if strings.Contains(s, "400 Bad Request") || strings.Contains(s, `"http_code":"400"`) {
		return hint
	}
	return ""
}

// turnConfigLocked derives the per-turn config, built fresh each turn so a
// between-turns mutation takes effect on the next Send. Caller must hold s.mu
// — the fields it reads are mutated by the mu-holding between-turns methods.
func (s *Session) turnConfigLocked() chat.TurnConfig {
	cfg := s.cfg
	tc := chat.NewTurnConfig(cfg, s.handlers)
	if s.runtime != nil && s.runtime.jobs != nil {
		rt := s.runtime
		parent := s
		tc.StartBashBg = func(command, workdir string, argv, env []string, report notify.ReportMode, note string) (string, error) {
			return rt.jobs.startCommand(parent, command, workdir, argv, env, report, note)
		}
		allowed := cfg.Subagents // the active agent's registered-subagent allowlist
		tc.StartSubagent = func(agent, prompt, desc string, report notify.ReportMode, note string) (string, error) {
			// Only the registered subagent names the task tool's schema
			// advertises; an empty allowlist means no delegation at all.
			if !slices.Contains(allowed, agent) {
				if len(allowed) == 0 {
					return "", errors.New("this agent has no subagents configured (the kit declares no employee)")
				}
				return "", fmt.Errorf("subagent_type %q is not allowed for this agent; allowed subagents: %s", agent, strings.Join(allowed, ", "))
			}
			// Dispatch depth is enforced centrally by the job manager; this
			// session-local closure enforces the active agent's peer allowlist.
			return rt.jobs.startSubagent(parent, agent, prompt, desc, subagentOpts{report: report, note: note})
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

// ActiveAgent returns the name of the currently active agent.
func (s *Session) ActiveAgent() string { return s.cfg.ModeLabel }

// Name is the session's runtime key, which front-ends use as a label.
func (s *Session) Name() string { return s.name }
