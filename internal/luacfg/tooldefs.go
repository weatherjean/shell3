package luacfg

import "github.com/weatherjean/shell3/internal/llm"

// skillTool is the built-in tool injected when the agent has ≥1 skill.
var skillTool = llm.ToolDefinition{
	Name:        "skill",
	Description: "Return the full body of a named skill from the skill index in the system prompt. Call this when one of the listed skills applies to the task.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "description": "The skill name exactly as shown in the skill index."},
		},
		"required": []string{"name"},
	},
}

// ToolDefs returns the llm.ToolDefinition schema list for an agent: each
// built-in tool whose gate is enabled (prune_tool_result, compact_history,
// bash, edit, …), the skill tool when hasSkills is true, plus one definition
// per custom tool.
func ToolDefs(g ToolGates, custom []CustomTool, hasSkills bool) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{}
	if g.Prune {
		defs = append(defs, pruneToolResultTool)
	}
	if g.Compact {
		defs = append(defs, compactHistoryTool)
	}
	if hasSkills {
		defs = append(defs, skillTool)
	}
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
	if g.History {
		defs = append(defs, historyGetTool, historySearchTool)
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

var pruneToolResultTool = llm.ToolDefinition{
	Name: "prune_tool_result",
	Description: "Replace a prior tool result with a short stub to free context. " +
		"Use whenever a result is no longer needed. " +
		"Scoped to the last 2 turns; older results return an out-of-scope error. " +
		"Copy the id from the result's `[tool_call_id=<id>]` header into tool_call_id.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_call_id": map[string]any{"type": "string", "description": "ID of the tool call whose result should be pruned"},
			"reason":       map[string]any{"type": "string", "description": "One-line note on why the result is no longer needed"},
		},
		"required": []string{"tool_call_id", "reason"},
	},
}

var compactHistoryTool = llm.ToolDefinition{
	Name: "compact_history",
	Description: "Compact the conversation history into a structured summary to free context. " +
		"ALWAYS follow the rules in the system prompt for when and how to offer compaction. " +
		"Write a thorough summary: decisions made, code written, errors encountered, outcomes reached. " +
		"List files created or modified. List references worth keeping (sessions, commits, URLs). " +
		"List skills the continuation should re-read, especially active workflow skills. " +
		"Include next steps only if there is confirmed remaining work.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"summary": map[string]any{
				"type":        "string",
				"description": "Narrative summary of the conversation: what was done, decisions made, errors encountered, outcomes reached.",
			},
			"important_files": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "File paths created or modified that may need to be re-read after compaction.",
			},
			"important_references": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "External references worth preserving: session IDs, commit hashes, URLs, ticket numbers.",
			},
			"skills": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Skill names or file paths the continuation should re-read before resuming work.",
			},
			"next_steps": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Remaining work items confirmed by the user. Omit if work is complete.",
			},
		},
		"required": []string{"summary"},
	},
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
		"`cat .shell3/bg.json` to list, " +
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

var historyGetTool = llm.ToolDefinition{
	Name: "history_get",
	Description: "Read one chunk of a completed past session. Omit args for the most recent completed session, chunk 1. " +
		"Use returned total_chunks to page and prev_session_id/next_session_id to walk sessions. Use for references like \"last time\" or \"earlier\".",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "integer", "description": "Which session to read. Omit or 0 = most-recent completed. Use prev_session_id from a previous response to walk backwards."},
			"chunk":      map[string]any{"type": "integer", "description": "1-based chunk within the session (25 turns each). Omit or 0 = chunk 1."},
		},
	},
}

var historySearchTool = llm.ToolDefinition{
	Name: "history_search",
	Description: "Full-text search past conversations. Use `terms` as focused concepts, one per array element; do not pass whole sentences. " +
		"Default match=any; use match=all only to narrow. Hits include session_id and chunk for follow-up history_get.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"terms": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "One concept per element. Each becomes a quoted FTS5 phrase."},
			"match": map[string]any{"type": "string", "enum": []string{"any", "all"}, "description": "any = OR (default, broad recall); all = AND (narrow)."},
			"limit": map[string]any{"type": "integer", "description": "Max search hits (default 20)"},
		},
		"required": []string{"terms"},
	},
}
