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
// Exit codes are not returned as errors; a non-zero exit prefixes the output
// with an "error: command exited N" line (one convention shared by every dispatch path), so
// the model — and the tool_result error flag — can tell the call failed even
// when the command wrote nothing to stderr.
type BashHandler struct{}

func (BashHandler) Name() string { return "bash" }

func (BashHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	command, timeout, err := parseBashArgsFull(string(args))
	if err != nil {
		return "error: invalid bash arguments: " + err.Error(), nil
	}
	argv, blockMsg, blocked := gateBash(ctx, cfg, "bash", command, string(args))
	if blocked {
		return blockMsg, nil
	}
	out, code := runBashCapture(ctx, argv, cfg.WorkDir, nil, timeout)
	if code != 0 {
		return fmt.Sprintf("error: command exited %d\n%s", code, out), nil
	}
	return out, nil
}

// gateBash runs the on_tool_call chain for a bash/bash_bg command and resolves
// the verdict to either an argv to exec or a block message for the model. name is
// the real tool name ("bash" or "bash_bg") so the handler sees the exact tool.
// On allow, the verdict argv runs exactly as approved (it carries any rewrite
// or runner-swap); an empty argv — a pure pass — defaults to bash -c command.
func gateBash(ctx context.Context, cfg ToolConfig, name, command, argsJSON string) (argv []string, blockMsg string, blocked bool) {
	if cfg.RunToolCall == nil {
		return []string{"bash", "-c", command}, "", false // no hooks: unsafe default
	}
	v := cfg.RunToolCall(ctx, name, command, argsJSON, cfg.HeadlessAsk)
	allowed, msg := resolveGate(ctx, cfg.Asker, v)
	if !allowed {
		return nil, msg, true
	}
	if len(v.Argv) > 0 {
		return v.Argv, "", false
	}
	return []string{"bash", "-c", command}, "", false
}

// ConfigureGroupKill puts cmd and its descendants in their own process group
// and signals the whole tree (SIGTERM to the group) on cancel — bare SIGKILL
// on the shell leaves grandchildren (e.g. node spawned by npx, a server
// started with `&`) holding our stdio pipes. waitDelay bounds how long Wait
// blocks on those pipes after the process exits, so a lingering grandchild
// can't wedge the caller forever. Shared by the foreground bash tool and the
// background job runtime (internal/shell3).
func ConfigureGroupKill(cmd *exec.Cmd, waitDelay time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay
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
	ConfigureGroupKill(c, bashWaitDelay)
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
// Malformed args return an error — execution paths must never fall back to
// running the raw JSON blob as a shell command.
func parseBashArgsFull(raw string) (string, time.Duration, error) {
	var args struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return "", 0, err
	}
	t := args.TimeoutSeconds
	if t <= 0 {
		t = DefaultBashTimeoutSeconds
	}
	if t > MaxBashTimeoutSeconds {
		t = MaxBashTimeoutSeconds
	}
	return args.Command, time.Duration(t) * time.Second, nil
}
