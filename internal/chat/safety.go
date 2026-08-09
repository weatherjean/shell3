package chat

import (
	"context"
)

// ToolCallAction is the disposition of a tool-call hook chain run.
type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
)

// ToolCallVerdict is the result of the tool-call hook chain for one invocation.
type ToolCallVerdict struct {
	Action ToolCallAction
	Argv   []string // Run: exec exactly this
	Reason string   // Block reason
	// Passthrough is true on Run only when no handler produced a command/argv
	// verdict (a pure fall-through). gateNonBashTool allows a non-bash tool only
	// when this is set: an actual {command=...}/{argv=...} verdict has no meaning
	// for a non-bash tool and must fail closed — including a {command=""} rewrite,
	// whose argv is byte-identical to a pass and so cannot be told apart by shape.
	Passthrough bool
}

// resolveGate maps a verdict's disposition to allow/deny, the part every tool
// gate shares: ActionBlock denies with the block message, ActionRun allows.
// What "allow" means — which argv to exec, whether a rewrite is legal — stays
// with each caller (gateBash, gateNonBashTool).
func resolveGate(v ToolCallVerdict) (allowed bool, blockMsg string) {
	if v.Action == ActionBlock {
		return false, "error: blocked by tool-call hook: " + v.Reason
	}
	return true, ""
}

// isBashTool reports whether a tool name is one of the two bash surfaces. These
// self-gate inside their handlers (with command/rewrite/argv support); every other
// tool is gated by gateNonBashTool in the dispatch loop.
func isBashTool(name string) bool {
	return name == "bash" || name == "bash_bg"
}

// gateNonBashTool runs the tool-call hook chain for a non-bash tool (edit_file,
// read_media, host tools, …) before it dispatches.
// The chain sees the real t.name and a nil t.command (only bash tools carry a
// command), so handlers gate these by t.name / t.args. Only nil / block / ask are
// meaningful here: a {command=...} or {argv=...} verdict can't apply to a non-bash
// tool, so it fails closed. Returns a block message when the call must not run.
func gateNonBashTool(ctx context.Context, cfg ToolConfig, name, argsJSON string) (blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return "", false // no hooks: ungated
	}
	v := cfg.RunToolCall(ctx, name, "", argsJSON, cfg.Headless)
	allowed, msg := resolveGate(v)
	if !allowed {
		return msg, true
	}
	// A pure pass (no handler produced a command/argv verdict) is the only Run
	// that applies to a non-bash tool. An actual {command=...}/{argv=...} verdict
	// — including a {command=""} rewrite, indistinguishable from a pass by argv
	// shape — is bash-only and fails closed here. (An ask-approved call is
	// exempt: the human explicitly approved this exact invocation.)
	if v.Action == ActionRun && !v.Passthrough {
		return "error: blocked by tool-call hook: a {command=...} or {argv=...} verdict " +
			"applies only to bash tools, not " + name + ".", true
	}
	return "", false
}
