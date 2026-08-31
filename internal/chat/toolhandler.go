package chat

import (
	"context"
	"encoding/json"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// ToolHandler is one built-in tool (bash, edit_file, bash_bg, …). Name is the
// canonical name used in the JSON schema and the lookup map; Execute runs
// synchronously and returns the tool result written back to the model — an
// error is surfaced to the user, and the string is still recorded.
//
// Host-registered Go tools dispatch separately (dispatchHostTool).
type ToolHandler interface {
	Name() string
	Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

// ToolReviewRequest is the exact bash action and bounded conversation evidence
// handed to the configured reviewer. TrustedUserContext is true only for an
// interactive root session; individual human messages additionally carry
// ephemeral OperatorContent so generated RoleUser carriers cannot authorize.
type ToolReviewRequest struct {
	Name               string
	Command            string
	Reason             string
	WorkDir            string
	Headless           bool
	TrustedUserContext bool
	Messages           []llm.Message
}

// funcHandler adapts a closure to ToolHandler; tests use it to stand up
// ad-hoc handlers without a dedicated type per case.
type funcHandler struct {
	name string
	fn   func(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}

func (h funcHandler) Name() string { return h.name }

func (h funcHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return h.fn(ctx, id, args, cfg)
}

// ToolConfig is the state passed to ToolHandler.Execute. Embedded in
// TurnConfig, so a field added here reaches handlers with no copy to forget.
type ToolConfig struct {
	// Store is the persistence layer for the history tools. May be nil.
	Store *runs.Store
	// WorkDir is the working directory tools should resolve paths against.
	WorkDir string
	// Headless: no human at the keyboard (subagents, cron). Reaches the gate
	// as .headless; turn.go injects a system reminder when set.
	Headless bool
	// HasParentAgent distinguishes subagents from ownerless headless roots such
	// as cron sessions. Only a subagent can hand a blocker up a parent chain.
	HasParentAgent bool
	// StartBashBg runs a command on the job runtime, returning its id. env is
	// extra "K=V" entries (bash_bg passes nil); report is the single axis for
	// what the finish does to the chat (see notify.ReportMode); note is
	// context carried into the completion mail. Nil ⇒ background jobs
	// disabled.
	StartBashBg func(command, workdir string, argv, env []string, report notify.ReportMode, note string) (string, error)
	// StartSubagent launches a child session under the concurrency cap and
	// returns its id; report/note as on StartBashBg. Nil ⇒ unavailable.
	StartSubagent func(agent, prompt, desc string, report notify.ReportMode, note string) (string, error)
	// ListJobs formats every job, running and done, for task_list.
	// Nil ⇒ task management unavailable.
	ListJobs func() string
	// JobStatus is one job's state and truncated result, for task_status.
	JobStatus func(id string) string
	// CancelJob cancels one job, returning a confirmation, for task_cancel.
	CancelJob func(id string) string
	// RunToolCall runs the gate (pass / rewrite / argv / block). The bash
	// family self-gates in its handlers (gateBash); everything else is gated
	// in the dispatch loop (gateNonBashTool). Nil = no gate declared, so
	// everything runs — the unsafe default. Config-global.
	RunToolCall func(ctx context.Context, name, command, argsJSON string, headless bool) ToolCallVerdict
	// ReviewToolCall resolves a {review} soft deny: an LLM reviewer judges an
	// exact bash action against bounded turn context. Nil = no reviewer, so a
	// review verdict fails closed. Config-global.
	ReviewToolCall func(ctx context.Context, request ToolReviewRequest) (approved bool, denyMsg string)
	// ReviewMessages is a per-call snapshot populated by the turn loop before
	// handler dispatch. It includes the current assistant tool call.
	ReviewMessages []llm.Message
	// TrustedUserContext is true only for an attached root operator. Individual
	// messages still need ephemeral OperatorContent to authorize an action.
	TrustedUserContext bool
	// Log lives here, not on TurnConfig, because the bash family self-gates
	// inside its handler and only ever sees a ToolConfig — a gate verdict the
	// operator never hears about is what this placement prevents. The embed
	// promotes it, so cfg.Log reads the same either way. Nil is safe via
	// LogOrNoop.
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
	// Personality supplies the system prompt and tool allow-list.
	Personality persona.Persona
	// RefreshPrompt re-renders the system prompt at turn start, so a
	// long-lived session sees current context files rather than a
	// session-creation snapshot. Nil keeps Personality.SystemPrompt.
	RefreshPrompt func() string

	// PromptSuffix appends per-session text to the system prompt (see
	// Config.PromptSuffix). Nil appends nothing.
	PromptSuffix func() string
	// StatusLine is the provider/model/effort string, for reminder tracking.
	StatusLine string
	// ConfigDir is threaded into new store sessions — notably the compaction
	// rollover, which starts one deep in the turn loop. '' if unknown.
	ConfigDir string
	// Agent, ParentID and CronJob mirror Config's fields of the same name,
	// threaded into the compaction rollover so a rolled session keeps its
	// cron attribution (see Config.CronJob).
	Agent    string
	ParentID string
	CronJob  string
	// Handlers maps tool name to built-in, built once and shared across turns.
	Handlers map[string]ToolHandler
	// HostTool dispatches a host-registered Go tool by name; names in
	// HostToolNames route here. Nil = none registered.
	HostTool func(ctx context.Context, name, argsJSON string) (string, error)
	// RunToolResult rewrites a tool's output (config-global, nil = none). Its
	// input sibling RunToolCall sits on ToolConfig — handlers self-gate.
	RunToolResult func(ctx context.Context, name, argsJSON, output string) string
	// AgentKnobs (compaction thresholds, host-tool routing, …) are forwarded
	// wholesale from Config by NewTurnConfig.
	AgentKnobs
}
