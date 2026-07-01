//go:build unix

package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	argv, blockMsg, blocked := gateBash(ctx, cfg, "bash", command, string(args))
	if blocked {
		return blockMsg, nil
	}
	out, _ := runBashCapture(ctx, argv, cfg.WorkDir, nil, timeout)
	return out, nil
}

// gateBash runs the on_tool_call chain for a bash/bash_bg command and resolves
// the verdict to either an argv to exec or a block message for the model. name is
// the real tool name ("bash" or "bash_bg") so the handler sees the exact tool.
func gateBash(ctx context.Context, cfg ToolConfig, name, command, argsJSON string) (argv []string, blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return []string{"bash", "-c", command}, "", false // no hooks: unsafe default
	}
	v := cfg.RunToolCall(ctx, name, command, argsJSON)
	switch v.Action {
	case Block:
		return nil, "error: blocked by on_tool_call: " + v.Reason, true
	case Ask:
		if resolveAsk(ctx, cfg.Asker, v) {
			if len(v.Argv) > 0 {
				return v.Argv, "", false // run exactly what was approved (carries any rewrite)
			}
			return []string{"bash", "-c", command}, "", false
		}
		return nil, "error: blocked by on_tool_call — needs human approval (" + v.Reason +
			"). Stop and ask the human before running this.", true
	default: // Run
		return v.Argv, "", false
	}
}

// runBashCapture runs argv (argv[0] with argv[1:] as args) in workdir with
// extraEnv appended to os.Environ() (nil = inherit only), capturing combined
// stdout+stderr, honoring timeout + cancellation. It returns the elided output
// and the process exit code (124 on timeout, -1 on a start error). Shared by the
// bash tool and foreground command-template tools. argv must be non-empty.
func runBashCapture(ctx context.Context, argv []string, workdir string, extraEnv []string, timeout time.Duration) (string, int) {
	if len(argv) == 0 {
		return "error: empty command argv\n", -1
	}
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(tctx, argv[0], argv[1:]...)
	c.Dir = workdir
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
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
	exit := 0
	err := c.Run()
	if err != nil {
		switch {
		case errors.Is(tctx.Err(), context.DeadlineExceeded):
			exit = 124
			fmt.Fprintf(&buf, "\nerror: command timed out after %s (set timeout_seconds to extend, max %ds)\n", timeout, MaxBashTimeoutSeconds)
		default:
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else {
				exit = -1
				if buf.Len() == 0 {
					fmt.Fprintf(&buf, "error: %v\n", err)
				}
			}
		}
	}
	if buf.Len() == 0 {
		return "(no output)", exit
	}
	return elideMiddle(buf.Bytes(), MaxBashOutputBytes), exit
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

// ParseBashArgs extracts the "command" field from bash tool JSON args.
// Takes string (not json.RawMessage) so it can be called from turn.go
// where tc.RawArgs is a string without a type conversion. Exported so
// the TUI render layer can format bash headers identically.
func ParseBashArgs(raw string) string {
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return raw
	}
	return args.Command
}
