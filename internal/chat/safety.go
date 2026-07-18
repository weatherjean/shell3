package chat

import (
	"context"
	"time"
)

// AskFunc asks a human to approve command (reason explains why it was gated).
// Front-ends supply it (an interactive approval prompt). Nil means no human is
// attached (headless subagent) — on_tool_call then denies instead of asking.
type AskFunc func(ctx context.Context, command, reason string) bool

// DefaultAskTimeout bounds how long an ask verdict waits for a human before it
// falls back to deny. Applied when a handler's ask verdict sets no ask_timeout.
const DefaultAskTimeout = 5 * time.Minute

// ToolCallAction is the disposition of an on_tool_call chain run.
type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
	ActionAsk
)

// ToolCallVerdict is the result of the on_tool_call chain for one invocation.
type ToolCallVerdict struct {
	Action     ToolCallAction
	Argv       []string      // Run: exec exactly this
	Prompt     string        // Ask: human prompt
	Reason     string        // Block reason / Ask deny-reason
	AskTimeout time.Duration // Ask: 0 = DefaultAskTimeout
	// Passthrough is true on Run only when no handler produced a command/argv
	// verdict (a pure fall-through). gateNonBashTool allows a non-bash tool only
	// when this is set: an actual {command=...}/{argv=...} verdict has no meaning
	// for a non-bash tool and must fail closed — including a {command=""} rewrite,
	// whose argv is byte-identical to a pass and so cannot be told apart by shape.
	Passthrough bool
}

// resolveGate maps a verdict's disposition to allow/deny, the part every tool
// gate shares: ActionBlock denies with the block message, ActionAsk defers to
// the human (deny on decline/headless), ActionRun allows. What "allow" means —
// which argv to exec, whether a rewrite is legal — stays with each caller
// (gateBash, gateNonBashTool).
func resolveGate(ctx context.Context, asker AskFunc, v ToolCallVerdict) (allowed bool, blockMsg string) {
	switch v.Action {
	case ActionBlock:
		return false, "error: blocked by on_tool_call: " + v.Reason
	case ActionAsk:
		if resolveAsk(ctx, asker, v) {
			return true, ""
		}
		return false, "error: blocked by on_tool_call — needs human approval (" + v.Reason +
			"). Stop and ask the human before running this."
	default: // ActionRun
		return true, ""
	}
}

// resolveAsk presents an Ask verdict to the human via asker, bounded by the
// verdict's timeout (or DefaultAskTimeout). With no asker (headless) it denies.
// Returns true to allow the command to run.
func resolveAsk(ctx context.Context, asker AskFunc, v ToolCallVerdict) bool {
	if asker == nil {
		return false
	}
	d := v.AskTimeout
	if d <= 0 {
		d = DefaultAskTimeout
	}
	askCtx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return asker(askCtx, v.Prompt, v.Reason)
}

// isBashTool reports whether a tool name is one of the two bash surfaces. These
// self-gate inside their handlers (with command/rewrite/argv support); every other
// tool is gated by gateNonBashTool in the dispatch loop.
func isBashTool(name string) bool {
	return name == "bash" || name == "bash_bg"
}

// gateNonBashTool runs the on_tool_call chain for a non-bash tool (edit_file,
// read_media, host tools, …) before it dispatches.
// The chain sees the real t.name and a nil t.command (only bash tools carry a
// command), so handlers gate these by t.name / t.args. Only nil / block / ask are
// meaningful here: a {command=...} or {argv=...} verdict can't apply to a non-bash
// tool, so it fails closed. Returns a block message when the call must not run.
func gateNonBashTool(ctx context.Context, cfg ToolConfig, name, argsJSON string) (blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return "", false // no hooks: ungated
	}
	v := cfg.RunToolCall(ctx, name, "", argsJSON, cfg.HeadlessAsk)
	allowed, msg := resolveGate(ctx, cfg.Asker, v)
	if !allowed {
		return msg, true
	}
	// A pure pass (no handler produced a command/argv verdict) is the only Run
	// that applies to a non-bash tool. An actual {command=...}/{argv=...} verdict
	// — including a {command=""} rewrite, indistinguishable from a pass by argv
	// shape — is bash-only and fails closed here. (An ask-approved call is
	// exempt: the human explicitly approved this exact invocation.)
	if v.Action == ActionRun && !v.Passthrough {
		return "error: blocked by on_tool_call: a {command=...} or {argv=...} verdict " +
			"applies only to bash tools, not " + name + ".", true
	}
	return "", false
}
