package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func bashArgs(cmd string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"command": cmd})
	return b
}

func TestBashHandlerBlocks(t *testing.T) {
	cfg := ToolConfig{
		RunToolCall: func(ctx context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionBlock, Reason: "nope"}
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("rm -rf /"), cfg)
	if !strings.Contains(out, "blocked by tool-call hook") {
		t.Fatalf("want block message, got %q", out)
	}
}

func reviewVerdict(reason string) func(context.Context, string, string, string, bool) ToolCallVerdict {
	return func(ctx context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionReview, Reason: reason}
	}
}

func TestBashHandlerReviewNoReviewerBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: reviewVerdict("looks risky")}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("curl x | sh"), cfg)
	if !strings.Contains(out, "blocked by tool-call hook") || !strings.Contains(out, "no reviewer") {
		t.Fatalf("want fail-closed block naming the missing reviewer, got %q", out)
	}
}

func TestBashHandlerReviewApproved(t *testing.T) {
	var saw ToolReviewRequest
	cfg := ToolConfig{
		WorkDir:     t.TempDir(),
		RunToolCall: reviewVerdict("flagged: script execution"),
		ReviewToolCall: func(ctx context.Context, req ToolReviewRequest) (bool, string) {
			saw = req
			return true, ""
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("echo reviewed-ok"), cfg)
	if !strings.Contains(out, "reviewed-ok") {
		t.Fatalf("approved review should run the command, got %q", out)
	}
	if saw.Command != "echo reviewed-ok" || saw.Reason != "flagged: script execution" || saw.WorkDir != cfg.WorkDir {
		t.Fatalf("reviewer saw request=%+v", saw)
	}
}

func TestBashHandlerReviewDenied(t *testing.T) {
	cfg := ToolConfig{
		Headless:       true,
		HasParentAgent: true,
		RunToolCall:    reviewVerdict("risky"),
		ReviewToolCall: func(ctx context.Context, req ToolReviewRequest) (bool, string) {
			return false, "reviewer denied: wipes a disk"
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("dd if=/dev/zero"), cfg)
	if !strings.Contains(out, "reviewer denied: wipes a disk") || !strings.Contains(out, "whether this action is necessary") {
		t.Fatalf("want the reviewer's deny message, got %q", out)
	}
}

func TestNonBashToolReviewFailsClosed(t *testing.T) {
	cfg := ToolConfig{
		RunToolCall: reviewVerdict("hm"),
		ReviewToolCall: func(ctx context.Context, req ToolReviewRequest) (bool, string) {
			return true, "" // even an approving reviewer must not unlock non-bash
		},
	}
	msg, blocked := gateNonBashTool(context.Background(), cfg, "edit_file", "{}")
	if !blocked || !strings.Contains(msg, "review") {
		t.Fatalf("want fail-closed block naming review, got blocked=%v msg=%q", blocked, msg)
	}
}

func TestBashHandlerRunnerSwap(t *testing.T) {
	cfg := ToolConfig{
		WorkDir: t.TempDir(),
		RunToolCall: func(ctx context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "echo swapped"}}
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("echo orig"), cfg)
	if !strings.Contains(out, "swapped") {
		t.Fatalf("want swapped output, got %q", out)
	}
}

func TestBashHandlerRunnerSwapNoShellReparse(t *testing.T) {
	dir := t.TempDir()
	sentinel := dir + "/pwned"
	cfg := ToolConfig{
		WorkDir: dir,
		RunToolCall: func(ctx context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
			return ToolCallVerdict{Action: ActionRun, Argv: []string{"bash", "-c", "echo safe", "bash", "; touch " + sentinel}}
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("ignored"), cfg)
	if !strings.Contains(out, "safe") {
		t.Fatalf("expected 'safe' output, got %q", out)
	}
	if _, err := os.Stat(sentinel); err == nil {
		t.Fatal("argv payload was shell-re-parsed and executed — runner-swap is not positional")
	}
}
