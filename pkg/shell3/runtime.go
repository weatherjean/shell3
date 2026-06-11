package shell3

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/store"
)

// TelegramConfig mirrors the parsed shell3.telegram{} block.
type TelegramConfig struct {
	Token     string
	ChatID    string
	Agent     string
	WorkDir   string
	Dashboard DashboardConfig
}

// DashboardConfig mirrors the parsed shell3.telegram.dashboard{} block.
type DashboardConfig struct {
	Enabled bool
	Addr    string
	URL     string
}

// CronJob mirrors one parsed shell3.cron job.
type CronJob struct {
	Name     string
	Schedule string
	Agent    string
	Prompt   string
	WorkDir  string
	Notify   bool
}

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
	// Subagent, when non-empty, runs the named registered subagent's config
	// instead of an agent (set by spawn_agent). Mutually exclusive with Agent.
	Subagent string
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
	// store is the shared SQLite history store (nil if unavailable). Used by
	// PastSessions/SessionTurns for the dashboard's conversation history.
	store *store.Store
	// ctx/cancel scope subagent goroutines so they outlive a spawning turn but
	// are bounded by Close.
	ctx    context.Context
	cancel context.CancelFunc

	telegram TelegramConfig
	cron     []CronJob

	configPath string // captured from RuntimeSpec for Reload
	homeDir    string // captured for Reload's BuildParts

	// subSeq mints process-unique subagent ids (see nextSubID). Global to the
	// runtime so two parents never collide on a "sub:<id>" session name.
	subSeq atomic.Int64
	// wg joins in-flight subagent goroutines at Close. Add happens only under
	// rt.mu while !closed (see trackSubagent), so it can't race wg.Wait.
	wg sync.WaitGroup

	mu       sync.Mutex
	sessions map[string]*Session
	nextName int
	closed   bool
}

// nextSubID returns a process-unique subagent id for this runtime.
func (rt *Runtime) nextSubID() string {
	return fmt.Sprintf("a%d", rt.subSeq.Add(1))
}

// trackSubagent starts fn as a tracked subagent goroutine, unless the runtime
// is already closing. Returns false if the runtime is closed (caller should not
// have created the child). Add happens under rt.mu so it cannot race Close's
// transition to closed + wg.Wait.
func (rt *Runtime) trackSubagent(fn func()) bool {
	rt.mu.Lock()
	if rt.closed {
		rt.mu.Unlock()
		return false
	}
	rt.wg.Add(1)
	rt.mu.Unlock()
	go func() { defer rt.wg.Done(); fn() }()
	return true
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
	tg := parts.Telegram()
	var cronJobs []CronJob
	for _, j := range parts.Cron() {
		cronJobs = append(cronJobs, CronJob{
			Name: j.Name, Schedule: j.Schedule, Agent: j.Agent,
			Prompt: j.Prompt, WorkDir: j.WorkDir, Notify: j.Notify,
		})
	}
	return &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			return parts.SessionConfig(agentsetup.SessionOptions{
				Agent: o.Agent, Subagent: o.Subagent, WorkDir: o.WorkDir, Headless: o.Headless, OutPath: o.OutPath,
				DisableSubagents: o.DisableSubagents,
			})
		},
		cleanup: cleanup,
		store:   parts.Store(),
		telegram: TelegramConfig{
			Token:   tg.Token,
			ChatID:  tg.ChatID,
			Agent:   tg.Agent,
			WorkDir: tg.WorkDir,
			Dashboard: DashboardConfig{
				Enabled: tg.Dashboard.Enabled,
				Addr:    tg.Dashboard.Addr,
				URL:     tg.Dashboard.URL,
			},
		},
		cron:       cronJobs,
		events:     make(chan HostEvent, 64),
		workDir:    workDir,
		configPath: spec.ConfigPath,
		homeDir:    homeDir,
		ctx:        ctx,
		cancel:     cancel,
		sessions:   map[string]*Session{},
	}, nil
}

// Events returns the out-of-turn event bus. One receiver drives N sessions.
// Buffered; if the host is not draining, Wake events coalesce (drop on full —
// the host re-checks inboxes on its next turn anyway).
func (rt *Runtime) Events() <-chan HostEvent { return rt.events }

// Telegram returns the parsed shell3.telegram{} config (zero value if absent).
func (rt *Runtime) Telegram() TelegramConfig { return rt.telegram }

// Cron returns the parsed shell3.cron jobs (nil if absent).
func (rt *Runtime) Cron() []CronJob { return rt.cron }

// SessionMeta summarizes one stored past conversation.
type SessionMeta struct {
	ID        int64  `json:"id"`
	StartedAt string `json:"started_at"`
	EndedAt   string `json:"ended_at,omitempty"`
	NumMsgs   int    `json:"num_msgs"`
	Preview   string `json:"preview"`
}

// PastSessions lists up to limit recent stored conversations (newest first).
// Returns nil if no store is configured.
func (rt *Runtime) PastSessions(limit int) ([]SessionMeta, error) {
	if rt.store == nil {
		return nil, nil
	}
	rows, err := rt.store.ListSessions(limit)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(rows))
	for _, m := range rows {
		e := SessionMeta{ID: m.ID, NumMsgs: m.NumMsgs, Preview: m.Preview, StartedAt: m.StartedAt.Format("2006-01-02 15:04")}
		if !m.EndedAt.IsZero() {
			e.EndedAt = m.EndedAt.Format("2006-01-02 15:04")
		}
		out = append(out, e)
	}
	return out, nil
}

// SessionTurns returns the stored turns of one past conversation as
// HistoryEntry values (Role/Content only; tool args are not persisted).
func (rt *Runtime) SessionTurns(id int64) ([]HistoryEntry, error) {
	if rt.store == nil {
		return nil, nil
	}
	turns, err := rt.store.SessionTurns(id)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(turns))
	for _, t := range turns {
		out = append(out, HistoryEntry{Role: t.Role, Content: t.Content})
	}
	return out, nil
}

// subagentIDRe constrains transcript ids to the minted "a<N>" form, preventing
// any path traversal when building the audit-file path.
var subagentIDRe = regexp.MustCompile(`^[A-Za-z0-9]+$`)

// TranscriptEvent is one lossless event from a subagent's audit log.
type TranscriptEvent struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Role   string `json:"role,omitempty"`
	Tool   string `json:"tool,omitempty"`
	Input  string `json:"input,omitempty"`
	Output string `json:"output,omitempty"`
	CallID string `json:"call_id,omitempty"`
}

// SubagentTranscript reads a subagent's audit JSONL (.shell3/agents/<id>.jsonl
// under the runtime root) and returns its events. Returns nil if absent.
func (rt *Runtime) SubagentTranscript(id string) ([]TranscriptEvent, error) {
	if !subagentIDRe.MatchString(id) {
		return nil, fmt.Errorf("shell3: invalid subagent id %q", id)
	}
	path := filepath.Join(rt.workDir, ".shell3", "agents", id+".jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []TranscriptEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var ev TranscriptEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue // skip malformed lines rather than failing the whole read
		}
		out = append(out, ev)
	}
	return out, nil
}

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
	s.opts = opts
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
	// Join in-flight subagent goroutines (child.Send → child.Close → deliver)
	// so they finish their audit writes before Close returns. rt.closed was set
	// true under rt.mu above, so trackSubagent can no longer Add (no Wait race).
	rt.wg.Wait()
	return firstErr
}
