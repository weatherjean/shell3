package chat

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestResolveAskNoAskerDenies(t *testing.T) {
	if resolveAsk(context.Background(), nil, ToolCallVerdict{Action: ActionAsk, Prompt: "x"}) {
		t.Fatal("no asker must deny")
	}
}

func TestResolveAskAllows(t *testing.T) {
	asker := AskFunc(func(ctx context.Context, cmd, reason string) bool { return true })
	if !resolveAsk(context.Background(), asker, ToolCallVerdict{Action: ActionAsk, Prompt: "x", AskTimeout: time.Second}) {
		t.Fatal("asker returning true must allow")
	}
}

// shell_interactive is gated through the on_tool_call chain under its real name,
// so a denylist block applies to it too.
func TestGateInteractiveBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
		if name != "shell_interactive" {
			t.Errorf("shell_interactive should gate under its real name, got %q", name)
		}
		return ToolCallVerdict{Action: ActionBlock, Reason: "no rm"}
	}}
	_, msg, blocked := gateInteractiveCommand(context.Background(), cfg, "rm -rf /", "{}")
	if !blocked || !strings.Contains(msg, "blocked by on_tool_call") {
		t.Fatalf("want blocked, got blocked=%v msg=%q", blocked, msg)
	}
}

// Non-bash tools fire on_tool_call too, under their real name with a nil command,
// and a block verdict stops them.
func TestGateNonBashToolBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, name, command, _ string, _ bool) ToolCallVerdict {
		if name != "read" {
			t.Errorf("want real name read, got %q", name)
		}
		if command != "" {
			t.Errorf("non-bash command should be empty, got %q", command)
		}
		return ToolCallVerdict{Action: ActionBlock, Reason: "no reading .env"}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "read", `{"path":".env"}`)
	if !blocked || !strings.Contains(msg, "no reading .env") {
		t.Fatalf("want blocked, got blocked=%v msg=%q", blocked, msg)
	}
}

// A pure pass (no handler produced a command/argv verdict) for a non-bash tool
// passes it through.
func TestGateNonBashToolPasses(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", ""}, Passthrough: true}
	}}
	if msg, blocked := gateNonBashTool(context.Background(), cfg, "read", "{}"); blocked {
		t.Fatalf("pure-pass verdict should pass, got blocked msg=%q", msg)
	}
}

// A {command=""} rewrite produces the same empty argv as a pass, but it is a
// command verdict, not a pass — so a non-bash tool must fail closed. This is the
// case a byte-shape check on the argv would wrongly let through; Passthrough
// (false here) is what distinguishes it.
func TestGateNonBashToolEmptyRewriteFailsClosed(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", ""}, Passthrough: false}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "read", "{}")
	if !blocked || !strings.Contains(msg, "only to bash tools") {
		t.Fatalf("empty rewrite on non-bash must fail closed, got blocked=%v msg=%q", blocked, msg)
	}
}

// No hooks declared ⇒ non-bash tools are ungated.
func TestGateNonBashToolNoHooksPasses(t *testing.T) {
	if _, blocked := gateNonBashTool(context.Background(), ToolConfig{}, "read", "{}"); blocked {
		t.Fatal("no hooks: read must not be gated")
	}
}

// A {command=...}/{argv=...} verdict makes no sense for a non-bash tool, so it
// fails closed rather than silently no-op'ing.
func TestGateNonBashToolRewriteFailsClosed(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "rewritten"}}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || !strings.Contains(msg, "only to bash tools") {
		t.Fatalf("rewrite on non-bash must fail closed, got blocked=%v msg=%q", blocked, msg)
	}
}

// Ask with no human attached denies (blocks) a non-bash tool.
func TestGateNonBashToolAskNoAskerBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionAsk, Prompt: "ok?", Reason: "confirm"}
	}}
	if _, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}"); !blocked {
		t.Fatal("ask with no asker must block")
	}
}

// No hooks declared → unsafe default: the command runs verbatim.
func TestGateInteractiveNoHooksRuns(t *testing.T) {
	cmd, _, blocked := gateInteractiveCommand(context.Background(), ToolConfig{}, "vim", "{}")
	if blocked || cmd != "vim" {
		t.Fatalf("no hooks: want run vim, got blocked=%v cmd=%q", blocked, cmd)
	}
}

// A {command=...} rewrite is honored — the PTY runs the rewritten command.
func TestGateInteractiveRewriteRuns(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, command, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "safe " + command}}
	}}
	cmd, _, blocked := gateInteractiveCommand(context.Background(), cfg, "top", "{}")
	if blocked || cmd != "safe top" {
		t.Fatalf("want rewritten 'safe top', got blocked=%v cmd=%q", blocked, cmd)
	}
}

// A runner-swap (argv) verdict can't run through the interactive PTY, so it
// fails closed rather than silently running un-sandboxed.
func TestGateInteractiveRunnerSwapFailsClosed(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, command, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"docker", "exec", "c", "bash", "-c", command}}
	}}
	_, msg, blocked := gateInteractiveCommand(context.Background(), cfg, "top", "{}")
	if !blocked || !strings.Contains(msg, "runner-swap") {
		t.Fatalf("runner-swap must fail closed for interactive, got blocked=%v msg=%q", blocked, msg)
	}
}

// Ask with no human attached denies (and blocks).
func TestGateInteractiveAskNoAskerBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, command, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionAsk, Prompt: "ok?", Reason: "confirm", Argv: []string{"bash", "-c", command}}
	}}
	_, msg, blocked := gateInteractiveCommand(context.Background(), cfg, "git push", "{}")
	if !blocked || !strings.Contains(msg, "human approval") {
		t.Fatalf("ask with no asker must block, got blocked=%v msg=%q", blocked, msg)
	}
}

// Ask allowed by the human runs exactly what was approved.
func TestGateInteractiveAskAllowRuns(t *testing.T) {
	cfg := ToolConfig{
		RunToolCall: func(_ context.Context, _, command, _ string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionAsk, Prompt: "ok?", Argv: []string{"bash", "-c", command}}
		},
		Asker: func(context.Context, string, string) bool { return true },
	}
	cmd, _, blocked := gateInteractiveCommand(context.Background(), cfg, "git push", "{}")
	if blocked || cmd != "git push" {
		t.Fatalf("ask allow must run, got blocked=%v cmd=%q", blocked, cmd)
	}
}

// TestGatesForwardHeadlessAsk: each gate site passes cfg.HeadlessAsk into the
// on_tool_call chain unmodified.
func TestGatesForwardHeadlessAsk(t *testing.T) {
	for _, headless := range []bool{true, false} {
		var got *bool
		cfg := ToolConfig{
			HeadlessAsk: headless,
			RunToolCall: func(_ context.Context, _, _, _ string, h bool) ToolCallVerdict {
				got = &h
				return ToolCallVerdict{Action: ActionRun, Passthrough: true}
			},
		}
		ctx := context.Background()

		got = nil
		gateBash(ctx, cfg, "bash", "echo hi", "{}")
		if got == nil || *got != headless {
			t.Fatalf("gateBash headless=%v: chain saw %v", headless, got)
		}

		got = nil
		gateInteractiveCommand(ctx, cfg, "echo hi", "{}")
		if got == nil || *got != headless {
			t.Fatalf("gateInteractiveCommand headless=%v: chain saw %v", headless, got)
		}

		got = nil
		gateNonBashTool(ctx, cfg, "read", "{}")
		if got == nil || *got != headless {
			t.Fatalf("gateNonBashTool headless=%v: chain saw %v", headless, got)
		}
	}
}
