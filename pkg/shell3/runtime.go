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
	return &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
			})
		},
		cleanup:  cleanup,
		sessions: map[string]*Session{},
	}, nil
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
	s.runtime, s.name = rt, opts.Name
	s.sink, s.sinkCleanup = sink, sinkCleanup
	if sink != nil {
		_, model := chat.SplitStatus(cfg.StatusLine)
		sink.WriteStart("(session "+opts.Name+")", cfg.ModeLabel, model, cfg.OutPath, cfg.Headless)
	}
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
	return firstErr
}
