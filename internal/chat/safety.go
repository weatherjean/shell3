package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/strutil"
)

// ToolCallAction is the disposition of a tool-call hook chain run.
type ToolCallAction int

const (
	ActionRun ToolCallAction = iota
	ActionBlock
	// ActionReview is a soft deny from the hook: an LLM reviewer decides
	// (ToolConfig.ReviewToolCall). Bash-only in v1 — a non-bash review fails
	// closed. No reviewer wired also fails closed.
	ActionReview
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

// gateLogFieldCap bounds how much of a command or reason reaches the app log.
// The gate sees every command the model runs, and a command line can carry a
// secret the operator deliberately keeps in .env — the app log is a plain file
// with none of that file's care around it, so it gets a summary, never a
// transcript. The full command is already in the run replay.
const gateLogFieldCap = 300

// logGateVerdict records a gate decision the operator would otherwise never
// see. An ALLOW is silent on purpose: the gate runs before EVERY tool call, so
// logging passes would bury the refusals in the noise they exist to stand out
// from. Everything else — a block, a rewrite, either half of a review — is a
// rule firing, which is what "has the gate ever done anything?" actually asks.
// Warn for all of them: a rewrite is not an error, but it changed what ran,
// and Info is not enabled on a normal install.
func logGateVerdict(cfg ToolConfig, verdict, name, command, reason string) {
	fields := []any{"tool", name}
	if command != "" {
		fields = append(fields, "command", strutil.Truncate(command, gateLogFieldCap))
	}
	if reason != "" {
		fields = append(fields, "reason", strutil.Truncate(reason, gateLogFieldCap))
	}
	LogOrNoop(cfg.Log).Warn("gate "+verdict, fields...)
}

// resolveGate maps a verdict's disposition to allow/deny, the part every tool
// gate shares: only ActionRun allows — ActionBlock denies with the block
// message, and ANY other action (ActionReview reaching here means a caller
// forgot its pre-check; a future action nobody handles yet) fails closed, so
// the invariant lives in this function rather than every caller's memory.
// What "allow" means — which argv to exec, whether a rewrite is legal — stays
// with each caller (gateBash, gateNonBashTool).
func resolveGate(v ToolCallVerdict) (allowed bool, blockMsg string) {
	switch v.Action {
	case ActionRun:
		return true, ""
	case ActionBlock:
		return false, "error: blocked by tool-call hook: " + v.Reason
	default:
		return false, "error: blocked by tool-call hook: unresolved gate verdict (fail closed): " + v.Reason
	}
}

// isBashTool reports whether a tool name is one of the two bash surfaces. These
// self-gate inside their handlers (with command/rewrite/argv support); every other
// tool is gated by gateNonBashTool in the dispatch loop.
func isBashTool(name string) bool {
	return name == "bash" || name == "bash_bg"
}

// resolveReview turns an ActionReview verdict into run-or-block for a bash
// tool: the reviewer approves (run the original command) or denies (block
// with its message). Fail closed when no reviewer is wired — a soft deny
// must never degrade to an allow.
func resolveReview(ctx context.Context, cfg ToolConfig, name, command, reason string) (blockMsg string, blocked bool) {
	if cfg.ReviewToolCall == nil {
		return "error: blocked by tool-call hook: the gate asked for review (" + reason +
			") but no reviewer is wired — soft deny fails closed. Do not work around " +
			"this; tell the operator the reviewer is unavailable and what you wanted to run.", true
	}
	approved, why := cfg.ReviewToolCall(ctx, name, command, reason)
	if approved {
		return "", false
	}
	if why == "" {
		why = "the reviewer denied this command"
	}
	return "error: blocked by tool-call hook: " + why, true
}

// gateNonBashTool runs the tool-call hook chain for a non-bash tool (edit_file,
// read_media, host tools, …) before it dispatches.
// The gate sees the real name and a nil command (only bash tools carry one), so
// it decides by name / args. Only a pass or a block is meaningful here: a
// {command=...} or {argv=...} verdict cannot apply to a non-bash tool, so it
// fails closed. Returns a block message when the call must not run.
func gateNonBashTool(ctx context.Context, cfg ToolConfig, name, argsJSON string) (blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return "", false // no hooks: ungated
	}
	v := cfg.RunToolCall(ctx, name, "", argsJSON, cfg.Headless)
	if v.Action == ActionReview {
		// Review is bash-only in v1: the reviewer assesses a shell command
		// string; there is no equivalent rendering for a structured tool
		// call, so this fails closed like command/argv verdicts do.
		logGateVerdict(cfg, "blocked", name, "", "review verdict on a non-bash tool fails closed: "+v.Reason)
		return "error: blocked by tool-call hook: a {review} verdict applies only to bash tools, not " +
			name + " — soft deny fails closed.", true
	}
	allowed, msg := resolveGate(v)
	if !allowed {
		logGateVerdict(cfg, "blocked", name, "", v.Reason)
		return msg, true
	}
	// A pure pass (the gate produced no command/argv verdict) is the only Run
	// that applies to a non-bash tool. An actual {command=...}/{argv=...} verdict
	// — including a {command=""} rewrite, indistinguishable from a pass by argv
	// shape — is bash-only and fails closed here.
	if v.Action == ActionRun && !v.Passthrough {
		logGateVerdict(cfg, "blocked", name, "", "a command/argv rewrite verdict applies only to bash tools")
		return "error: blocked by tool-call hook: a {command=...} or {argv=...} verdict " +
			"applies only to bash tools, not " + name + ".", true
	}
	return "", false
}
