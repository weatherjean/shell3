package chat

import (
	"context"
	"time"
)

// AskFunc asks a human to approve command (reason explains why it was gated).
// Front-ends supply it (TUI prompt or equivalent). Nil means no human is
// attached (headless subagent) — on_tool_call then denies instead of asking.
type AskFunc func(ctx context.Context, command, reason string) bool

// DefaultAskTimeout bounds how long an ask verdict waits for a human before it
// falls back to deny. Applied when a handler's ask verdict sets no ask_timeout.
const DefaultAskTimeout = 5 * time.Minute

// ToolCallAction is the disposition of an on_tool_call chain run.
type ToolCallAction int

const (
	Run ToolCallAction = iota
	Block
	Ask
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

// isBashTool reports whether a tool name is one of the three bash surfaces. These
// self-gate inside their handlers (with command/rewrite/argv support); every other
// tool is gated by gateNonBashTool in the dispatch loop.
func isBashTool(name string) bool {
	return name == "bash" || name == "bash_bg" || name == "shell_interactive"
}

// gateInteractiveCommand runs the on_tool_call chain for a shell_interactive
// command and resolves the verdict to the command string the front-end PTY
// runner should execute, or a block message for the model. The chain sees the
// real tool name, t.name == "shell_interactive". block / ask / {command=...}
// rewrite are honored; a runner-swap ({argv=...}) verdict has no interactive-PTY
// form, so it fails closed.
func gateInteractiveCommand(ctx context.Context, cfg TurnConfig, command, argsJSON string) (cmd, blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return command, "", false // no hooks: unsafe default
	}
	v := cfg.RunToolCall(ctx, "shell_interactive", command, argsJSON)
	switch v.Action {
	case Block:
		return "", "error: blocked by on_tool_call: " + v.Reason, true
	case Ask:
		if resolveAsk(ctx, cfg.Asker, v) {
			return interactiveCmdFromArgv(v.Argv, command)
		}
		return "", "error: blocked by on_tool_call — needs human approval (" + v.Reason +
			"). Stop and ask the human before running this.", true
	default: // Run
		return interactiveCmdFromArgv(v.Argv, command)
	}
}

// interactiveCmdFromArgv extracts the command string for the PTY runner from a
// verdict argv. A pass or {command=...} rewrite yields ["bash","-c",cmd]; any
// other (runner-swap) argv can't be handed to the interactive TTY runner, so it
// fails closed rather than running the command un-sandboxed.
func interactiveCmdFromArgv(argv []string, fallback string) (cmd, blockMsg string, blocked bool) {
	if len(argv) == 0 {
		return fallback, "", false
	}
	if len(argv) == 3 && argv[0] == "bash" && argv[1] == "-c" {
		return argv[2], "", false
	}
	return "", "error: blocked by on_tool_call: shell_interactive cannot run under a " +
		"runner-swap (argv) verdict — set shell_interactive = false for this agent, or " +
		"route the command through bash instead.", true
}

// gateNonBashTool runs the on_tool_call chain for a non-bash tool (read,
// list_files, edit_file, read_media, custom tools, …) before it dispatches.
// The chain sees the real t.name and a nil t.command (only bash tools carry a
// command), so handlers gate these by t.name / t.args. Only nil / block / ask are
// meaningful here: a {command=...} or {argv=...} verdict can't apply to a non-bash
// tool, so it fails closed. Returns a block message when the call must not run.
func gateNonBashTool(ctx context.Context, cfg TurnConfig, name, argsJSON string) (blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return "", false // no hooks: ungated
	}
	v := cfg.RunToolCall(ctx, name, "", argsJSON)
	switch v.Action {
	case Block:
		return "error: blocked by on_tool_call: " + v.Reason, true
	case Ask:
		if resolveAsk(ctx, cfg.Asker, v) {
			return "", false
		}
		return "error: blocked by on_tool_call — needs human approval (" + v.Reason +
			"). Stop and ask the human before running this.", true
	default: // Run
		// A pure pass (no handler produced a command/argv verdict) is the only Run
		// that applies to a non-bash tool. An actual {command=...}/{argv=...} verdict
		// — including a {command=""} rewrite, indistinguishable from a pass by argv
		// shape — is bash-only and fails closed here.
		if v.Passthrough {
			return "", false
		}
		return "error: blocked by on_tool_call: a {command=...} or {argv=...} verdict " +
			"applies only to bash tools, not " + name + ".", true
	}
}
