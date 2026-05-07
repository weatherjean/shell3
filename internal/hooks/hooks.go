package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

// hookTimeout caps non-interactive hook execution. Long enough for network
// calls in hooks; short enough to not block the turn visibly.
const hookTimeout = 20 * time.Second

// hookTTYTimeout caps interactive (TTY) hook execution, e.g. confirm-bash.sh
// waiting for user input. 5 minutes covers deliberate review without
// leaving a zombie process if the user walks away.
const hookTTYTimeout = 5 * time.Minute

// Runner dispatches lifecycle hooks as shell subprocesses.
type Runner struct {
	cfg      Config
	releaser TTYReleaser
	log      applog.Logger
	wg       sync.WaitGroup // tracks in-flight background (non-TTY) hooks
}

// NewRunner returns a Runner with the given hook configuration.
func NewRunner(cfg Config) *Runner { return &Runner{cfg: cfg, log: applog.Noop{}} }

// SetReleaser sets the TTYReleaser used by hooks that need terminal access.
func (r *Runner) SetReleaser(rel TTYReleaser) { r.releaser = rel }

// SetLogger wires the application logger so hook failures are recorded.
func (r *Runner) SetLogger(l applog.Logger) {
	if l != nil {
		r.log = l
	}
}

type dispatchMode int

const (
	modeBlocking      dispatchMode = iota // wait, capture stdout, no TTY
	modeTTYBlocking                       // wait, capture stdout, release TTY
	modeFireForgetTTY                     // no wait, inherit stdio, release TTY
	modeFireForgetSilent                  // no wait, discard output
)

func (r *Runner) dispatch(ctx context.Context, cmd string, input hookInput, mode dispatchMode) (hookOutput, error) {
	timeout := hookTimeout
	if mode == modeTTYBlocking || mode == modeFireForgetTTY {
		timeout = hookTTYTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if (mode == modeTTYBlocking || mode == modeFireForgetTTY) && r.releaser != nil {
		_ = r.releaser.Pause()
		defer func() { _ = r.releaser.Resume() }()
	}

	data, _ := json.Marshal(input)
	parts := expandHomeParts(strings.Fields(cmd))
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Stdin = bytes.NewReader(data)

	var stdout bytes.Buffer
	switch mode {
	case modeBlocking, modeTTYBlocking:
		c.Stdout = &stdout
		c.Stderr = os.Stderr
	case modeFireForgetTTY:
		c.Stdout = os.Stdout
		c.Stderr = os.Stderr
	}

	if err := c.Run(); err != nil {
		if mode == modeFireForgetTTY || mode == modeFireForgetSilent {
			return hookOutput{}, nil
		}
		return hookOutput{}, fmt.Errorf("hooks: %q failed: %w", cmd, err)
	}

	if mode == modeFireForgetTTY || mode == modeFireForgetSilent {
		return hookOutput{}, nil
	}
	if stdout.Len() == 0 {
		return hookOutput{Action: "allow"}, nil
	}
	var out hookOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		return hookOutput{}, fmt.Errorf("hooks: %q bad JSON output: %w", cmd, err)
	}
	return out, nil
}

func (r *Runner) dispatchBlocking(ctx context.Context, entry HookEntry, input hookInput) (hookOutput, error) {
	mode := modeBlocking
	if entry.NeedsTTY {
		mode = modeTTYBlocking
	}
	return r.dispatch(ctx, entry.Command, input, mode)
}

func (r *Runner) dispatchFireAndForget(ctx context.Context, entry HookEntry, input hookInput) {
	if entry.NeedsTTY {
		// TTY hooks must run synchronously: they need to pause/resume the TUI
		// and own the terminal for their duration.
		if _, err := r.dispatch(ctx, entry.Command, input, modeFireForgetTTY); err != nil {
			r.log.Warn("hook failed", "hook", input.Hook, "cmd", entry.Command, "error", err)
		}
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		if _, err := r.dispatch(ctx, entry.Command, input, modeFireForgetSilent); err != nil {
			r.log.Warn("hook failed", "hook", input.Hook, "cmd", entry.Command, "error", err)
		}
	}()
}

