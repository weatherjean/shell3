package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// Grandchild that inherits stdout would hang Run() forever without WaitDelay
// because the bytes.Buffer copy goroutine waits on a pipe the dead bash
// child no longer holds but the surviving grandchild still does.
func TestBashHandler_Execute_timeoutWithGrandchild(t *testing.T) {
	h := BashHandler{}
	// Spawn a backgrounded grandchild that keeps stdout open for 30s.
	// Parent bash exits immediately on the outer `sleep 5` timeout.
	args := json.RawMessage(`{"command":"bash -c 'sleep 30 & echo started; sleep 5'","timeout_seconds":1}`)
	start := time.Now()
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	// Timeout (1s) + WaitDelay (2s) + a bit of slack.
	if elapsed > 5*time.Second {
		t.Fatalf("did not return promptly despite grandchild: elapsed %s", elapsed)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected 'timed out' in output, got %q", out)
	}
}

func TestBashHandler_Execute_outputTruncation(t *testing.T) {
	h := BashHandler{}
	// Emit ~60KB; cap is 30KB. Use yes piped to head for speed.
	cmd := fmt.Sprintf(`yes a | head -c %d`, MaxBashOutputBytes*2)
	args := json.RawMessage(fmt.Sprintf(`{"command":%q,"timeout_seconds":5}`, cmd))
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > MaxBashOutputBytes+200 {
		t.Fatalf("output not truncated: len=%d cap=%d", len(out), MaxBashOutputBytes)
	}
	if !strings.Contains(out, "bytes elided") {
		t.Fatalf("expected elided marker, got len=%d", len(out))
	}
}

func TestElideMiddle(t *testing.T) {
	short := []byte("hello")
	if got := elideMiddle(short, 100); got != "hello" {
		t.Fatalf("short pass-through failed: %q", got)
	}
	long := bytes.Repeat([]byte("x"), 1000)
	got := elideMiddle(long, 100)
	if !strings.Contains(got, "bytes elided") {
		t.Fatalf("missing marker: %q", got)
	}
	if len(got) > 250 {
		t.Fatalf("elided output too large: %d", len(got))
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
