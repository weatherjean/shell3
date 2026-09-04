package shell3

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

// SessionOpts parameterizes one Session on a Runtime.
type SessionOpts struct {
	// Name keys the session on the runtime; "" generates one. An existing
	// live name returns that session.
	Name string
	// Agent selects the initial agent ("" → first declared).
	Agent string
	// WorkDir roots tool execution for this session ("" → runtime root).
	WorkDir string
	// Headless injects the headless reminder (no human to answer questions).
	Headless bool
	// ParentID groups a child transcript under the session that spawned it.
	ParentID string
	// CronJob names the cron job that started this session, recorded in its
	// meta so a job's runs are findable without guessing from duration.
	CronJob string
	// PromptSuffix appends per-session text to the system prompt every turn;
	// the Telegram front-end gives each chat its own brief with it.
	PromptSuffix func() string
	// ResumeID reloads a stored session's messages. A front-end records the
	// id under its OWN surface and passes it back — there is deliberately no
	// "resume the newest session", which with two front-ends live reattaches
	// to whichever spoke last.
	ResumeID string
}

// HostEventKind discriminates out-of-turn runtime events.
type HostEventKind int

const (
	// Wake: an idle session's inbox gained an item. The host answers with
	// Session.RunQueued, which runs a model turn.
	Wake HostEventKind = iota
)

// String returns the event name ("wake") for logs and diagnostics.
func (k HostEventKind) String() string {
	if k == Wake {
		return "wake"
	}
	return fmt.Sprintf("HostEventKind(%d)", int(k))
}

// HostEvent is one out-of-turn event, carrying the session's store id so a
// host can match it against what it is watching.
type HostEvent struct {
	Session string
	Kind    HostEventKind
}

// Runtime hosts N sessions over one shared build, safe for concurrent Session
// calls. Close is idempotent and closes any session still open; a session
// deregisters itself on its own Close.
type Runtime struct {
	// sessionConfig derives a per-session chat.Config; tests inject fakes.
	sessionConfig func(SessionOpts) (chat.Config, error)
	cleanup       func()
	log           applog.Logger

	// events is the out-of-turn event bus (Wake). Buffered; emit drops on full.
	events chan HostEvent
	// jobEvents is the job-progress bus, buffered and dropping on full so a
	// slow consumer never stalls a job. Never closed — a late emit from an
	// unwinding job goroutine must not panic.
	jobEvents chan JobProgress
	// workDir is the runtime root (.shell3_project lives under it).
	workDir string
	// store is the shared runs store, nil if unavailable: front-end session
	// lists and replay, plus the job runtime's transcript reads.
	store *runs.Store
	// ctx is the runtime's base context. A watcher calls
	// Close when the parent fires, and Close cancels ctx, so the watcher and
	// everything scoped to the runtime unwind together.
	ctx    context.Context
	cancel context.CancelFunc

	// jobs owns the in-process background jobs, cancelled at Close.
	jobs *jobManager

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool

	// decorate runs for every session this runtime creates and every session
	// already live when it is installed. Front-ends register their tools here
	// rather than on their own main session alone. Always invoked OUTSIDE rt.mu:
	// decorators call back into locked Runtime methods.
	decorate func(*Session)
}

// NewConfiguredRuntime builds the process/runtime half around an already
// assembled chat configuration. Background jobs and session lifecycle remain
// native. cleanup may be nil. The caller must Close the returned runtime.
func NewConfiguredRuntime(ctx context.Context, workDir string, store *runs.Store, maxConcurrent int, cleanup func(), sessionConfig func(SessionOpts) (chat.Config, error)) (*Runtime, error) {
	if sessionConfig == nil {
		return nil, errors.New("shell3: configured runtime needs a session config")
	}
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, err
	}
	if workDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		workDir = wd
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	runtimeCtx, cancel := context.WithCancel(parent)
	rt := &Runtime{
		sessionConfig: sessionConfig,
		cleanup:       cleanup,
		log:           applog.Noop{},
		store:         store,
		events:        make(chan HostEvent, 64),
		jobEvents:     make(chan JobProgress, 256),
		workDir:       workDir,
		ctx:           runtimeCtx,
		cancel:        cancel,
		sessions:      map[string]*Session{},
	}
	rt.jobs = newJobManager(rt, maxConcurrent)
	go func() {
		<-rt.ctx.Done()
		_ = rt.Close()
	}()
	return rt, nil
}

// SetLogger installs the process diagnostic logger before front ends and
// sessions begin work. Nil restores the silent test default.
func (rt *Runtime) SetLogger(log applog.Logger) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if log == nil {
		log = applog.Noop{}
	}
	rt.log = log
}

// Logger returns the runtime's shared diagnostic logger.
func (rt *Runtime) Logger() applog.Logger {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.log == nil {
		return applog.Noop{}
	}
	return rt.log
}

// Events is the out-of-turn bus; one receiver drives N sessions. Buffered,
// dropping on full — the host re-checks inboxes on its next turn anyway.
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

// JobEvents is the job-progress bus: a Chunk per write to a job's output tee,
// a Done at completion. Buffered, dropping on full so a slow consumer never
// stalls a job, and never closed.
func (rt *Runtime) JobEvents() <-chan JobProgress { return rt.jobEvents }

// Store returns the runtime's durable conversation store.
func (rt *Runtime) Store() *runs.Store {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.store
}

