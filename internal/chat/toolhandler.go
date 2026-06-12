package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
)

// ResolvedTool is a custom-tool call reduced to an executable form: the bash
// command, the env to run it with (declared params + secrets), and dispatch
// knobs. Produced by agentsetup (via luacfg.ResolveCustomCall) and run by
// dispatchCustomTool.
type ResolvedTool struct {
	Command    string
	Env        []string
	Background bool
	Timeout    int
}

// ToolHandler is the interface for built-in tool implementations. Each
// built-in tool (bash, edit_file, bash_bg, etc.) implements this.
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

// funcHandler adapts a closure to the ToolHandler interface. Used for the
// turn-scoped tools (shell_interactive, read_media) whose
// implementations close over the tool loop's mutable state — see
// turnScopedHandlers in turn.go.
type funcHandler struct {
	name string
	fn   func(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

func (h funcHandler) Name() string { return h.name }

func (h funcHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return h.fn(ctx, id, args, cfg)
}

// ToolConfig holds per-invocation state passed to ToolHandler.Execute. It is
// constructed fresh for each tool call from the current TurnConfig and the
// session's working message slices. Mutations to AllMsgs and SessMsgs
// elements propagate to the caller's slices (handlers that rewrite prior
// messages in place rely on this).
type ToolConfig struct {
	// Store is the persistence layer for the history tools. May be nil.
	Store *store.Store
	// WorkDir is the working directory tools should resolve paths against.
	WorkDir string
	// SinkPath is the session's notification sink (internal/sink): bash_bg's
	// reaper appends a bg_done notification here on process exit. Empty when the
	// front-end wires no sink (the bg job is still spawned, just not announced).
	SinkPath string
	// WrapBash, when non-nil, is the shell3.wrap_bash hook: the bash/bash_bg
	// handlers pass their parsed command through it before execution. It returns
	// the (possibly rewritten) command to run, whether the call is allowed, and a
	// block reason. Nil means no hook is declared — the tools run the command
	// verbatim (the unsafe default). The hook FAILS CLOSED on error (see
	// luacfg.WrapBash): a broken wrapper blocks rather than silently runs.
	WrapBash func(ctx context.Context, cmd string) (rewritten string, allowed bool, reason string, err error)
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
	// SinkPath is the session's notification sink, threaded to each tool call's
	// ToolConfig (see ToolConfig.SinkPath). Empty disables sink notifications.
	SinkPath string
	// Store persists newly appended messages when non-nil.
	Store *store.Store
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
	// ResolveCustomTool resolves a custom-tool call to its executable form
	// (command + env). Names in CustomToolNames route here.
	ResolveCustomTool func(name, argsJSON string) (ResolvedTool, error)
	// HostTool dispatches a host-registered Go tool (pkg/shell3.RegisterHostTool)
	// by name, returning its result string. Tried BEFORE ResolveCustomTool so an
	// embedding host can supply native tools (which return strings directly, not
	// bash commands) alongside command-template tools. Nil = none registered.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// CustomToolNames is the set of tool names routed to HostTool/ResolveCustomTool.
	CustomToolNames map[string]bool
	// StubTools maps a hallucinated tool name to its redirect message (a nudge,
	// never an error). Checked after real/custom tools so a real tool always wins.
	StubTools map[string]string
	// WrapBash is the shell3.wrap_bash hook threaded to each tool call's
	// ToolConfig (see ToolConfig.WrapBash). Nil = no hook = run commands verbatim.
	WrapBash func(ctx context.Context, cmd string) (rewritten string, allowed bool, reason string, err error)
	// CompactAt is the auto-compaction prompt-token threshold (0 = off).
	// maybeCompact (called at the top of RunTurn) consults it.
	CompactAt int
}
