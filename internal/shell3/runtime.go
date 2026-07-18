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
	// Name keys the session on the runtime (e.g. "telegram"). "" gets a unique
	// generated name. Requesting an existing live name returns that session.
	Name string
	// Agent selects the initial agent ("" → first declared).
	Agent string
	// WorkDir roots tool execution for this session ("" → runtime root).
	WorkDir string
	// Headless injects the headless reminder (no human to answer questions).
	Headless bool
	// OutPath, when non-empty, streams this session's JSONL audit log there.
	OutPath string
	// Asker confirms an on_tool_call ask-verdict command with a human (true = allow).
	Asker func(ctx context.Context, command, reason string) bool
	// ResumeID reloads a stored session's messages when non-empty.
	ResumeID string
	// ResumeLatest reattaches to the newest stored session matching this
	// session's workdir+config (instead of starting fresh) when ResumeID is empty.
	// Falls back to a new session when none exists. A front-end sets this to
	// rejoin the live conversation rather than spawning empty sessions on restart.
	ResumeLatest bool
}

// HostEventKind discriminates out-of-turn runtime events.
type HostEventKind int

const (
	// Wake signals a session's inbox gained an item while no turn was running.
	// The host should call Session.RunQueued to react (runs a model turn).
	Wake HostEventKind = iota
)

// String returns the event name ("wake") for logs and diagnostics.
func (k HostEventKind) String() string {
	if k == Wake {
		return "wake"
	}
	return fmt.Sprintf("HostEventKind(%d)", int(k))
}

// HostEvent is one out-of-turn event for a session. Wake carries the
// session's store id (Session.ID()) so a host can match it against the
// session it is watching.
type HostEvent struct {
	Session string
	Kind    HostEventKind
}

// Runtime hosts N sessions over one shared build. Create with NewRuntime,
// release with Close. Safe for concurrent Session calls.
//
// Lifetime: NewRuntime → Session (×N) → Close. Close is idempotent; any
// sessions still open at Close time are closed first. Sessions deregister
// from the runtime automatically on their own Close.
type Runtime struct {
	// sessionConfig derives a per-session chat.Config; production wires
	// agentsetup.Parts.SessionConfig, tests inject fakes.
	sessionConfig func(SessionOpts) (chat.Config, error)
	cleanup       func()

	// events is the out-of-turn event bus (Wake). Buffered; emit drops on full.
	events chan HostEvent
	// jobEvents is the background-job progress bus. Buffered at 256; emitJob
	// drops on full so a slow consumer never stalls a running job. Not closed
	// at Close (a late emit from an unwinding job goroutine must not panic).
	jobEvents chan JobProgress
	// workDir is the runtime root (.shell3_project lives under it).
	workDir string
	// store is the shared file-native runs store (nil if unavailable). Used by
	// PastSessions/SessionMessages for front-end session lists/replay and by
	// the job runtime's transcript reads (task_status / JobTranscript).
	store *runs.Store
	// ctx is the runtime's base context, parented by the ctx given to
	// NewRuntime. A watcher goroutine calls Close when it fires, so cancelling
	// the parent tears the runtime down; cancel fires at Close so the watcher
	// (and anything else scoped to the runtime's lifetime) unwinds with it.
	ctx    context.Context
	cancel context.CancelFunc

	configDir string // captured from RuntimeSpec for ConfigDir
	homeDir   string // captured from construction for ConfigDir

	// jobs manages in-process background jobs (command and subagent jobs).
	// Owned by this Runtime; cancelled at Close.
	jobs *jobManager
	// telegram + cron + heartbeat mirror the parsed config blocks the runtime
	// was built with (and re-derived on Reload). Read via
	// Telegram()/Cron()/HeartbeatConfig(). See telegram.go.
	telegram  TelegramConfig
	web       WebConfig
	cron      []CronJob
	heartbeat *Heartbeat

	// parts is the shared config assembly this Runtime was (re)built from.
	// Swapped alongside the other fields at Reload; read via Parts() by host
	// code that needs config-derived resources Runtime doesn't otherwise
	// expose (e.g. rebuilding media.Clients from LoadedConfig + EnsureProxy).
	parts *agentsetup.Parts

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool

	// decorate, when set (SetSessionDecorator), runs for every session this
	// runtime creates — main and subagent children alike — and again for every
	// live session after a Reload (which rebuilds session configs, dropping
	// previously registered host tools). Front-ends use it to register host
	// tools (e.g. image_generate) uniformly instead of decorating only their
	// own main session. Always invoked OUTSIDE rt.mu: decorators call back
	// into locked Runtime methods (rt.Parts()).
	decorate func(*Session)
}

