package chat

import (
	"context"
	"strings"
	"testing"
)

func TestGateNonBashToolBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, name, command, _ string, _ bool) ToolCallVerdict {
		if name != "edit_file" {
			t.Errorf("want real name read, got %q", name)
		}
		if command != "" {
			t.Errorf("non-bash command should be empty, got %q", command)
		}
		return ToolCallVerdict{Action: ActionBlock, Reason: "no reading .env"}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", `{"path":".env"}`)
	if !blocked || !strings.Contains(msg, "no reading .env") {
		t.Fatalf("want blocked, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestSubagentBlockRequiresNecessityTriageBeforeEscalation(t *testing.T) {
	cfg := ToolConfig{
		Headless:       true,
		HasParentAgent: true,
		RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionBlock, Reason: "publishing is not authorized"}
		},
	}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked {
		t.Fatal("subagent block unexpectedly allowed")
	}
	for _, want := range []string{
		"whether this action is necessary",
		"materially safer, policy-compliant approach",
		"never reinterpret a blanket refusal",
		"Complete any useful partial work",
		"Only if this block prevents meaningful completion",
		"report to the parent agent",
		"exact blocked action",
		"alternatives considered",
		"decision the operator must make",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("subagent guidance missing %q:\n%s", want, msg)
		}
	}
}

func TestInteractiveBlockOmitsHeadlessTriage(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionBlock, Reason: "blocked"}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || strings.Contains(msg, "Subagent triage") {
		t.Fatalf("interactive block got subagent guidance: blocked=%v msg=%q", blocked, msg)
	}
}

func TestOwnerlessHeadlessBlockOmitsParentTriage(t *testing.T) {
	cfg := ToolConfig{
		Headless: true,
		RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionBlock, Reason: "blocked"}
		},
	}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || strings.Contains(msg, "report to the parent agent") {
		t.Fatalf("ownerless headless block got parent guidance: blocked=%v msg=%q", blocked, msg)
	}
}

func TestGateNonBashToolPasses(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", ""}, Passthrough: true}
	}}
	if msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}"); blocked {
		t.Fatalf("pure-pass verdict should pass, got blocked msg=%q", msg)
	}
}

func TestGateNonBashToolEmptyRewriteFailsClosed(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", ""}, Passthrough: false}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || !strings.Contains(msg, "only to bash tools") {
		t.Fatalf("empty rewrite on non-bash must fail closed, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestGateNonBashToolNoHooksPasses(t *testing.T) {
	if _, blocked := gateNonBashTool(context.Background(), ToolConfig{}, "edit_file", "{}"); blocked {
		t.Fatal("no hooks: read must not be gated")
	}
}

func TestGateNonBashToolRewriteFailsClosed(t *testing.T) {
	cfg := ToolConfig{RunToolCall: func(_ context.Context, _, _, _ string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "rewritten"}}
	}}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || !strings.Contains(msg, "only to bash tools") {
		t.Fatalf("rewrite on non-bash must fail closed, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestGatesForwardHeadless(t *testing.T) {
	for _, headless := range []bool{true, false} {
		var got *bool
		cfg := ToolConfig{
			Headless: headless,
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
		gateNonBashTool(ctx, cfg, "edit_file", "{}")
		if got == nil || *got != headless {
			t.Fatalf("gateNonBashTool headless=%v: chain saw %v", headless, got)
		}
	}
}
