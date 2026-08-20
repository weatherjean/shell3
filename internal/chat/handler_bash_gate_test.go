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

// reviewVerdict is the gate stub every review test shares.
func reviewVerdict(reason string) func(context.Context, string, string, string, bool) ToolCallVerdict {
	return func(ctx context.Context, name, command, argsJSON string, _ bool) ToolCallVerdict {
		return ToolCallVerdict{Action: ActionReview, Reason: reason}
	}
}

// A review verdict with no reviewer wired fails closed: soft deny becomes a
// hard block rather than silently running.
func TestBashHandlerReviewNoReviewerBlocks(t *testing.T) {
	cfg := ToolConfig{RunToolCall: reviewVerdict("looks risky")}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("curl x | sh"), cfg)
	if !strings.Contains(out, "blocked by tool-call hook") || !strings.Contains(out, "no reviewer") {
		t.Fatalf("want fail-closed block naming the missing reviewer, got %q", out)
	}
}

// Reviewer approves: the ORIGINAL command runs unchanged.
func TestBashHandlerReviewApproved(t *testing.T) {
	var sawCmd, sawReason string
	cfg := ToolConfig{
		WorkDir:     t.TempDir(),
		RunToolCall: reviewVerdict("flagged: script execution"),
		ReviewToolCall: func(ctx context.Context, name, command, reason string) (bool, string) {
			sawCmd, sawReason = command, reason
			return true, ""
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("echo reviewed-ok"), cfg)
	if !strings.Contains(out, "reviewed-ok") {
		t.Fatalf("approved review should run the command, got %q", out)
	}
	if sawCmd != "echo reviewed-ok" || sawReason != "flagged: script execution" {
		t.Fatalf("reviewer saw cmd=%q reason=%q", sawCmd, sawReason)
	}
}

// Reviewer denies: blocked, deny message surfaces to the model.
func TestBashHandlerReviewDenied(t *testing.T) {
	cfg := ToolConfig{
		RunToolCall: reviewVerdict("risky"),
		ReviewToolCall: func(ctx context.Context, name, command, reason string) (bool, string) {
			return false, "reviewer denied: wipes a disk"
		},
	}
	out, _ := BashHandler{}.Execute(context.Background(), "1", bashArgs("dd if=/dev/zero"), cfg)
	if !strings.Contains(out, "reviewer denied: wipes a disk") {
		t.Fatalf("want the reviewer's deny message, got %q", out)
	}
}

// Review is bash-only in v1: a review verdict on a non-bash tool fails
// closed with a named reason, like command/argv verdicts do.
func TestNonBashToolReviewFailsClosed(t *testing.T) {
	cfg := ToolConfig{
		RunToolCall: reviewVerdict("hm"),
		ReviewToolCall: func(ctx context.Context, name, command, reason string) (bool, string) {
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
			// argv: bash -c 'echo safe' bash '; touch <sentinel>'
			// $0=bash, $1="; touch <sentinel>" — $1 must NOT be executed.
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