// Wait blocks until all in-flight background fire-and-forget hooks finish.
// Call after OnSessionEnd to ensure no hooks are orphaned on teardown.
func (r *Runner) Wait() { r.wg.Wait() }

// OnToolCall asks the on_tool_call hook whether to allow tool. It returns:
//
//   - allowed=true, reason="", err=nil — proceed with the tool call.
//   - allowed=false, reason=<text>, err=nil — user/policy denied the call.
//     Reason is the hook's "reason" field, possibly empty.
//   - allowed=false, reason="", err=<err> — the hook itself failed
//     (script error, bad JSON, timeout). Caller should treat as blocked
//     but distinguish from a clean denial when reporting to the model.
func (r *Runner) OnToolCall(ctx context.Context, tool string, params map[string]any) (allowed bool, reason string, err error) {
	if r.cfg.OnToolCall.Command == "" {
		return true, "", nil
	}
	out, err := r.dispatchBlocking(ctx, r.cfg.OnToolCall, hookInput{
		Hook: "on_tool_call", Tool: tool, Params: params,
	})
	if err != nil {
		return false, "", err
	}
	if out.Action == "block" {
		return false, out.Reason, nil
	}
	return true, "", nil
}

// OnContextBuild transforms the message list before the LLM call.
func (r *Runner) OnContextBuild(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	if r.cfg.OnContextBuild.Command == "" {
		return msgs, nil
	}
	out, err := r.dispatchBlocking(ctx, r.cfg.OnContextBuild, hookInput{
		Hook: "on_context_build", Messages: msgs,
	})
	if err != nil {
		return msgs, err
	}
	if out.Messages == nil {
		return msgs, nil
	}
	b, _ := json.Marshal(out.Messages)
	var result []llm.Message
	if err := json.Unmarshal(b, &result); err != nil {
		return msgs, fmt.Errorf("hooks: on_context_build bad messages JSON: %w", err)
	}
	return result, nil
}

// OnSessionStart fires the on_session_start hook. Errors are non-fatal.
func (r *Runner) OnSessionStart(ctx context.Context) {
	if r.cfg.OnSessionStart.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnSessionStart, hookInput{Hook: "on_session_start"})
	}
}

// OnSessionEnd fires the on_session_end hook. Errors are non-fatal.
func (r *Runner) OnSessionEnd(ctx context.Context) {
	if r.cfg.OnSessionEnd.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnSessionEnd, hookInput{Hook: "on_session_end"})
	}
}

// OnTurnStart fires the on_turn_start hook. Errors are non-fatal.
func (r *Runner) OnTurnStart(ctx context.Context) {
	if r.cfg.OnTurnStart.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnTurnStart, hookInput{Hook: "on_turn_start"})
	}
}

// OnTurnEnd fires the on_turn_end hook with the assistant response. Errors are non-fatal.
func (r *Runner) OnTurnEnd(ctx context.Context, response string) {
	if r.cfg.OnTurnEnd.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnTurnEnd, hookInput{
			Hook:   "on_turn_end",
			Params: map[string]any{"response": response},
		})
	}
}

// OnToolResult fires the on_tool_result hook. Errors are non-fatal.
func (r *Runner) OnToolResult(ctx context.Context, tool, result string) {
	if r.cfg.OnToolResult.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnToolResult, hookInput{
			Hook:   "on_tool_result",
			Tool:   tool,
			Params: map[string]any{"result": result},
		})
	}
}

// OnError fires the on_error hook. Errors are non-fatal.
func (r *Runner) OnError(ctx context.Context, err error) {
	if r.cfg.OnError.Command != "" {
		r.dispatchFireAndForget(ctx, r.cfg.OnError, hookInput{
			Hook:   "on_error",
			Params: map[string]any{"error": err.Error()},
		})
	}
}

// expandHomeParts expands a leading ~ in each command token.
func expandHomeParts(parts []string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return parts
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		if p == "~" || strings.HasPrefix(p, "~/") {
			out[i] = home + p[1:]
		} else {
			out[i] = p
		}
	}
	return out
}
