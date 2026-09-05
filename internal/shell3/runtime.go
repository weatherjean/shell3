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
	// Headless injects the headless reminder (no human to answer questions).
	Headless bool
	// PromptSuffix appends per-session text to the system prompt every turn;
	// the Telegram front-end gives each chat its own brief with it.
	PromptSuffix func() string
	// ResumeID reloads a stored session's messages. A front-end records the
	// id under its OWN surface and passes it back — there is deliberately no
	// "resume the newest session", which with two front-ends live reattaches
	// to whichever spoke last.
	ResumeID string
}

// Runtime hosts N sessions over one shared build, safe for concurrent Session
// calls. Close is idempotent and closes any session still open; a session
// deregisters itself on its own Close.
type Runtime struct {
	// sessionConfig derives a per-session chat.Config; tests inject fakes.
	sessionConfig func(SessionOpts) (chat.Config, error)
	cleanup       func()
	log           applog.Logger

	// jobCompletions wakes the one-shot CLI after a background command exits.
	// It carries no output: durable results live in the filesystem inbox.
	jobCompletions chan struct{}
	// workDir is the runtime root (.shell3_project lives under it).
	workDir string
	// store is the shared conversation store, nil if persistence is unavailable.
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
	completionBuffer := maxConcurrent
	if completionBuffer <= 0 {
		completionBuffer = defaultMaxConcurrent
	}
	runtimeCtx, cancel := context.WithCancel(parent)
	rt := &Runtime{
		sessionConfig:  sessionConfig,
		cleanup:        cleanup,
		log:            applog.Noop{},
		store:          store,
		jobCompletions: make(chan struct{}, completionBuffer),
		workDir:        workDir,
		ctx:            runtimeCtx,
		cancel:         cancel,
		sessions:       map[string]*Session{},
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

// JobCompletions reports background-command exits. The channel is never
// closed; consume it only while the Runtime is live.
func (rt *Runtime) JobCompletions() <-chan struct{} { return rt.jobCompletions }

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

// emitJobCompletion wakes a waiter without making command teardown block.
func (rt *Runtime) emitJobCompletion() {
	select {
	case rt.jobCompletions <- struct{}{}:
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

// Session creates a session on this runtime. A closed
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
	// unwinds.
	if rt.cancel != nil {
		rt.cancel()
	}
	return firstErr
}
