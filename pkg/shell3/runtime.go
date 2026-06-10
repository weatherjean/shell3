package shell3

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// RuntimeSpec configures a long-lived Runtime: the process-wide unit owning
// the config (Lua state), store, MCP servers, proxy spawner, and log.
type RuntimeSpec struct {
	ConfigPath string // "" → ./shell3.lua then ~/.shell3/shell3.lua
	WorkDir    string // runtime root; "" → os.Getwd(). Sessions default here.
}

// SessionOpts parameterizes one Session on a Runtime.
type SessionOpts struct {
	// Name keys the session on the runtime (e.g. "tg:1234"). "" gets a unique
	// generated name. Requesting an existing live name returns that session.
	Name string
	// Agent selects the initial agent ("" → first declared).
	Agent string
	// WorkDir roots tool execution for this session ("" → runtime root).
	WorkDir string
	// Headless strips shell_interactive and injects the headless reminder.
	Headless bool
	// OutPath, when non-empty, streams this session's JSONL audit log there.
	OutPath string
	// ShellInteractive runs an interactive shell command with TTY access.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
	// Approve resolves guard "ask" verdicts. Nil fails closed.
	Approve func(ctx context.Context, req ApprovalRequest) bool
	// DisableSubagents strips the spawn tools from this session (used for
	// spawned subagents; depth limit 1).
	DisableSubagents bool
}

// HostEventKind enumerates out-of-turn runtime events. v1: Wake only.
type HostEventKind int

const (
	// Wake signals a session's inbox gained an item while no turn was running.
	// The host should call Session.RunQueued to react.
	Wake HostEventKind = iota
)

// HostEvent is one out-of-turn event for a session. Payload is reserved for
// future kinds; Wake carries none.
type HostEvent struct {
	Session string
	Kind    HostEventKind
	Payload any
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
	// workDir is the runtime root; subagents write audit logs under it.
	workDir string
	// ctx/cancel scope subagent goroutines so they outlive a spawning turn but
	// are bounded by Close.
	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool
}

// NewRuntime loads the config and assembles the shared runtime parts.
// The Runtime must be Closed; sessions left open are closed by Close.
func NewRuntime(spec RuntimeSpec) (*Runtime, error) {
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
		ConfigPath: spec.ConfigPath, CWD: workDir, HomeDir: homeDir,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
				DisableSubagents: o.DisableSubagents,
			})
		},
		cleanup:  cleanup,
		events:   make(chan HostEvent, 64),
		workDir:  workDir,
		ctx:      ctx,
		cancel:   cancel,
		sessions: map[string]*Session{},
	}, nil
}

// Events returns the out-of-turn event bus. One receiver drives N sessions.
// Buffered; if the host is not draining, Wake events coalesce (drop on full —
// the host re-checks inboxes on its next turn anyway).
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

func (rt *Runtime) root() string                 { return rt.workDir }
func (rt *Runtime) baseContext() context.Context { return rt.ctx }

func (rt *Runtime) emit(ev HostEvent) {
	select {
	case rt.events <- ev:
	default: // bus full: drop (Wake is a hint, not a queue)
	}
}

// Session returns the live session named opts.Name, creating it if necessary.
// A closed runtime returns an error. An empty name gets a unique generated
// name ("s<N>"). Requesting an existing live name returns that session unchanged.
func (rt *Runtime) Session(opts SessionOpts) (*Session, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.closed {
		return nil, fmt.Errorf("shell3: runtime is closed")
	}
	if opts.Name == "" {
		// Advance until we find a name not already taken by a live session.
		for {
			rt.nextName++
			opts.Name = fmt.Sprintf("s%d", rt.nextName)
			if _, taken := rt.sessions[opts.Name]; !taken {
				break
			}
		}
	}
	if s, ok := rt.sessions[opts.Name]; ok {
		return s, nil
	}
	cfg, err := rt.sessionConfig(opts)
	if err != nil {
		return nil, err
	}
	// Open the sink before constructing anything stateful: a failure here must
	// not leak a partially-initialised session or a store row.
	sink, sinkCleanup, err := chat.OpenSink(opts.OutPath)
	if err != nil {
		return nil, err
	}
	s := newSession(cfg, func() {}) // shared parts are the runtime's to clean
	s.shellInteractive = opts.ShellInteractive
	if opts.Approve != nil {
		_ = s.SetApprover(opts.Approve) // freshly built session: never busy
	}
	s.runtime, s.name = rt, opts.Name
	s.sink, s.sinkCleanup = sink, sinkCleanup
	s.writeStartLine("(session " + opts.Name + ")")
	rt.sessions[opts.Name] = s
	return s, nil
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
	rt.cleanup()
	// Cancel the runtime ctx so any in-flight subagent goroutine unwinds. Do
	// NOT close rt.events: a late emit from a finishing subagent must not panic.
	if rt.cancel != nil {
		rt.cancel()
	}
	return firstErr
}
