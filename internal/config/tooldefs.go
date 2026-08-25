package config

import (
	"slices"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// builtins is the one table mapping a built-in's kit name (what an agent
// writes in `use:`) to the schema the model receives. It is ordered, so
// ToolDefs emits a stable list regardless of the order the names arrive in —
// a tool list that reshuffles per turn would invalidate the prompt cache.
var builtins = []struct {
	Name string
	Def  llm.ToolDefinition
}{
	{"bash", bashTool},
	{"bash_bg", bashBgTool},
	{"edit", editFileTool},
	{"media", readMediaTool},
	{"history", historyTool},
}

// ToolDefs returns the llm.ToolDefinition schema list for the built-in tool
// names an agent resolved to (kit.Resolved.Builtins). Unknown names are
// ignored here — kit.Resolve has already rejected them, and this is the
// rendering half.
func ToolDefs(names []string) []llm.ToolDefinition {
	defs := []llm.ToolDefinition{}
	for _, b := range builtins {
		if slices.Contains(names, b.Name) {
			defs = append(defs, b.Def)
		}
	}
	return defs
}

var historyTool = llm.ToolDefinition{
	Name: "history",
	Description: "Search past conversations, or read a stored session's transcript. Full-text search " +
		"covers what you and the user said in every stored session (tool output is not indexed). " +
		"Typical flow: search with query, then read around a hit with session (+ around). Read-only.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"runs":    map[string]any{"type": "boolean", "description": "List recent runs instead of searching. Combine with agent to audit one employee."},
			"agent":   map[string]any{"type": "string", "description": "List only this agent's runs (implies runs)."},
			"query":   map[string]any{"type": "string", "description": "FTS5 search: bare words AND together, \"quoted phrases\" match exactly, OR/NOT/prefix* work. Omit when reading a session."},
			"session": map[string]any{"type": "string", "description": "Session id to read instead of searching"},
			"around":  map[string]any{"type": "integer", "description": "With session: center the excerpt on this message seq (default: the start)"},
			"limit":   map[string]any{"type": "integer", "description": "Max search hits or messages to return (default 10 hits / 20 messages)"},
		},
	},
}

var bashBgTool = llm.ToolDefinition{
	Name: "bash_bg",
	Description: "Start a shell command in the background on the in-process runtime and return a job id immediately. " +
		"Use this for long-running work or servers — anything that should not block the turn. " +
		"You will receive its report automatically — mid-turn into this same reply if the job is quick, in a " +
		"later turn otherwise — so start it, say it's running, and end your turn. " +
		reportParamBrief +
		"Never wait in-turn: no task_status loops, no sleep-and-recheck " +
		"in bash. task_status <id> is for reading a finished job's output or answering a user's how's-it-going.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{"type": "string", "description": "The shell command to run in the background"},
			"workdir": map[string]any{"type": "string", "description": "Working directory; defaults to the project root"},
			"report":  reportParamSchema(),
			"note":    map[string]any{"type": "string", "description": "Context carried into the report (what this job is for, whether anyone is waiting). Ignored when report is \"raw\""},
		},
		"required": []string{"command"},
	},
}

// SubagentRef is one allowed subagent for TaskToolFor: its name plus the
// model-facing "when to use" description from its kit `agent:` block.
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
		Description: "Spawn a subagent that runs in the background. Returns immediately — the completion " +
			"report reaches you automatically: mid-turn into this same reply if it's quick, in a later turn " +
			"otherwise. Dispatch, say it's running, and end your turn. " +
			reportParamBrief +
			"Never wait in-turn: no task_status loops, no sleep-and-recheck. Use this to " +
			"delegate work to a specialised subagent while you continue with other tasks. Brief it like a " +
			"contract — vague prompts produce misaimed work.",
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
					"description": "The task brief for the subagent: objective, expected outcome and output format, and boundaries (what is out of scope)",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "A short 3-5 word label describing the task (used in completion notices)",
				},
				"report": reportParamSchema(),
				"note": map[string]any{
					"type":        "string",
					"description": "Context carried into the report (what this task is for, whether anyone is waiting). Ignored when report is \"raw\"",
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
var TaskStatusTool = taskByIDTool("task_status",
	"Get the status and result of a single background task by id (e.g. sub1, bg1). Returns status, type, and a truncated result.",
	"The task id returned by the task or bash_bg tool (e.g. sub1, bg1)")

// TaskCancelTool is the llm.ToolDefinition for task_cancel: cancels a running
// background task.
var TaskCancelTool = taskByIDTool("task_cancel",
	"Cancel a running background task by id. No-op if the task is already done.",
	"The task id to cancel (e.g. sub1, bg1)")

// taskByIDTool is the schema shared by the task tools that take one required
// {id} — the shape chat.taskByIDHandler answers.
func taskByIDTool(name, desc, idDesc string) llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        name,
		Description: desc,
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id": map[string]any{"type": "string", "description": idDesc},
			},
			"required": []string{"id"},
		},
	}
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
				"description": "Max seconds before the command is killed. Defaults to 10. Clamped to [1, 120].",
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
	Description: "Load a media file from disk so a vision/audio-capable model can perceive it — images (jpg, png, gif, webp), audio (wav, mp3, ogg/opus), or PDFs (pdf). " +
		"The file is decoded and attached as a user message immediately after the tool results, so it appears in your view on the next step. " +
		"Requires a model with the matching modality; PDF parts additionally require a model/provider that accepts file content parts. " +
		"This tool is for images/audio/PDF only — to read text files use `bash` with cat/sed/head.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path to the media file (absolute or relative to the project root)."},
		},
		"required": []string{"path"},
	},
}

// reportParamBrief is the sentence both background-spawn tool descriptions
// carry about the report param. It is one string so the two tools can never
// describe the same mechanism differently — the mismatch a model resolves by
// guessing.
const reportParamBrief = "Use report: to say what the finish does to the chat — " +
	"\"auto\" (default) lets you judge whether the user needs to hear it, " +
	"\"always\" binds you to answer them when it lands, " +
	"\"raw\" posts the output itself and spends no turn of yours. "

// reportParamSchema is the shared `report` property for bash_bg and task: the
// single axis for what a finished job does to the chat. There is deliberately
// no second boolean beside it — "post it raw" and "you must speak" are two
// answers to one question, and a config able to state both is a config able to
// contradict itself.
func reportParamSchema() map[string]any {
	return map[string]any{
		"type": "string",
		"enum": []string{"auto", "always", "raw"},
		"description": "What this job's finish does to the chat. " +
			"\"auto\" (default): its report reaches you in a later turn and you decide whether to tell the user (NO_REPLY posts nothing). " +
			"\"always\": same report, but you MUST answer the user — set it whenever they asked for this result or you told them it was coming, " +
			"because if you stay silent the raw output is posted in your place. " +
			"\"raw\": no turn of yours at all — the job's own output posts straight to the chat.",
	}
}
