package chat

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
)

// LLMClient is the streaming interface the turn loop calls into. Implementers
// must invoke onEvent synchronously for each delta (token, tool call, usage)
// as it arrives from the provider and return an error only when the stream
// cannot be completed. Returning nil with no events is treated as a no-op
// turn. Implementations may also satisfy llm.TrafficInspector to expose the
// last request/response bytes for error dumps.
type LLMClient interface {
	Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error
}

// ActiveAgent is the full runtime bundle produced when switching agents:
// everything the chat loop needs to run the next turn under a different agent.
type ActiveAgent struct {
	Personality  persona.Persona
	ToolGuard    func(ctx context.Context, tool string, params map[string]any) (int, string, error)
	ModeLabel    string
	ActiveSkills []string
	ActiveTools  []string
	// CustomToolNames is the set of tool names routed to the custom-tool dispatcher.
	CustomToolNames map[string]bool
	LLM             LLMClient
	Params          llm.RequestParams
	ModelID         string
	ContextWindow   int
}

// Config holds all dependencies for a chat session. It is the top-level
// embedding contract: callers populate this once at startup and reuse it
// across turns. TurnConfig is derived from Config for each turn.
type Config struct {
	// LLM is the active streaming client.
	LLM LLMClient
	// Store persists conversation history. Optional; nil keeps the session
	// purely in-memory.
	Store *store.Store
	// Personality is the loaded persona (system prompt, allowed tools).
	Personality persona.Persona
	// RefreshPrompt rebuilds the system prompt with current runtime data
	// (notably a fresh timestamp). The /clear command calls it when starting a
	// new conversation so a long-lived process doesn't carry a stale,
	// boot-time clock into a fresh context. Nil leaves the prompt frozen at
	// construction (the safe default for embedders).
	RefreshPrompt func() string
	// WorkDir is the working directory for tool execution and error dumps.
	WorkDir string
	// StatusLine is the human-readable provider/model/effort line shown in
	// the TUI and used by reminder tracking to detect model changes.
	StatusLine string
	// ModeLabel is a short tag (e.g. "chat", "code") surfaced to renderers.
	ModeLabel string
	// ProjectRef is the project UUID from .ref.
	ProjectRef string
	// ActiveSkills lists skill names enabled for this persona.
	ActiveSkills []string
	// ActiveTools lists tool names enabled for this agent.
	ActiveTools []string
	// ContextWindow is the active model's context window in tokens, used by
	// the reminder tracker to emit context-usage warnings. Zero means unknown.
	ContextWindow int
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
	// CustomTool dispatches a custom (Lua-handler) tool call by name.
	// Nil means no custom tools are wired; unknown tools fall through to
	// the built-in handler map.
	CustomTool func(ctx context.Context, name, argsJSON string) (string, error)
	// CustomToolNames is the set of tool names routed to CustomTool.
	// Entries must match the names registered in the LLM tool schema.
	CustomToolNames map[string]bool
	// ToolGuard runs the on_tool_call guard chain. Nil = allow all.
	// Return values follow the guardAllow/guardBlock/guardCancel constants
	// defined in this package (0/1/2).
	ToolGuard func(ctx context.Context, tool string, params map[string]any) (guardDecision int, reason string, err error)
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
// model client, persona, params, guard chain, tool/skill sets, context window,
// and the derived status line. Every front-end (TUI /agent + Tab, pkg/shell3
// SwitchAgent) and the initial assembly in agentsetup route through this method
// so the agent-derived field copy lives in exactly one place.
//
// It deliberately does NOT touch agent-independent fields (Store, WorkDir,
// ProjectRef, Docs, AgentNames, SwitchAgent, OutPath, Headless, Log,
// RefreshPrompt): those are set once at assembly and survive switches.
func (c *Config) ApplyActiveAgent(rt ActiveAgent) {
	c.LLM = rt.LLM
	c.Personality = rt.Personality
	c.Params = rt.Params
	c.ToolGuard = rt.ToolGuard
	c.ModeLabel = rt.ModeLabel
	c.ActiveSkills = rt.ActiveSkills
	c.ActiveTools = rt.ActiveTools
	c.CustomToolNames = rt.CustomToolNames
	c.ContextWindow = rt.ContextWindow
	c.StatusLine = AgentStatusLine(rt)
}

// NewHandlers constructs the built-in tool handler map from a Config.
// Handlers are injected into TurnConfig and looked up by tool name during dispatch.
func NewHandlers(cfg Config) map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		BashBgHandler{},
		EditHandler{},
		PruneHandler{},
		StoreHandler{toolName: "history_get"},
		StoreHandler{toolName: "history_search"},
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
// last two arguments and otherwise copy the same dozen fields from Config, so
// this is the single place that copy lives. shellInteractive may be nil, in
// which case shell_interactive tool calls return an "unavailable" error.
func NewTurnConfig(cfg Config, handlers map[string]ToolHandler, shellInteractive func(ctx context.Context, cmd, workdir string) string) TurnConfig {
	return TurnConfig{
		LLM:              cfg.LLM,
		Personality:      cfg.Personality,
		StatusLine:       cfg.StatusLine,
		WorkDir:          cfg.WorkDir,
		Store:            cfg.Store,
		Handlers:         handlers,
		Log:              LogOrNoop(cfg.Log),
		Headless:         cfg.Headless,
		CustomTool:       cfg.CustomTool,
		CustomToolNames:  cfg.CustomToolNames,
		ToolGuard:        cfg.ToolGuard,
		ShellInteractive: shellInteractive,
	}
}

// OpenSink opens path for write+truncate (each run starts fresh) and returns
// the sink and a cleanup closure. Returns (nil, no-op, nil) when path is empty.
func OpenSink(path string) (*OutSink, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open --out %s: %w", path, err)
	}
	return newOutSink(f, time.Time{}), func() { _ = f.Close() }, nil
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
