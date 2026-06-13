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
	Description: "Run a command that requires an interactive terminal (e.g. vim, less, python REPL). The TUI hands the terminal to the process and resumes when it exits.",
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
	Description: "Spawn a detached background shell command and immediately return its pid + log path. " +
		"The process runs in its own process group; shell3 does not wait on it. Use this for long-running " +
		"servers, watchers, or any command that should not block the turn (e.g. `npx some-server`). " +
		"Output is captured to a log file under /tmp/shell3/runs/<id>.log. Manage running jobs with the " +
		"regular `bash` tool: " +
		"`shell3 jobs` to list, " +
		"`tail -n 100 <log>` to inspect output, " +
		"`kill <pid>` or `kill -- -<pid>` (whole group) to stop, " +
		"`kill -0 <pid>` to check if alive, " +
		"`rm <log>` to clean up.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "The shell command to run in the background"},
			"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
		},
		"required": []string{"command"},
	},
}

var bashTool = llm.ToolDefinition{
	Name:        "bash",
	Description: "Execute a non-interactive shell command in the project directory. Returns combined stdout and stderr. Do not use for editors or interactive programs — use shell_interactive instead. Default timeout is 10s; pass timeout_seconds (max 600) for slower commands.",
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
		"To inspect a file use `bash` with `cat`, `sed -n`, `head`, or `tail`. To search use `bash` with `grep` or `rg`. " +
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
	Description: "Load a media file from disk so a vision/audio-capable model can perceive it — images (jpg, png, gif) or audio (wav, mp3). " +
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
