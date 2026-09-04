package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/runs"
)

// ToolHandler is one built-in tool (bash, bash_bg, …). Name is the
// canonical name used in the JSON schema and the lookup map; Execute runs
// synchronously and returns the tool result written back to the model — an
// error is surfaced to the user, and the string is still recorded.
//
// Host-registered Go tools dispatch separately (dispatchHostTool).
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// ToolConfig is the state passed to ToolHandler.Execute. Embedded in
// TurnConfig, so a field added here reaches handlers with no copy to forget.
type ToolConfig struct {
	// Store persists chat history and compaction state. May be nil.
	Store *runs.Store
	// WorkDir is the working directory tools should resolve paths against.
	WorkDir string
	// Headless marks turns without an attached human.
	Headless bool
	// TrustedUserContext marks an interactive root turn for ephemeral operator
	// attribution in the persisted transcript.
	TrustedUserContext bool
	// StartBashBg runs a command on the job runtime, returning its id. env is
	// extra "K=V" entries (bash_bg passes nil). Nil disables background jobs.
	StartBashBg func(command, workdir string, argv, env []string) (string, error)
	// Log records genuine handler faults. Nil is safe via LogOrNoop.
	Log applog.Logger
}

// TurnConfig is everything one user→assistant turn needs, derived from a
// Config per turn and passed to RunTurn and each ToolHandler.Execute.
type TurnConfig struct {
	// ToolConfig is embedded so its fields are set and read as TurnConfig
	// fields directly, with no per-call copy that could drift.
	ToolConfig
	// LLM is the streaming client for this turn.
	LLM LLMClient
	// Profile supplies the system prompt and tool allow-list.
	Profile AgentProfile
	// PromptSuffix appends per-session text to the system prompt (see
	// Config.PromptSuffix). Nil appends nothing.
	PromptSuffix func() string
	// ModelID identifies the provider model in context reminders.
	ModelID string
	// Handlers maps tool name to built-in, built once and shared across turns.
	Handlers map[string]ToolHandler
	// HostTool dispatches a host-registered Go tool by name; names in
	// HostToolNames route here. Nil = none registered.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// AgentKnobs (compaction thresholds, host-tool routing, …) are forwarded
	// wholesale from Config by NewTurnConfig.
	AgentKnobs
}
