package chat

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// LLMClient is the streaming interface the turn loop calls into — an alias of
// llm.Streamer, which owns the contract (onEvent invoked synchronously per
// delta from one goroutine; nil with no events is a no-op turn).
// Implementations may also satisfy llm.TrafficInspector to expose the last
// request/response bytes for error dumps.
type LLMClient = llm.Streamer

// AgentKnobs are the agent-scoped runtime knobs that follow every agent
// switch as one unit: ActiveAgent carries them, ApplyActiveAgent copies them
// into Config wholesale, and NewTurnConfig forwards them into TurnConfig.
// Adding a knob here flows through all three automatically — no per-field
// copy lines to forget.
type AgentKnobs struct {
	// CustomToolNames is the set of tool names routed to the custom-tool
	// dispatcher (HostTool/ResolveCustomTool). Entries must match the names
	// registered in the LLM tool schema.
	CustomToolNames map[string]bool
	// Subagents is the active agent's allowlist of registered subagent names
	// (its tools.subagents). pkg/shell3 renders it into the per-session
	// Delegation context (which subagents the agent may spawn via the task tool).
	Subagents []string
	// Environment/Delegation are the active agent's host-reminder toggles
	// (luacfg agent.environment / agent.delegation, default off). pkg/shell3
	// gates the standing Environment / Delegation reminders on them.
	Environment bool
	Delegation  bool
	// ContextWindow is the active model's context window in tokens, used by
	// the reminder tracker to emit context-usage warnings. Zero means unknown.
	ContextWindow int
	// CompactAt is the model's auto-compaction prompt-token threshold (0 = off).
	CompactAt int
	// KeepRecent is the verbatim tail (prompt tokens) preserved across an
	// auto-compaction. 0 derives a default from CompactAt (resolveKeepRecent).
	KeepRecent int
	// PruneAt is the lower threshold; stub old tool outputs with no LLM call.
	// 0 disables. Must be below CompactAt.
	PruneAt int
}

// ActiveAgent is the full runtime bundle produced when switching agents:
// everything the chat loop needs to run the next turn under a different agent.
type ActiveAgent struct {
	AgentKnobs
	Personality  persona.Persona
	ModeLabel    string
	ActiveSkills []string
	ActiveTools  []string
	LLM          LLMClient
	Params       llm.RequestParams
	ModelID      string
}

