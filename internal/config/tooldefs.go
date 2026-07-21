package config

import (
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// ToolDefs returns the llm.ToolDefinition schema list for an agent: each
// built-in tool whose gate is enabled (bash, edit, …).
func ToolDefs(g ToolGates) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{}
	if g.Bash {
		defs = append(defs, bashTool)
	}
	if g.BashBg {
		defs = append(defs, bashBgTool)
	}
	if g.Edit {
		defs = append(defs, editFileTool)
	}
	if g.Media {
		defs = append(defs, readMediaTool)
	}
	return defs
}

var bashBgTool = llm.ToolDefinition{
	Name: "bash_bg",
	Description: "Start a shell command in the background on the in-process runtime and return a job id immediately. " +
		"Use this for long-running work or servers — anything that should not block the turn. " +
		"When the job finishes you are woken with a completion notice carrying an output tail — do not poll. " +
		"There is no pid and no log path; use task_status <id> to read more of a finished job's output.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "The shell command to run in the background"},
			"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
			"quiet":   map[string]any{"type": "boolean", "description": "When true, a clean exit queues its notice for your next turn instead of waking you (failures still wake). Use for servers, watchers, and side jobs whose completion needs no immediate action"},
		},
		"required": []string{"command"},
	},
}

// SubagentRef is one allowed subagent for TaskToolFor: its name plus the
// model-facing "when to use" description from its shell3.subagent declaration.
type SubagentRef struct{ Name, Description string }

// TaskToolFor returns the llm.ToolDefinition for the `task` tool with the
// agent's concrete allowlist baked into the schema: subagent_type carries an
// enum of the allowed names and its description lists what each subagent is
// for. The schema is the single place the model learns what it may spawn — no
// separate delegation reminder spends per-turn tokens restating it. Exposed
// via config so agentsetup can append it to the tool schema for any agent that
// has delegation enabled.
func TaskToolFor(subs []SubagentRef) llm.ToolDefinition {
	names := make([]string, 0, len(subs))
	var b strings.Builder
	b.WriteString("The subagent type to spawn:")
	for _, s := range subs {
		names = append(names, s.Name)
		b.WriteString("\n- " + s.Name)
		if s.Description != "" {
			b.WriteString(": " + s.Description)
		}
	}
	return llm.ToolDefinition{
		Name: "task",
		Description: "Spawn a subagent that runs in the background. Returns immediately — you will be notified " +
			"of completion on a later turn. Do NOT poll for results. Use this to delegate work to a " +
			"specialised subagent while you continue with other tasks.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"subagent_type": map[string]any{
					"type":        "string",
					"enum":        names,
					"description": b.String(),
				},
				"prompt": map[string]any{
					"type":        "string",
					"description": "The task prompt to send to the subagent",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "A short 3-5 word label describing the task (used in completion notices)",
				},
			},
			"required": []string{"subagent_type", "prompt"},
		},
	}
}

// TaskListTool is the llm.ToolDefinition for task_list: lists all running and
// finished background tasks (subagents and bash_bg commands).
var TaskListTool = llm.ToolDefinition{
	Name:        "task_list",
	Description: "List all background tasks (subagents and bash_bg commands) with their status. Returns running tasks first, then finished ones.",
	Parameters: map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	},
}

// TaskStatusTool is the llm.ToolDefinition for task_status: returns one task's
// status and a truncated result (transcript tail for subagents, output for commands).
var TaskStatusTool = llm.ToolDefinition{
	Name:        "task_status",
	Description: "Get the status and result of a single background task by id (e.g. sub1, bg1). Returns status, type, and a truncated result.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The task id returned by the task or bash_bg tool (e.g. sub1, bg1)",
			},
		},
		"required": []string{"id"},
	},
}

// TaskCancelTool is the llm.ToolDefinition for task_cancel: cancels a running
// background task.
var TaskCancelTool = llm.ToolDefinition{
	Name:        "task_cancel",
	Description: "Cancel a running background task by id. No-op if the task is already done.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id": map[string]any{
				"type":        "string",
				"description": "The task id to cancel (e.g. sub1, bg1)",
			},
		},
		"required": []string{"id"},
	},
}

var bashTool = llm.ToolDefinition{
	Name:        "bash",
	Description: "Execute a shell command in the project directory. Returns combined stdout and stderr. Non-interactive only — editors and REPLs (vim, less, python) will hang, so run them non-interactively (flags, heredocs, -c). Default timeout is 10s; pass timeout_seconds (max 120) for slower commands. A foreground call blocks your whole turn — run anything slower than ~2 minutes via bash_bg instead (it wakes you with the result when done). Read files with cat / sed -n / rg; list directories with ls / find.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to run",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"description": "Max seconds before the command is killed. Defaults to 10. Clamped to [1, 600].",
			},
		},
		"required": []string{"command"},
	},
}

var editFileTool = llm.ToolDefinition{
	Name: "edit_file",
	Description: "WRITE-ONLY tool. Edits a file by exact string replacement, or writes/overwrites it when old_string is empty. " +
		"NEVER call this tool to read a file — it has no read mode and an empty new_string DELETES the matched chunk. " +
		"To inspect a file use `bash` with `cat`/`sed -n`/`head`. To search use `bash` with `grep` or `rg`. " +
		"Calling edit_file with empty new_string when you only wanted to read will silently delete content; this is destructive and cannot be undone. " +
		"To create or overwrite a file pass an empty old_string and the full content as new_string. " +
		"To delete a chunk, pass an empty new_string (intentional). " +
		"By default old_string must be unique in the file; set replace_all=true to replace every occurrence. " +
		"Falls back to fuzzy line-trim/whitespace/indentation/escape matching if exact match fails. " +
		"Prefer this over `bash` heredoc for code edits — it is atomic, diffs cleanly, and refuses ambiguous matches.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":   map[string]any{"type": "string", "description": "Path to the file (absolute or relative to project root). This tool MUTATES the file — never call it to read."},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace; empty ONLY when you intend to create or overwrite the entire file"},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text; empty DELETES the matched chunk (do not leave empty unless deletion is intended)"},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence (default false)"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	},
}

var readMediaTool = llm.ToolDefinition{
	Name: "read_media",
	Description: "Load a media file from disk so a vision/audio-capable model can perceive it — images (jpg, png, gif, webp), audio (wav, mp3, ogg/opus), PDFs (pdf), or video (mp4, webm, mov). " +
		"The file is decoded and attached as a user message immediately after the tool results, so it appears in your view on the next step. " +
		"Requires a model with the matching modality; PDF and video parts additionally require a model/provider that accepts file/video content parts. " +
		"This tool is for images/audio/PDF/video only — to read text files use `bash` with cat/sed/head.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path to the media file (absolute or relative to the project root)."},
		},
		"required": []string{"path"},
	},
}