// NewRuntime loads the config and assembles the shared runtime parts. ctx
// parents the runtime's base context: cancelling it tears down the runtime
// (and any in-flight session/turn) just as Close does; pass
// context.Background() for a lifetime bounded only by Close.
// The Runtime must be Closed; sessions left open are closed by Close.
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
		web:           parts.Web(),
		cron:          parts.Cron(),
		heartbeat:     parts.Heartbeat(),
		parts:         parts,
	}
	rt.jobs = newJobManager(rt, parts.BackgroundMaxConcurrent())
	// Implement the documented cancellation contract: cancelling the parent ctx
	// tears the runtime down just as Close does. Close itself cancels rt.ctx,
	// so this watcher always unwinds — the second Close is an idempotent no-op.
	go func() {
		<-rt.ctx.Done()
		_ = rt.Close()
	}()
	return rt, nil
}

// Events returns the out-of-turn event bus. One receiver drives N sessions.
// Buffered; if the host is not draining, Wake events coalesce (drop on full —
// the host re-checks inboxes on its next turn anyway).
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

// JobEvents returns the background-job progress bus. Each write to a job's
// output tee emits a Chunk event; job completion emits a Done event. The
// channel is buffered at 256 and drops on full — a slow consumer never stalls
// a running job. The channel is never closed.
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

// SetSessionDecorator installs fn to run for every session this runtime
// creates from now on (main and subagent children alike), for every session
// already live (so boot order doesn't matter), and for every live session
// after a Reload (which rebuilds session configs, dropping previously
// registered host tools). Front-ends use it to register host tools —
// image_generate in particular — uniformly across all sessions. fn runs
// outside rt.mu, so it may call locked Runtime methods (rt.Parts()); it must
// only be installed while sessions are idle (same contract as
// RegisterHostTool). Installing a new decorator replaces the previous one.
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

// Session creates and returns a new session on this runtime (a front-end's root
// session, or a subagent's child session). A closed runtime returns an error.
func (rt *Runtime) Session(opts SessionOpts) (*Session, error) {
	// Registered before rt.mu.Lock so it runs AFTER the deferred unlock (LIFO):
	// the decorator must run outside rt.mu (it calls locked Runtime methods),
	// and only for a session this call actually created (not the early return
	// of an existing live name, which was decorated when it was created).
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
	// A named session is keyed on the runtime: requesting an existing live name
	// (e.g. the telegram host's "telegram") returns that same session so its
	// history persists across reattach. An empty name gets a unique generated
	// label ("sN"), skipping any already taken by a live session.
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
	// Open the sink before constructing anything stateful: a failure here must
	// not leak a partially-initialised session or a store row.
	sink, sinkCleanup, err := chat.OpenSink(opts.OutPath, cfg.Log)
	if err != nil {
		return nil, err
	}
	s := newSession(cfg, opts) // shared parts are the runtime's to clean
	s.asker = opts.Asker
	s.opts = opts
	s.runtime, s.name = rt, name
	s.sink, s.sinkCleanup = sink, sinkCleanup
	// Set the per-session host standing reminders now that runtime+name are set:
	// the Environment context (gated by the active agent's toggle). No-op when
	// the toggle is off.
	s.applyHostReminders()
	s.writeStartLine("(session " + name + ")")
	rt.sessions[name] = s
	created = s // decorated by the deferred hook, after rt.mu releases
	// Subagent completions are injected in-process by the runtime's jobManager
	// (finishSubagent notifies the parent directly) — nothing to launch here.
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

// Close closes all live sessions, then the shared parts. Idempotent; a second
// call is a no-op and returns nil.
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
	// Cancel and join all in-process background job goroutines BEFORE the store
	// closes so no goroutine can write to a closed store.
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
