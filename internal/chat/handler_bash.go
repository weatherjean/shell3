package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// DefaultBashTimeoutSeconds caps bash tool runtime when caller does not set timeout_seconds.
const DefaultBashTimeoutSeconds = 10

// MaxBashTimeoutSeconds caps the upper bound the model can request.
const MaxBashTimeoutSeconds = 600

// MaxBashOutputBytes caps captured stdout+stderr. Beyond this the middle is
// elided so the model sees the head and tail of long outputs.
const MaxBashOutputBytes = 30 * 1024

// bashWaitDelay bounds how long c.Wait blocks on stdio pipes after the
// process is killed. Grandchildren that inherit our fds would otherwise
// hold the buffer copy goroutines open forever.
const bashWaitDelay = 2 * time.Second

// BashHandler executes a bash command and returns its combined stdout+stderr.
// It respects context cancellation — callers set timeouts before invoking Execute.
// Exit codes are not returned as errors; non-zero exit appends the error to output.
type BashHandler struct{}

func (BashHandler) Name() string { return "bash" }

func (BashHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	command, timeout := parseBashArgsFull(string(args))
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(tctx, "bash", "-c", command)
	c.Dir = cfg.WorkDir
	// Put bash and its descendants in their own process group so we can
	// signal the whole tree on cancel/timeout — bare SIGKILL on bash
	// leaves grandchildren (e.g. node spawned by npx) holding our pipes.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
	c.WaitDelay = bashWaitDelay
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	if err != nil && errors.Is(tctx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(&buf, "\nerror: command timed out after %s (set timeout_seconds to extend, max %ds)\n", timeout, MaxBashTimeoutSeconds)
	} else if err != nil && buf.Len() == 0 {
		fmt.Fprintf(&buf, "error: %v\n", err)
	}
	if buf.Len() == 0 {
		return "(no output)", nil
	}
	return elideMiddle(buf.Bytes(), MaxBashOutputBytes), nil
}

// elideMiddle returns out unchanged if within max, otherwise keeps the
// first and last half and elides the middle with a marker line.
func elideMiddle(out []byte, max int) string {
	if len(out) <= max {
		return string(out)
	}
	half := max / 2
	head := out[:half]
	tail := out[len(out)-half:]
	elided := len(out) - 2*half
	return fmt.Sprintf("%s\n... [%d bytes elided] ...\n%s", head, elided, tail)
}

// parseBashArgsFull extracts command and timeout. Timeout defaults to
// DefaultBashTimeoutSeconds and is clamped to [1, MaxBashTimeoutSeconds].
func parseBashArgsFull(raw string) (string, time.Duration) {
	var args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw, time.Duration(DefaultBashTimeoutSeconds) * time.Second
	}
	t := args.TimeoutSeconds
	if t <= 0 {
		t = DefaultBashTimeoutSeconds
	}
	if t > MaxBashTimeoutSeconds {
		t = MaxBashTimeoutSeconds
	}
	return args.Command, time.Duration(t) * time.Second
}

// parseBashArgs extracts the "command" field from bash tool JSON args.
// Takes string (not json.RawMessage) so it can be called from turn.go
// where tc.RawArgs is a string without a type conversion.
func parseBashArgs(raw string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw
	}
	return args.Command
}
