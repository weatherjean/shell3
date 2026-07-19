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
	out, err := h.Execute(ctx, "1", args, ToolConfig{})
	if err != nil {
		t.Fatal(err)
	}
	// The command must not have run: a pre-cancelled context kills it before
	// exec, so the output carries the error marker, never the echo text.
	if strings.Contains(out, "should not run") {
		t.Fatalf("command ran despite cancelled context: %q", out)
	}
	if !strings.HasPrefix(out, "error:") {
		t.Fatalf("expected error output, got %q", out)
	}
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

// A foreground bash call blocks the whole turn, so the requested timeout must
// clamp to MaxBashTimeoutSeconds — the model cannot buy back the old 10-minute
// wedge by passing a huge timeout_seconds.
func TestParseBashArgsClampsTimeout(t *testing.T) {
	_, timeout, err := parseBashArgsFull(`{"command":"sleep 1","timeout_seconds":600}`)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Duration(MaxBashTimeoutSeconds) * time.Second; timeout != want {
		t.Fatalf("timeout not clamped: got %s, want %s", timeout, want)
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
	// A quiet failure must be distinguishable from success: the exit code is
	// surfaced as an error: prefix line (which also flips the tool_result
	// error flag via classifyHandlerOutput).
	if !strings.HasPrefix(out, "error: command exited 1") {
		t.Fatalf("expected exit-code marker, got %q", out)
	}
}

// A malformed args blob must never fall back to executing the raw JSON as the
// shell command (e.g. {"command": 5} passes schema presence checks but fails
// unmarshal).
func TestBashHandler_Execute_malformedArgs(t *testing.T) {
	h := BashHandler{}
	args := json.RawMessage(`{"command": 5}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "error: invalid bash arguments") {
		t.Fatalf("expected invalid-arguments error, got %q", out)
	}
}
