package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBashHandler_Name(t *testing.T) {
	h := BashHandler{}
	if h.Name() != "bash" {
		t.Fatalf("Name() = %q, want %q", h.Name(), "bash")
	}
}

func TestBashHandler_Execute_echo(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"echo hello"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("expected output to contain 'hello', got %q", out)
	}
}

func TestBashHandler_Execute_emptyOutput(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"true"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "(no output)" {
		t.Fatalf("expected '(no output)', got %q", out)
	}
}

func TestBashHandler_Execute_canceledContext(t *testing.T) {
	h := BashHandler{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := json.RawMessage(`{"command":"echo should not run"}`)
	out, _ := h.Execute(ctx, "1", args, ToolConfig{})
	// Should return error output or empty — must not block.
	_ = out
}

func TestBashHandler_Execute_timeout(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"sleep 5","timeout_seconds":1}`)
	start := time.Now()
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("timeout did not fire: elapsed %s", elapsed)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected 'timed out' in output, got %q", out)
	}
}

func TestBashHandler_Execute_nonzeroExit(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command":"echo oops && exit 1"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err) // Execute never returns an error — exit codes are in output
	}
	if !strings.Contains(out, "oops") {
		t.Fatalf("expected 'oops' in output, got %q", out)
	}
}
