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
			Description: "Start a long-running shell command in the background and return its job id immediately. Its completion is saved to the durable inbox; do not poll or sleep in the turn.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"command": map[string]any{"type": "string", "description": "The shell command to run"},
					"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
				},
				"required":             []string{"command"},
				"additionalProperties": false,
			},
		},
	}
}
