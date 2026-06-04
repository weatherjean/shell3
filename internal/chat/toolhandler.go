package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
)

// ToolHandler is the interface for built-in tool implementations. Each
// built-in tool (bash, edit_file, prune_tool_result, etc.) implements this.
// Name returns the canonical tool name used in the JSON schema and lookup
// map. Execute runs the tool synchronously and returns the string written
// back to the model as the tool result; non-nil errors are surfaced to the
// user but the returned string is still recorded.
//
// User tools (YAML-defined) use a separate dispatch path and do not
// implement this interface.
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// ToolConfig holds per-invocation state passed to ToolHandler.Execute. It is
// constructed fresh for each tool call from the current TurnConfig and the
// session's working message slices. Mutations to AllMsgs and SessMsgs
// elements propagate to the caller's slices (PruneHandler relies on this).
type ToolConfig struct {
	// Store is the persistence layer for the history tools. May be nil.
	Store *store.Store
	// WorkDir is the working directory tools should resolve paths against.
	WorkDir string
	// AllMsgs is the full conversation slice including any reminder
	// injections; tools that need to operate on what the model sees use
	// this view.
	AllMsgs []llm.Message
	// SessMsgs is the persisted session history slice without reminder
	// injections; tools that mutate authoritative state use this view.
	SessMsgs []llm.Message
}

// TurnConfig holds all dependencies needed for one user→assistant turn. It
// is derived from a Config per turn and passed to RunTurn (and through it to
// each ToolHandler.Execute). Handlers should be constructed once via
// NewHandlers and reused across turns.
type TurnConfig struct {
	// LLM is the streaming client for this turn.
	LLM LLMClient
	// Personality is the persona whose system prompt and tool allow-list
	// drive this turn.
	Personality persona.Persona
	// StatusLine is the current provider/model/effort string; used for
	// reminder tracking.
	StatusLine string
	// WorkDir is the working directory for tool execution.
	WorkDir string
	// Store persists newly appended messages when non-nil.
	Store *store.Store
	// Truncate enables tool-output truncation before sending back to the model.
	Truncate bool
	// Handlers maps tool name to built-in implementation. Built once via
	// NewHandlers and shared across turns.
	Handlers map[string]ToolHandler
	// Log is the turn-scoped logger. Nil is safe via LogOrNoop.
	Log applog.Logger
	// Headless is true when shell3 runs as a subprocess (no human at the
	// keyboard). turn.go drops shell_interactive and injects a system
	// reminder when this is set.
	Headless bool
	// ShellInteractive runs an interactive shell command with TTY access.
	// When nil, turn.go returns an "unavailable" error string for
	// shell_interactive tool calls. The TUI wires this to a PTY runner that
	// releases the terminal; headless leaves it nil or stubs an error.
	ShellInteractive func(ctx context.Context, cmd, workdir string) string
	// CustomTool dispatches a custom (Lua-handler) tool call by name.
	// Nil means no custom tools are wired.
	CustomTool func(ctx context.Context, name, argsJSON string) (string, error)
	// CustomToolNames is the set of tool names routed to CustomTool.
	CustomToolNames map[string]bool
	// ToolGuard runs the on_tool_call guard chain. Nil = allow all.
	// Return values follow the guardAllow/guardBlock/guardCancel constants
	// (0=allow, 1=block, 2=cancel).
	ToolGuard func(ctx context.Context, tool string, params map[string]any) (guardDecision int, reason string, err error)
}
