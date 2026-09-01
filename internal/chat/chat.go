package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// LLMClient is the turn loop's streaming interface, an alias of llm.Streamer,
// which owns the contract. Implementations may also satisfy
// llm.TrafficInspector, exposing the last bytes for error dumps.
type LLMClient = llm.Streamer

// AgentProfile is the model-facing part of one resolved agent: its current
// system prompt and exposed tool schema. It lives with the turn
// configuration that consumes it; there is no separate persona subsystem.
type AgentProfile struct {
	SystemPrompt string
	Tools        []llm.ToolDefinition
}

// AgentKnobs carries the resolved per-agent runtime settings as one unit from
// assembly into each turn.
type AgentKnobs struct {
	// HostToolNames routes to the HostTool dispatcher. Entries must match the
	// names in the LLM tool schema.
	HostToolNames map[string]bool
	// Subagents is the allowlist internal/shell3 validates task spawns
	// against; the schema-side listing lives in the task tool itself.
	Subagents []string
	// ContextWindow feeds the reminder tracker's usage warnings; 0 = unknown.
	ContextWindow int
	// CompactAt is the model's auto-compaction prompt-token threshold (0 = off).
	CompactAt int
	// KeepRecent is the verbatim tail across a compaction; 0 derives it from
	// CompactAt.
	KeepRecent int
	// PruneAt is the lower threshold; stub old tool outputs with no LLM call.
	// 0 disables. Must be below CompactAt.
	PruneAt int
}

// ActiveAgent is the resolved runtime bundle for one declared agent.
type ActiveAgent struct {
	AgentKnobs
	Profile      AgentProfile
	Agent        string
	ActiveSkills []string
	LLM          LLMClient
	Params       llm.RequestParams
	ModelID      string
}

// Config is a chat session's dependencies, populated once and reused across
// turns; TurnConfig derives from it per turn.
type Config struct {
	// LLM is the active streaming client.
	LLM LLMClient
	// Store persists history; nil keeps the session in-memory.
	Store *runs.Store
	// RunsDir is where job logs live, named in the Environment reminder.
	RunsDir string
	// Profile is the resolved agent's system prompt and allowed tools.
	Profile AgentProfile
	// RefreshPrompt rebuilds the system prompt at the start of EVERY turn, so
	// a long-lived conversation tracks current context files rather than a
	// session-creation snapshot. Nil freezes the prompt at construction.
	RefreshPrompt func() string
	// PromptSuffix appends per-SESSION text, such as a room's brief. A
	// closure, not a string, for RefreshPrompt's reason: a cached string
	// would freeze the suffix at session creation, and a mid-conversation
	// edit would never take effect.
	PromptSuffix func() string
	// WorkDir is the working directory for tool execution and error dumps.
	WorkDir string
	// ModelID is the provider model identifier used by this agent.
	ModelID string
	// ConfigDir is recorded per session so a resume reloads the right config.
	// Agent-independent: set once at assembly.
	ConfigDir string
	// Agent, ParentID and CronJob identify this session in runs.Meta terms.
	// Set once at assembly and carried onto every runs session this
	// conversation rolls onto — notably the compaction rollover, so a session
	// that compacts mid-run keeps its cron attribution.
	Agent    string
	ParentID string
	CronJob  string
	// ConfigWarnings are non-fatal load issues, already logged and printed to
	// stderr, carried here so a front-end can surface them in-band to someone
	// who never sees that stderr line.
	ConfigWarnings []string
	// ActiveSkills lists skill names enabled for this agent.
	ActiveSkills []string
	// AgentKnobs are copied wholesale by ApplyActiveAgent.
	AgentKnobs
	// Params are provider-level request parameters (temperature, top_p,
	// reasoning effort, etc.).
	Params llm.RequestParams
	// Log is the application logger. Nil is allowed; LogOrNoop wraps it.
	Log applog.Logger
	// OnEvent is the kit event: seam, observing every event this session
	// emits. It runs inline, so real work must hand off to its own worker.
	OnEvent func(Event)
	// Headless injects the no-human-attached reminder and signals hooks via
	// SHELL3_HEADLESS=1.
	Headless bool
	// HostTool dispatches a host-registered Go tool by name (see
	// internal/shell3.RegisterHostTool). Nil = none.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// MCPStatus is the declared servers' live health, nil when none is
	// declared. Set once at assembly, surfaced by Snapshot.
	MCPStatus func() []MCPServerStatus
	// RunToolCall runs the tool-call hook chain (config-global, nil = no hooks).
	RunToolCall func(ctx context.Context, name, command, argsJSON string, headless bool) ToolCallVerdict
	// ReviewToolCall resolves a {review} soft deny via the LLM reviewer
	// (config-global, nil = review verdicts fail closed).
	ReviewToolCall func(ctx context.Context, request ToolReviewRequest) (approved bool, denyMsg string)
	// RunToolResult runs the on_tool_result chain (config-global, nil = none).
	RunToolResult func(ctx context.Context, name, argsJSON, output string) string
}