// ReloadConfig atomically replaces the session factory and refreshes every
// idle live session. A busy session retains its captured turn generation and
// adopts the replacement before its next turn. All configs are built before
// any are published, so a failed reload leaves the current generation intact.
func (rt *Runtime) ReloadConfig(factory func(SessionOpts) (chat.Config, error)) error {
	if factory == nil {
		return errors.New("shell3: reload needs a session config")
	}
	for {
		rt.mu.Lock()
		if rt.closed {
			rt.mu.Unlock()
			return ErrRuntimeClosed
		}
		live := make(map[string]*Session, len(rt.sessions))
		for name, session := range rt.sessions {
			live[name] = session
		}
		rt.mu.Unlock()

		configs := make(map[string]chat.Config, len(live))
		for name, session := range live {
			cfg, err := factory(session.opts)
			if err != nil {
				return err
			}
			configs[name] = cfg
		}

		rt.mu.Lock()
		unchanged := len(rt.sessions) == len(live)
		if unchanged {
			for name, session := range live {
				if rt.sessions[name] != session {
					unchanged = false
					break
				}
			}
		}
		if !unchanged {
			rt.mu.Unlock()
			continue
		}
		locked := make([]*Session, 0, len(live))
		for _, session := range live {
			session.mu.Lock()
			locked = append(locked, session)
		}
		var stateErr error
		for _, session := range locked {
			if session.closed {
				stateErr = ErrClosed
				break
			}
		}
		if stateErr == nil {
			for name, session := range live {
				session.queueReloadLocked(configs[name])
			}
			rt.sessionConfig = factory
		}
		for i := len(locked) - 1; i >= 0; i-- {
			locked[i].mu.Unlock()
		}
		rt.mu.Unlock()
		return stateErr
	}
}

func (rt *Runtime) emit(ev HostEvent) {
	select {
	case rt.events <- ev:
	default: // bus full: drop (Wake is a hint, not a queue)
	}
}

// emitJob sends a JobProgress event on the job bus. Non-blocking: if the
// buffer is full the event is dropped so a slow consumer never stalls a job.
func (rt *Runtime) emitJob(ev JobProgress) {
	select {
	case rt.jobEvents <- ev:
	default: // bus full: drop
	}
}

// SetSessionDecorator installs fn for every session this runtime creates,
// every one already live (so boot order does not matter). fn
// runs outside rt.mu, so it may call locked Runtime methods, and must be
// installed only while sessions are idle. A new decorator replaces the old.
func (rt *Runtime) SetSessionDecorator(fn func(*Session)) {
	rt.mu.Lock()
	rt.decorate = fn
	live := make([]*Session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		live = append(live, s)
	}
	rt.mu.Unlock()
	if fn == nil {
		return
	}
	for _, s := range live {
		fn(s)
	}
}

// Session creates a session on this runtime, root or subagent child. A closed
// runtime returns an error.
func (rt *Runtime) Session(opts SessionOpts) (*Session, error) {
	// Registered before rt.mu.Lock so it runs AFTER the deferred unlock: the
	// decorator calls locked Runtime methods, and must fire only for a session
	// this call created, never the early return of an existing live name.
	var created *Session
	defer func() {
		if created == nil {
			return
		}
		if dec := rt.decorateFn(); dec != nil {
			dec(created)
		}
	}()
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil, ErrRuntimeClosed
	}
	// An existing live name returns that same session, so its history
	// persists across reattach. An empty name gets a generated "sN", skipping
	// any a live session already took.
	if opts.Name == "" {
		for {
			rt.nextName++
			opts.Name = fmt.Sprintf("s%d", rt.nextName) // internal bookkeeping label only
			if _, taken := rt.sessions[opts.Name]; !taken {
				break
			}
		}
	}
	if s, ok := rt.sessions[opts.Name]; ok {
		return s, nil
	}
	name := opts.Name
	cfg, err := rt.sessionConfig(opts)
	if err != nil {
		return nil, err
	}
	s := newSession(cfg, opts) // shared parts are the runtime's to clean
	s.opts = opts
	s.runtime, s.name = rt, name
	// Standing reminders now that runtime and name are set.
	s.applyHostReminders()
	rt.sessions[name] = s
	created = s // decorated by the deferred hook, after rt.mu releases
	return s, nil
}

// decorateFn returns the current session decorator under rt.mu.
func (rt *Runtime) decorateFn() func(*Session) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.decorate
}

// forget removes a closed session from the registry. Called by Session.Close.
func (rt *Runtime) forget(name string) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	delete(rt.sessions, name)
}

// Close closes every live session, then the shared parts. Idempotent.
func (rt *Runtime) Close() error {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return nil
	}
	rt.closed = true
	open := make([]*Session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		open = append(open, s)
	}
	rt.sessions = map[string]*Session{}
	rt.mu.Unlock()
	rt.Logger().Info("runtime closing", "sessions", len(open))

	var firstErr error
	for _, s := range open {
		if err := s.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	// Join the job goroutines BEFORE the store closes, so none can write to it.
	if rt.jobs != nil {
		rt.jobs.cancelAll()
		rt.jobs.wait()
	}
	rt.cleanup()
	// Cancel the runtime base ctx so anything scoped to the runtime's lifetime
	// unwinds. Do NOT close rt.events: a late emit from an unwinding goroutine
	// must not panic.
	if rt.cancel != nil {
		rt.cancel()
	}
	return firstErr
}
