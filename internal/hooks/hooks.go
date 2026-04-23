package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

const hookTimeout = 5 * time.Second

// Runner dispatches lifecycle hooks as shell subprocesses.
type Runner struct{ cfg Config }

// NewRunner returns a Runner with the given hook configuration.
func NewRunner(cfg Config) *Runner { return &Runner{cfg} }

func (r *Runner) callHook(ctx context.Context, cmd string, input hookInput) (hookOutput, error) {
	ctx, cancel := context.WithTimeout(ctx, hookTimeout)
	defer cancel()

	data, _ := json.Marshal(input)
	parts := strings.Fields(cmd)
	c := exec.CommandContext(ctx, parts[0], parts[1:]...)
	c.Stdin = bytes.NewReader(data)

	var stdout bytes.Buffer
	c.Stdout = &stdout

	if err := c.Run(); err != nil {
		return hookOutput{}, fmt.Errorf("hooks: %q failed: %w", cmd, err)
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

// OnToolCall returns true if the tool call is allowed by the hook.
func (r *Runner) OnToolCall(ctx context.Context, tool string, params map[string]any) (bool, error) {
	if r.cfg.OnToolCall == "" {
		return true, nil
	}
	out, err := r.callHook(ctx, r.cfg.OnToolCall, hookInput{
		Hook: "on_tool_call", Tool: tool, Params: params,
	})
	if err != nil {
		return false, err
	}
	if out.Action == "block" {
		return false, fmt.Errorf("hooks: tool call blocked: %s", out.Reason)
	}
	return true, nil
}

// OnContextBuild transforms the message list before the LLM call.
func (r *Runner) OnContextBuild(ctx context.Context, msgs []llm.Message) ([]llm.Message, error) {
	if r.cfg.OnContextBuild == "" {
		return msgs, nil
	}
	out, err := r.callHook(ctx, r.cfg.OnContextBuild, hookInput{
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

// OnSessionStart fires the on_session_start hook. Errors are logged but non-fatal.
func (r *Runner) OnSessionStart(ctx context.Context) {
	if r.cfg.OnSessionStart != "" {
		_, _ = r.callHook(ctx, r.cfg.OnSessionStart, hookInput{Hook: "on_session_start"})
	}
}

// OnSessionEnd fires the on_session_end hook. Errors are logged but non-fatal.
func (r *Runner) OnSessionEnd(ctx context.Context) {
	if r.cfg.OnSessionEnd != "" {
		_, _ = r.callHook(ctx, r.cfg.OnSessionEnd, hookInput{Hook: "on_session_end"})
	}
}

// OnTurnStart fires the on_turn_start hook. Errors are logged but non-fatal.
func (r *Runner) OnTurnStart(ctx context.Context) {
	if r.cfg.OnTurnStart != "" {
		_, _ = r.callHook(ctx, r.cfg.OnTurnStart, hookInput{Hook: "on_turn_start"})
	}
}

// OnTurnEnd fires the on_turn_end hook with the assistant response. Errors are non-fatal.
func (r *Runner) OnTurnEnd(ctx context.Context, response string) {
	if r.cfg.OnTurnEnd != "" {
		_, _ = r.callHook(ctx, r.cfg.OnTurnEnd, hookInput{
			Hook:   "on_turn_end",
			Params: map[string]any{"response": response},
		})
	}
}

// OnToolResult fires the on_tool_result hook. Errors are non-fatal.
func (r *Runner) OnToolResult(ctx context.Context, tool, result string) {
	if r.cfg.OnToolResult != "" {
		_, _ = r.callHook(ctx, r.cfg.OnToolResult, hookInput{
			Hook:   "on_tool_result",
			Tool:   tool,
			Params: map[string]any{"result": result},
		})
	}
}