// MCPServerStatus mirrors internal/mcp's health type, so chat stays
// independent of the MCP client; agentsetup converts.
type MCPServerStatus struct {
	Name      string
	Up        bool
	ToolCount int
	Err       string
}

// ApplyActiveAgent copies an agent's runtime bundle into the config — client,
// profile, params, tool and skill sets, knobs, and model id. Assembly routes
// through it, so the agent-derived field copy lives in one place.
//
// It deliberately does NOT touch the agent-independent fields (Store, WorkDir,
// ConfigDir, Headless, Log, RefreshPrompt, RunToolCall), set once at assembly.
func (c *Config) ApplyActiveAgent(rt ActiveAgent) {
	c.LLM = rt.LLM
	c.Profile = rt.Profile
	c.Params = rt.Params
	c.Agent = rt.Agent
	c.ModelID = rt.ModelID
	c.ActiveSkills = rt.ActiveSkills
	c.AgentKnobs = rt.AgentKnobs
}

// NewHandlers constructs the built-in tool handler map, looked up by name
// during dispatch.
func NewHandlers() map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		BashBgHandler{},
		TaskHandler{},
		TaskListHandler{},
		TaskStatusHandler(),
		TaskCancelHandler(),
		EditHandler{},
		HistoryHandler{},
	}
	m := make(map[string]ToolHandler, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
	}
	return m
}

// NewTurnConfig is the single place the Config→TurnConfig field copy lives.
func NewTurnConfig(cfg Config, handlers map[string]ToolHandler) TurnConfig {
	return TurnConfig{
		ToolConfig: ToolConfig{
			Store:              cfg.Store,
			WorkDir:            cfg.WorkDir,
			Headless:           cfg.Headless,
			HasParentAgent:     cfg.ParentID != "",
			RunToolCall:        cfg.RunToolCall,
			ReviewToolCall:     cfg.ReviewToolCall,
			TrustedUserContext: !cfg.Headless && cfg.ParentID == "" && cfg.CronJob == "",
			Log:                LogOrNoop(cfg.Log),
		},
		LLM:           cfg.LLM,
		Profile:       cfg.Profile,
		RefreshPrompt: cfg.RefreshPrompt,
		PromptSuffix:  cfg.PromptSuffix,
		ModelID:       cfg.ModelID,
		ConfigDir:     cfg.ConfigDir,
		Agent:         cfg.Agent,
		ParentID:      cfg.ParentID,
		CronJob:       cfg.CronJob,
		Handlers:      handlers,
		HostTool:      cfg.HostTool,
		AgentKnobs:    cfg.AgentKnobs,
		RunToolResult: cfg.RunToolResult,
	}
}

// LogOrNoop gives a caller with no logger silence rather than a nil panic.
func LogOrNoop(l applog.Logger) applog.Logger {
	if l != nil {
		return l
	}
	return applog.Noop{}
}
