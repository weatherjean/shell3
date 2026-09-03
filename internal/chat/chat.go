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

// Config is a chat session's dependencies, populated once and reused across
// turns; TurnConfig derives from it per turn.
type Config struct {
	// LLM is the active streaming client.
	LLM LLMClient
	// Store persists history; nil keeps the session in-memory.
	Store *runs.Store
	// RenderEnvironment optionally replaces the legacy kit-oriented host
	// reminder. It receives the durable session id. Nil keeps the legacy
	// renderer; replacement config paths use this to state only paths and
	// capabilities they actually provide.
	RenderEnvironment func(sessionID string) string
	// Profile is the resolved agent's system prompt and allowed tools.
	Profile AgentProfile
	// PromptSuffix appends per-session text, such as a room brief. A closure
	// lets metadata refresh without rebuilding the session.
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
	// ActiveSkills lists skill names enabled for this agent.
	ActiveSkills []string
	// AgentKnobs are copied wholesale into each turn.
	AgentKnobs
	// Log is the application logger. Nil is allowed; LogOrNoop wraps it.
	Log applog.Logger
	// Headless injects the no-human-attached reminder.
	Headless bool
	// HostTool dispatches a host-registered Go tool by name (see
	// internal/shell3.RegisterHostTool). Nil = none.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
}

// NewHandlers constructs the built-in tool handler map, looked up by name
// during dispatch.
func NewHandlers() map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		BashBgHandler{},
		EditHandler{},
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
			TrustedUserContext: !cfg.Headless && cfg.ParentID == "" && cfg.CronJob == "",
			Log:                LogOrNoop(cfg.Log),
		},
		LLM:          cfg.LLM,
		Profile:      cfg.Profile,
		PromptSuffix: cfg.PromptSuffix,
		ModelID:      cfg.ModelID,
		ConfigDir:    cfg.ConfigDir,
		Agent:        cfg.Agent,
		ParentID:     cfg.ParentID,
		CronJob:      cfg.CronJob,
		Handlers:     handlers,
		HostTool:     cfg.HostTool,
		AgentKnobs:   cfg.AgentKnobs,
	}
}

// LogOrNoop gives a caller with no logger silence rather than a nil panic.
func LogOrNoop(l applog.Logger) applog.Logger {
	if l != nil {
		return l
	}
	return applog.Noop{}
}
