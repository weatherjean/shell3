package orchestrator

import "github.com/weatherjean/shell3/internal/llm"

func coreToolDefinitions() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "bash",
			Description: "Execute a non-interactive shell command in the project directory. Returns combined stdout and stderr. Read and search files with ordinary Unix commands. Use bash_bg for long-running work.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command":         map[string]any{"type": "string", "description": "The shell command to run"},
					"timeout_seconds": map[string]any{"type": "integer", "description": "Timeout in seconds, clamped to 1-120"},
				},
				"required": []string{"command"},
			},
		},
		{
			Name:        "bash_bg",
			Description: "Start a long-running shell command and return its job id immediately. Completion is saved to the durable inbox. Set poll_in to schedule one later progress-check turn while it runs (interactive console or Telegram). Finish this turn after arranging the check. This also works for shell3 wrk run commands.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The shell command to run"},
					"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
					"poll_in": map[string]any{"type": "string", "description": "Optional one-shot check-in delay, 1m to 24h; typically 2m, 3m, or 5m. A busy conversation defers the check. Completion cancels it. Re-arm with shell3 action=poll and job_id."},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}
