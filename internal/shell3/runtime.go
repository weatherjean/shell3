package shell3

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/runs"
)

// RuntimeSpec configures a long-lived Runtime: the process-wide unit owning
// the loaded config, store, proxy spawner, and log.
type RuntimeSpec struct {
	ConfigDir string // "" → ~/.shell3/
	WorkDir   string // runtime root; "" → os.Getwd(). Sessions default here.
}

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
	// ParentID marks this a subagent child of that runs session: the dash
	// groups the transcript under the conversation that spawned it, and the
	// janitor never deletes a message-less session other rows name as parent.
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
	// ctx is the runtime's base context under NewRuntime's. A watcher calls
	// Close when the parent fires, and Close cancels ctx, so the watcher and
	// everything scoped to the runtime unwind together.
	ctx    context.Context
	cancel context.CancelFunc

	configDir string // captured from RuntimeSpec for ConfigDir
	homeDir   string // captured from construction for ConfigDir

	// jobs owns the in-process background jobs, cancelled at Close.
	jobs *jobManager
	// telegram and cron mirror the parsed config blocks, re-derived on Reload.
	telegram TelegramConfig
	cron     []CronJob

	// parts is the config assembly this Runtime was built from, swapped at
	// Reload, for host code needing resources Runtime does not expose.
	parts *agentsetup.Parts

	// parkedClosers holds teardowns deferred past a Reload that happened with
	// background work live: a running job may still hold the old generation's
	// store and MCP handles. Each drains exactly once, when the job manager
	// reports zero running tasks. Guarded by mu.
	parkedClosers []func()

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool

	// completionH is the front-end completion surface; nil = the library
	// fallback, a raw notice to the owning session.
	completionH CompletionHost

	// cronOutcome is where a finished cron run's real result goes (the
	// scheduler); nil = nobody is keeping cron history. See CronOutcome.
	cronOutcome func(CronOutcome)

	// decorate runs for every session this runtime creates, and again for
	// every live one after a Reload, which rebuilds configs and drops
	// registered host tools. Front-ends register their tools here rather than
	// on their own main session alone. Always invoked OUTSIDE rt.mu:
	// decorators call back into locked Runtime methods.
	decorate func(*Session)
}

// NewRuntime loads the config and assembles the shared parts. Cancelling ctx
// tears the runtime down exactly as Close does; pass context.Background() for
// a lifetime bounded only by Close. The Runtime must be Closed.
func NewRuntime(ctx context.Context, spec RuntimeSpec) (*Runtime, error) {
	parent := ctx
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return nil, err // caller already cancelled — don't build a runtime
	}
	workDir := spec.WorkDir
	if workDir == "" {
		w, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("get working directory: %w", err)
		}
		workDir = w
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home directory: %w", err)
	}
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: spec.ConfigDir, CWD: workDir, HomeDir: homeDir,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	rt := &Runtime{
		sessionConfig: sessionConfigFrom(parts),
		cleanup:       cleanup,
		store:         parts.Store(),
		events:        make(chan HostEvent, 64),
		jobEvents:     make(chan JobProgress, 256),
		workDir:       workDir,
		configDir:     spec.ConfigDir,
		homeDir:       homeDir,
		ctx:           ctx,
		cancel:        cancel,
		sessions:      map[string]*Session{},
		telegram:      parts.Telegram(),
		cron:          parts.Cron(),
		parts:         parts,
	}
	rt.jobs = newJobManager(rt, parts.BackgroundMaxConcurrent())
	// Close cancels rt.ctx, so this watcher always unwinds and its second
	// Close is an idempotent no-op.
	go func() {
		<-rt.ctx.Done()
		_ = rt.Close()
	}()
	return rt, nil
}

// Events is the out-of-turn bus; one receiver drives N sessions. Buffered,
// dropping on full — the host re-checks inboxes on its next turn anyway.
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

// JobEvents is the job-progress bus: a Chunk per write to a job's output tee,
// a Done at completion. Buffered, dropping on full so a slow consumer never
// stalls a job, and never closed.
func (rt *Runtime) JobEvents() <-chan JobProgress { return rt.jobEvents }

// ConfigDir returns the absolute path of the config directory this runtime was built
// from. An empty or relative spec path is resolved exactly the way construction
// resolves it — ~/.shell3/. Useful for self-reconfiguration
// surfaces that need to show the agent/operator which file to edit.
func (rt *Runtime) ConfigDir() (string, error) {
	return agentsetup.ResolveConfigDir(rt.configDir, rt.homeDir)
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
// every one already live (so boot order does not matter), and every live one
// after a Reload, which rebuilds configs and drops registered host tools. fn
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

// drainParkedClosers runs the teardowns a Reload deferred, once, provided no
// background work could still be using an old generation. A no-op when
// nothing is parked or a job is live — the next completion re-checks. The
// running-jobs check runs before rt.mu, so the two locks never nest.
func (rt *Runtime) drainParkedClosers() {
	if rt.jobs != nil && len(rt.jobs.runningJobIDs()) > 0 {
		return
	}
	rt.mu.Lock()
	closers := rt.parkedClosers
	rt.parkedClosers = nil
	rt.mu.Unlock()
	for _, c := range closers {
		c()
	}
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
	// Jobs have unwound, so parked closers run now, before this generation's
	// cleanup. Directly, not via drainParkedClosers: teardown is unconditional.
	rt.mu.Lock()
	parked := rt.parkedClosers
	rt.parkedClosers = nil
	rt.mu.Unlock()
	for _, c := range parked {
		c()
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
