package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// ToolHandler is the interface for built-in tool implementations. Each
// built-in tool (bash, edit_file, bash_bg, etc.) implements this.
// Name returns the canonical tool name used in the JSON schema and lookup
// map. Execute runs the tool synchronously and returns the string written
// back to the model as the tool result; non-nil errors are surfaced to the
// user but the returned string is still recorded.
//
// Host-registered Go tools use a separate dispatch path (dispatchHostTool)
// and do not implement this interface.
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// funcHandler adapts a closure to the ToolHandler interface. Used for the
// turn-scoped tools (read_media) whose
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

// ToolConfig holds the state passed to ToolHandler.Execute. It is embedded in
// TurnConfig — the turn loop hands each handler the turn's ToolConfig directly,
// so a field added here reaches handlers with no per-field copy to forget.
type ToolConfig struct {
	// Store is the persistence layer for the history tools. May be nil.
	Store *runs.Store
	// WorkDir is the working directory tools should resolve paths against.
	WorkDir string
	// Asker confirms an ask-verdict command with a human. Nil ⇒ ask degrades to
	// deny (headless subagent path).
	Asker AskFunc
	// HeadlessAsk is true when no human asker is attached to the session — an
	// ask verdict would degrade to deny. Forwarded to the tool-call hook
	// as .headless so the gate script can branch on it. Independent of the
	// disable_safety toggle (which affects ask resolution, not human presence).
	HeadlessAsk bool
	// StartBashBg launches a background shell command on the host's in-process
	// job runtime and returns its job id. env holds extra "K=V" entries appended
	// to the inherited environment (bash_bg passes nil). quiet makes a clean
	// exit queue its completion notice for the agent's next turn instead of
	// waking it (failures always wake). Nil func ⇒ background jobs disabled.
	StartBashBg func(command, workdir string, argv, env []string, quiet bool) (string, error)
	// StartSubagent launches a background subagent (child session) and returns its
	// id. It enforces the concurrency cap; single-level delegation holds by
	// construction (subagents are never given the task tool). Nil ⇒ subagents
	// unavailable.
	StartSubagent func(agent, prompt, desc string) (string, error)
	// ListJobs returns a compact formatted list of all background jobs (running +
	// done) for the task_list tool. Nil ⇒ task management unavailable.
	ListJobs func() string
	// JobStatus returns one job's status and truncated result for the task_status
	// tool. Nil ⇒ task management unavailable.
	JobStatus func(id string) string
	// CancelJob cancels a running job and returns a short confirmation or error
	// for the task_cancel tool. Nil ⇒ task management unavailable.
	CancelJob func(id string) string
	// RunToolCall runs the tool-call hook chain (pass / rewrite / argv / block /
	// ask) with the real tool name. The bash family self-gates via this in their
	// handlers (gateBash); every other tool is gated in the
	// dispatch loop via gateNonBashTool. Nil = no hooks declared (everything runs —
	// the unsafe default). Config-global. headless carries HeadlessAsk to the
	// chain as t.headless.
	RunToolCall func(ctx context.Context, name, command, argsJSON string, headless bool) ToolCallVerdict
}

// TurnConfig holds all dependencies needed for one user→assistant turn. It
// is derived from a Config per turn and passed to RunTurn (and through it to
// each ToolHandler.Execute). Handlers should be constructed once via
// NewHandlers and reused across turns.
type TurnConfig struct {
	// ToolConfig is the per-turn tool-execution state (WorkDir, Store,
	// Asker, job-runtime hooks, RunToolCall) handed to each ToolHandler.Execute.
	// Embedded so its fields are set and read as TurnConfig fields directly and
	// there is no per-call copy that could drift.
	ToolConfig
	// LLM is the streaming client for this turn.
	LLM LLMClient
	// Personality is the persona whose system prompt and tool allow-list
	// drive this turn.
	Personality persona.Persona
	// StatusLine is the current provider/model/effort string; used for
	// reminder tracking.
	StatusLine string
	// ConfigDir is the resolved config directory, threaded into new store
	// sessions (notably the compaction rollover, which starts a session deep in
	// the turn loop). '' if unknown.
	ConfigDir string
	// Handlers maps tool name to built-in implementation. Built once via
	// NewHandlers and shared across turns.
	Handlers map[string]ToolHandler
	// Log is the turn-scoped logger. Nil is safe via LogOrNoop.
	Log applog.Logger
	// Headless is true when shell3 runs without a human at the keyboard
	// (subagents, and any front-end that attaches no asker). turn.go injects a
	// system reminder when this is set.
	Headless bool
	// HostTool dispatches a host-registered Go tool (internal/shell3.RegisterHostTool)
	// by name, returning its result string. Names in HostToolNames route here.
	// Nil = none registered.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// RunToolResult runs the on_tool_result chain over a tool's output
	// (config-global, nil = none). Its input sibling RunToolCall lives on the
	// embedded ToolConfig because handlers self-gate with it.
	RunToolResult func(ctx context.Context, name, argsJSON, output string) string
	// AgentKnobs are the agent-scoped runtime knobs (compaction thresholds,
	// host-tool routing, …), forwarded wholesale from Config by NewTurnConfig.
	AgentKnobs
}
