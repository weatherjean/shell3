package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// BashTool executes shell commands with a configurable timeout.
type BashTool struct {
	cwd        string
	timeoutSec int
}

// NewBashTool returns a BashTool running in cwd with a timeoutSec second timeout.
func NewBashTool(cwd string, timeoutSec int) *BashTool {
	return &BashTool{cwd: cwd, timeoutSec: timeoutSec}
}

// Definition returns the LLM tool definition for bash.
func (t *BashTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "bash",
		Description: "Execute a shell command in the project directory. Use for reading files, running tests, making changes.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "Shell command to execute",
				},
			},
			"required": []string{"command"},
		},
	}
}

// Execute runs the command and returns combined stdout+stderr.
func (t *BashTool) Execute(ctx context.Context, params map[string]any) (string, error) {
	cmd, ok := params["command"].(string)
	if !ok || cmd == "" {
		return "", fmt.Errorf("bash: command param required")
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(t.timeoutSec)*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = t.cwd

	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr

	if err := c.Run(); err != nil {
		out := stdout.String() + stderr.String()
		return out, fmt.Errorf("bash: exit error: %w\n%s", err, out)
	}
	return stdout.String() + stderr.String(), nil
}
