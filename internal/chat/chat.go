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

// ModelInfo describes one selectable model for the /model command, in the
// order it was declared in the config.
type ModelInfo struct {
	// Name is the config-local model name (e.g. "main", "fast").
	Name string
	// ModelID is the provider-specific model id (e.g. "o4-mini").
	ModelID string
	// ContextWindow is the model's max prompt+completion tokens, used by the
	// reminder tracker. Zero means unknown.
	ContextWindow int
}

// ActiveModel is the result of a successful model switch: the new streaming
// client plus the metadata the TUI needs to refresh the status line and
// reminder accounting.
type ActiveModel struct {
	Client        LLMClient
	Params        llm.RequestParams
	ModelID       string
	ContextWindow int
}

// ActiveAgent is the full runtime bundle produced when switching agents:
// everything the chat loop needs to run the next turn under a different agent.
type ActiveAgent struct {
	Personality   persona.Persona
	ToolGuard     func(ctx context.Context, tool string, params map[string]any) (int, string, error)
	ModeLabel     string
	ActiveSkills  []string
	ActiveTools   []string
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
	// Store persists conversation history and memory. Optional; nil keeps
	// the session purely in-memory.
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
	// Truncate, when true, trims oversized tool outputs before sending
	// them back to the model.
	Truncate bool
	// Docs is the rendered shell3_docs payload returned by the docs tool.
	Docs string
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
	// Models lists the selectable models for the /model command, in
	// declaration order. Empty disables /model.
	Models []ModelInfo
	// SwitchModel activates the model with the given config name and returns
	// the new client plus its metadata. Nil disables model switching.
	SwitchModel func(name string) (ActiveModel, error)
	// AgentNames lists configured agents in declaration order, for /agent and
	// Tab cycling. Empty or single-element disables switching.
	AgentNames []string
	// SwitchAgent activates the agent with the given name and returns its full
	// runtime bundle. Nil disables agent switching.
	SwitchAgent func(name string) (ActiveAgent, error)
}

// NewHandlers constructs the built-in tool handler map from a Config.
// Handlers are injected into TurnConfig and looked up by tool name during dispatch.
func NewHandlers(cfg Config) map[string]ToolHandler {
	handlers := []ToolHandler{
		BashHandler{},
		BashBgHandler{},
		EditHandler{},
		PruneHandler{},
		DocsHandler{docs: cfg.Docs},
		StoreHandler{toolName: "memory_upsert"},
		StoreHandler{toolName: "memory_list"},
		StoreHandler{toolName: "memory_search"},
		StoreHandler{toolName: "history_get"},
		StoreHandler{toolName: "history_search"},
	}
	m := make(map[string]ToolHandler, len(handlers))
	for _, h := range handlers {
		m[h.Name()] = h
	}
	return m
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
