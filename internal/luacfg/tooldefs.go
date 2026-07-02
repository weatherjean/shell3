package luacfg

import (
	"github.com/weatherjean/shell3/internal/llm"
)

// ToolDefs returns the llm.ToolDefinition schema list for an agent: each
// built-in tool whose gate is enabled (bash, edit, …), plus one definition per
// custom tool.
func ToolDefs(g ToolGates, custom []CustomTool) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{}
	if g.Bash {
		defs = append(defs, bashTool)
	}
	if g.BashBg {
		defs = append(defs, bashBgTool)
	}
	if g.ShellInteractive {
		defs = append(defs, shellInteractiveTool)
	}
	if g.Edit {
		defs = append(defs, editFileTool)
	}
	if g.Media {
		defs = append(defs, readMediaTool)
	}
	if g.Read {
		defs = append(defs, readTool)
	}
	if g.ListFiles {
		defs = append(defs, listFilesTool)
	}
	for _, ct := range custom {
		defs = append(defs, llm.ToolDefinition{
			Name:        ct.Name,
			Description: ct.Description,
			Parameters:  ct.Parameters,
		})
	}
	return defs
}

// CustomToolsFor returns the CustomTool values for the agent's allowlist, in
// allowlist order. Unknown names are skipped.
func (c *LoadedConfig) CustomToolsFor(names []string) []CustomTool {
	out := make([]CustomTool, 0, len(names))
	for _, n := range names {
		if ct, ok := c.Tools[n]; ok {
			out = append(out, ct)
		}
	}
	return out
}

var shellInteractiveTool = llm.ToolDefinition{
	Name:        "shell_interactive",
	Description: "Run a command that requires an interactive terminal (e.g. vim, less, python REPL). The TUI hands the terminal to the process and resumes when it exits. Returns only a completion status — the command's output goes to the user's terminal and is NOT returned to you, so you cannot read or verify what it printed.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to run interactively",
			},
		},
		"required": []string{"command"},
	},
}

var bashBgTool = llm.ToolDefinition{
	Name: "bash_bg",
	Description: "Start a shell command in the background on the in-process runtime and return a job id immediately. " +
		"Use this for long-running servers, watchers, or any command that should not block the turn. " +
		"The host notifies you of completion on a later turn — do not poll. " +
		"There is no pid, no status file, and no log path; job management is host-side.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "The shell command to run in the background"},
			"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
		},
		"required": []string{"command"},
	},
}

// TaskTool is the llm.ToolDefinition for the `task` tool: spawns a subagent
// (child session) that runs in the background and notifies the parent on
// completion. Exposed via luacfg so agentsetup can append it to the tool schema
// for any agent that has delegation enabled.
var TaskTool = llm.ToolDefinition{
	Name: "task",
	Description: "Spawn a subagent that runs in the background. Returns immediately — you will be notified " +
		"of completion on a later turn. Do NOT poll for results. Use this to delegate work to a " +
		"specialised subagent while you continue with other tasks.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subagent_type": map[string]any{
				"type":        "string",
				"description": "The subagent type to spawn (one of the names listed in the Delegation context)",
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
	Description: "Get the status and result of a single background task by id (e.g. sub1, bg1). Returns status, type, depth, and a truncated result.",
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
	Description: "Execute a non-interactive shell command in the project directory. Returns combined stdout and stderr. Do not use for editors or interactive programs — use shell_interactive instead. Default timeout is 10s; pass timeout_seconds (max 600) for slower commands. To read a whole text file prefer the `read` tool.",
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
		"To inspect a file use the `read` tool (text) or `bash` with `sed -n`/`head` for slices. To search use `bash` with `grep` or `rg`. " +
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
	Description: "Load a media file from disk so a vision/audio-capable model can perceive it — images (jpg, png, gif, webp) or audio (wav, mp3, ogg/opus). " +
		"The file is decoded and attached as a user message immediately after the tool results, so it appears in your view on the next step. " +
		"Requires a model with the matching modality. This tool is for images/audio only — to read text files use `bash` with cat/sed/head.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path to the media file (absolute or relative to the project root)."},
		},
		"required": []string{"path"},
	},
}

var listFilesTool = llm.ToolDefinition{
	Name: "list_files",
	Description: "List a directory as an indented tree (directories first, suffixed \"/\"). " +
		"Pairs with the read tool so a read-only agent can explore the filesystem without bash. " +
		"Start SHALLOW: the default depth is 2 — widen `depth` only as needed, or narrow by passing a deeper `path` or `ignore` globs (e.g. [\"node_modules\", \"*.lock\"]). " +
		"No automatic filtering: hidden and vendored files are shown unless you ignore them. " +
		"Output is capped at 1000 entries with a truncation notice. To find files by name use bash glob/rg; to read a file use the read tool.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "Directory to list (absolute or relative to the project root). Defaults to the project root."},
			"depth":  map[string]any{"type": "integer", "description": "Max levels to recurse. Defaults to 2. Use 1 for just the immediate directory."},
			"ignore": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Glob patterns to exclude. A pattern without \"/\" matches the base name (e.g. \"*.test.go\"); with \"/\" it matches the path relative to the listed directory."},
		},
	},
}

var readTool = llm.ToolDefinition{
	Name: "read",
	Description: "Read a text file with paging. Returns raw file content (no line-number prefixes). " +
		"Output is capped at 2000 lines or 50KB, whichever comes first; when truncated the footer gives the exact offset to continue from. " +
		"Use offset (1-indexed line) and limit to page through large files. " +
		"This tool is for TEXT files — images/audio go through read_media; raw bytes via bash xxd. " +
		"To search file contents use bash with rg/grep.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":   map[string]any{"type": "string", "description": "Path to the file (absolute or relative to the project root)."},
			"offset": map[string]any{"type": "integer", "description": "1-indexed line to start from. Defaults to 1."},
			"limit":  map[string]any{"type": "integer", "description": "Max lines to return. Defaults to 2000."},
		},
		"required": []string{"path"},
	},
}