// Config holds all dependencies for a chat session: callers populate it once at
// startup and reuse it across turns. TurnConfig is derived from Config per turn.
type Config struct {
	// LLM is the active streaming client.
	LLM LLMClient
	// Store persists conversation history. Optional; nil keeps the session
	// purely in-memory.
	Store *runs.Store
	// RunsDir is the project's .shell3_project/runs directory path, shown in
	// the system prompt's Environment section (history is searched with rg
	// over it).
	RunsDir string
	// Personality is the loaded persona (system prompt, allowed tools).
	Personality persona.Persona
	// RefreshPrompt rebuilds the system prompt with current runtime data
	// (notably a fresh timestamp). /clear calls it when starting a new
	// conversation so a long-lived process doesn't carry a stale boot-time clock
	// into a fresh context. Nil leaves the prompt frozen at construction.
	RefreshPrompt func() string
	// WorkDir is the working directory for tool execution and error dumps.
	WorkDir string
	// StatusLine is the human-readable provider/model/effort line shown in
	// the TUI and used by reminder tracking to detect model changes.
	StatusLine string
	// ModeLabel is a short tag (e.g. "chat", "code") surfaced to renderers.
	ModeLabel string
	// ConfigPath is the resolved absolute shell3.lua path for this session; ''
	// if unknown. Recorded per session so resume can reload the right
	// config. Agent-independent: set once at assembly, survives agent switches.
	ConfigPath string
	// ConfigWarnings are non-fatal config load issues (e.g. a removed config key
	// that is now ignored). Already logged + printed to stderr at load; also
	// carried here so an interactive front-end can surface them in-band, since an
	// alt-screen TUI clears the stderr line before the user can read it.
	ConfigWarnings []string
	// Theme holds config-global TUI color overrides (token → "#RRGGBB") from
	// shell3.theme{}. Carried here only so pkg/shell3 Snapshot can surface it to a
	// front-end; the chat layer itself never reads it. Agent-independent.
	Theme map[string]string
	// Welcome, if set, is a custom TUI welcome card (shell3.welcome). Carried only
	// for pkg/shell3 Snapshot to surface to a front-end; the chat layer never
	// reads it. Agent-independent.
	Welcome string
	// ActiveSkills lists skill names enabled for this persona.
	ActiveSkills []string
	// ActiveTools lists tool names enabled for this agent.
	ActiveTools []string
	// AgentKnobs are the agent-scoped runtime knobs (context window,
	// compaction thresholds, reminder toggles, custom-tool routing), copied
	// wholesale from the active agent by ApplyActiveAgent so they follow
	// agent switches.
	AgentKnobs
	// Params are provider-level request parameters (temperature, top_p,
	// reasoning effort, etc.).
	Params llm.RequestParams
	// Log is the application logger. Nil is allowed; LogOrNoop wraps it.
	Log applog.Logger
	// OutPath, when non-empty, opens a JSONL audit log at this path and
	// streams every turn event into it. Independent of stdout/TUI rendering.
	OutPath string
	// Headless flips on subprocess-friendly behaviors: strips
	// shell_interactive from the tool schema, injects a system-reminder
	// explaining the constraints, and signals hooks via SHELL3_HEADLESS=1.
	Headless bool
	// ShellInteractive runs an interactive shell command with TTY access and
	// returns the result string to record as tool output. When nil, the
	// shell_interactive tool returns an "unavailable" error string instead.
	// The TUI sets this to a PTY runner that releases the terminal.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
	// ResolveCustomTool resolves a custom-tool call to its executable form
	// (command + env). Nil means no custom tools are wired; unknown tools
	// fall through to the built-in handler map.
	ResolveCustomTool func(name, argsJSON string) (ResolvedTool, error)
	// HostTool dispatches a host-registered Go tool by name (see
	// pkg/shell3.RegisterHostTool). Tried before ResolveCustomTool. Nil = none.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// StubTools maps a hallucinated tool name to its redirect message (a nudge,
	// never an error). Config-global; checked after real/custom tools.
	StubTools map[string]string
	// Asker confirms ask-verdict commands with a human; supplied per-front-end.
	// Nil ⇒ headless: ask degrades to deny.
	Asker AskFunc
	// RunToolCall runs the on_tool_call chain (config-global, nil = no hooks).
	RunToolCall func(ctx context.Context, name, command, argsJSON string) ToolCallVerdict
	// RunToolResult runs the on_tool_result chain (config-global, nil = none).
	RunToolResult func(ctx context.Context, name, argsJSON, output string) string
	// AgentNames lists configured agents in declaration order, for /agent and
	// Tab cycling. Empty or single-element disables switching.
	AgentNames []string
	// SwitchAgent activates the agent with the given name and returns its full
	// runtime bundle. Nil disables agent switching.
	SwitchAgent func(name string) (ActiveAgent, error)
}

// AgentStatusLine renders the status line for a switched-in agent: the agent
// label and its model id, joined by a box-drawing separator. The single source
// for this format, shared by initial config assembly and every agent switch.
func AgentStatusLine(rt ActiveAgent) string {
	return fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID)
}

