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

// A {command=...} rewrite is honored — the PTY runs the rewritten command.

// A runner-swap (argv) verdict can't run through the interactive PTY, so it
// fails closed rather than silently running un-sandboxed.

// Ask with no human attached denies (and blocks).

// Ask allowed by the human runs exactly what was approved.

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
		gateNonBashTool(ctx, cfg, "read", "{}")
		if got == nil || *got != headless {
			t.Fatalf("gateNonBashTool headless=%v: chain saw %v", headless, got)
		}
	}
}
