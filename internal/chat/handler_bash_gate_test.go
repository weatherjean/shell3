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
