package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/weatherjean/shell3/internal/procutil"
	"github.com/weatherjean/shell3/internal/strutil"
)

// defaultBashTimeoutSeconds caps bash tool runtime when caller does not set timeout_seconds.
const defaultBashTimeoutSeconds = 10

// maxBashTimeoutSeconds caps the upper bound the model can request. Kept
// deliberately low: a foreground bash call blocks the whole turn — the agent
// cannot answer the user until it returns — so anything slower belongs in
// bash_bg (which saves its result to the durable inbox). The old
// 600s cap let a single call wedge the bot for 10 minutes.
const maxBashTimeoutSeconds = 120

// maxBashOutputBytes caps captured stdout+stderr. Beyond this the middle is
// elided so the model sees the head and tail of long outputs.
const maxBashOutputBytes = 30 * 1024

// bashWaitDelay bounds how long c.Wait blocks on stdio pipes after the
// process is killed. Grandchildren that inherit our fds would otherwise
// hold the buffer copy goroutines open forever.
const bashWaitDelay = 2 * time.Second

// BashHandler executes a command and returns its combined output and exit error.
type BashHandler struct{}

func (BashHandler) Name() string { return "bash" }

func (BashHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	command, timeout, err := parseBashArgsFull(string(args))
	if err != nil {
		return "", fmt.Errorf("invalid bash arguments: %w", err)
	}
	argv := []string{"bash", "-c", command}
	out, code := runBashCapture(ctx, argv, cfg.WorkDir, nil, timeout)
	if code != 0 {
		return out, fmt.Errorf("command exited %d", code)
	}
	return out, nil
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
	procutil.ConfigureGroupCancel(c, bashWaitDelay)
	buf := strutil.Capture{Limit: maxBashOutputBytes}
	c.Stdout = &buf
	c.Stderr = &buf
	exit := 0
	err := c.Run()
	if err != nil {
		switch {
		case errors.Is(tctx.Err(), context.DeadlineExceeded):
			exit = 124
			fmt.Fprintf(&buf, "\nerror: command timed out after %s (set timeout_seconds to extend, max %ds; for anything slower use bash_bg — it saves the result to the inbox)\n", timeout, maxBashTimeoutSeconds)
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
	return buf.String(), exit
}

// parseBashArgsFull extracts command and timeout. Timeout defaults to
// defaultBashTimeoutSeconds and is clamped to [1, maxBashTimeoutSeconds].
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
		t = defaultBashTimeoutSeconds
	}
	if t > maxBashTimeoutSeconds {
		t = maxBashTimeoutSeconds
	}
	return args.Command, time.Duration(t) * time.Second, nil
}