// ApplyActiveAgent copies a switched agent's runtime bundle into the config:
// model client, persona, params, tool/skill sets, context window, and the
// derived status line. Every front-end (TUI /agent + Tab, pkg/shell3
// SwitchAgent) and the initial assembly in agentsetup route through this method,
// so the agent-derived field copy lives in exactly one place.
//
// It deliberately does NOT touch agent-independent fields (Store, WorkDir,
// ConfigPath, AgentNames, SwitchAgent, OutPath, Headless, Log, RefreshPrompt,
// RunToolCall): those are set once at assembly and survive switches.
func (c *Config) ApplyActiveAgent(rt ActiveAgent) {
	c.LLM = rt.LLM
	c.Personality = rt.Personality
	c.Params = rt.Params
	c.ModeLabel = rt.ModeLabel
	c.ActiveSkills = rt.ActiveSkills
	c.ActiveTools = rt.ActiveTools
	c.AgentKnobs = rt.AgentKnobs
	c.StatusLine = AgentStatusLine(rt)
}

// NewHandlers constructs the built-in tool handler map. Handlers are injected
// into TurnConfig and looked up by tool name during dispatch.
func NewHandlers() map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		BashBgHandler{},
		TaskHandler{},
		TaskListHandler{},
		TaskStatusHandler{},
		TaskCancelHandler{},
		EditHandler{},
		ReadHandler{},
		ListFilesHandler{},
	}
	m := make(map[string]ToolHandler, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
	}
	return m
}

// NewTurnConfig assembles a TurnConfig from a Config, the shared built-in
// handler map, and the front-end's interactive-shell runner. The three
// front-ends (TUI, stdout one-shot, embedded pkg/shell3) differ only in those
// last two arguments, so this is the single place the field copy lives.
// shellInteractive may be nil, in which case shell_interactive tool calls
// return an "unavailable" error.
func NewTurnConfig(cfg Config, handlers map[string]ToolHandler, shellInteractive func(ctx context.Context, cmd, workdir string) string) TurnConfig {
	return TurnConfig{
		ToolConfig: ToolConfig{
			Store:       cfg.Store,
			WorkDir:     cfg.WorkDir,
			Asker:       cfg.Asker,
			RunToolCall: cfg.RunToolCall,
		},
		LLM:               cfg.LLM,
		Personality:       cfg.Personality,
		StatusLine:        cfg.StatusLine,
		ConfigPath:        cfg.ConfigPath,
		Handlers:          handlers,
		Log:               LogOrNoop(cfg.Log),
		Headless:          cfg.Headless,
		ResolveCustomTool: cfg.ResolveCustomTool,
		HostTool:          cfg.HostTool,
		StubTools:         cfg.StubTools,
		AgentKnobs:        cfg.AgentKnobs,
		RunToolResult:     cfg.RunToolResult,
		ShellInteractive:  shellInteractive,
	}
}

// OpenSink opens path for write+truncate (each run starts fresh) and returns
// the sink and a cleanup closure. Returns (nil, no-op, nil) when path is empty.
// lg (nil-safe) receives a one-time warning if the sink ever drops a line —
// this is an audit log, so a silent drop would hide real faults.
func OpenSink(path string, lg applog.Logger) (*OutSink, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	// Create the parent directory: a subagent invocation passes
	// --out .shell3_project/agents/<id>.jsonl, whose directory may not exist yet.
	// Best-effort — a failure here surfaces as the open error below with the
	// same path context.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		_ = os.MkdirAll(dir, 0o755)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open --out %s: %w", path, err)
	}
	sink := newOutSink(f, time.Time{})
	log := LogOrNoop(lg)
	sink.onErr = func(err error) {
		log.Warn("audit sink write failed; further drops are silent", "path", path, "error", err)
	}
	return sink, func() { _ = f.Close() }, nil
}

// LogOrNoop returns l if non-nil, otherwise an applog.Noop logger. Callers
// that did not configure a logger get silent behaviour rather than a nil
// pointer panic.
func LogOrNoop(l applog.Logger) applog.Logger {
	if l != nil {
		return l
	}
	return applog.Noop{}
}

// PruneLastTurn returns messages truncated to remove the last user message
// and everything after it. Used by /rollback to back out the most recent
// exchange. Returns messages unchanged when no user message is present.
func PruneLastTurn(messages []llm.Message) []llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			return messages[:i]
		}
	}
	return messages
}
