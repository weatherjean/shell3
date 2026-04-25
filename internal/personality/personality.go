package personality

import (
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/skills"
)

// Type identifies a built-in personality.
type Type string

const (
	TypeCode  Type = "code"
	TypeAgent Type = "agent"
)

// ToolDef is a thin alias so callers don't import llm directly.
type ToolDef = llm.ToolDefinition

// Personality holds everything needed to start a chat session.
type Personality struct {
	SystemPrompt string
	Tools        []ToolDef
}

// Build constructs a Personality for the given type.
// loadedSkills are appended to the system prompt.
// hasStore controls whether memory/history tools are included.
// noBash removes bash and shell_interactive tools.
func Build(t Type, loadedSkills []skills.Skill, hasStore, noBash bool) Personality {
	var base string
	switch t {
	case TypeAgent:
		base = agentPrompt
	default:
		base = codePrompt
	}

	prompt := base + skills.BuildSection(loadedSkills)

	var tools []ToolDef
	tools = append(tools, docsTool)
	if !noBash {
		tools = append(tools, bashTool, shellInteractiveTool)
	}
	if hasStore {
		tools = append(tools, storeTools...)
	}

	return Personality{SystemPrompt: prompt, Tools: tools}
}

var docsTool = ToolDef{
	Name:        "shell3_docs",
	Description: "Return shell3's own documentation: commands, config format, slash commands, keyboard shortcuts, and project structure. Call when asked what shell3 is or what it can do.",
	Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
}

// shellInteractiveTool runs a command with a real terminal (TTY handoff).
// Use for interactive programs: editors, pagers, REPL sessions, etc.
var shellInteractiveTool = ToolDef{
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

var bashTool = ToolDef{
	Name:        "bash",
	Description: "Execute a non-interactive shell command in the project directory. Returns combined stdout and stderr. Do not use for editors or interactive programs — use shell_interactive instead.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to run",
			},
		},
		"required": []string{"command"},
	},
}

var storeTools = []ToolDef{
	{
		Name:        "memory_store",
		Description: "Store a key-value entry in project memory for future reference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember"},
			},
			"required": []string{"key", "value"},
		},
	},
	{
		Name:        "memory_list",
		Description: "List all stored memory entries.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "memory_search",
		Description: "Search project memory for relevant past decisions, notes, or context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "memory_remove",
		Description: "Remove a key-value entry from project memory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key to remove"},
			},
			"required": []string{"key"},
		},
	},
	{
		Name:        "history_latest",
		Description: "Return the most recent conversation turns. Call when asked about recent or past activity.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	},
	{
		Name:        "history_search",
		Description: "Full-text search past conversation turns by query term.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
}

const codePrompt = `You are shell3 — an agentic coding assistant running in the user's terminal.

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_store   — persist a key-value fact. Call when the user says "remember X" or you learn something worth keeping.
memory_list    — list all stored memories. Call when asked "what do you remember?".
memory_search  — full-text search memories by query term.
memory_remove  — delete a memory entry by key.

history_latest — return the most recent conversation turns. Call when asked about recent or past activity.
history_search — full-text search past conversation turns.

RULES:
- When told "remember X" → call memory_store immediately.
- When asked about memories or past context → call memory_search first. Never answer from training data.
- Never use bash to find or store memories.
- history_search searches past conversations. Never use bash to find past chat history.
- After gathering enough information, respond clearly — do not call tools indefinitely.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

Read before writing. Minimal changes. Test after every change.`

const agentPrompt = `You are shell3 — a general-purpose agent running in the user's terminal.

## Tools

bash — execute shell commands to accomplish tasks.

memory_store   — persist a key-value fact for future reference.
memory_list    — list all stored memories.
memory_search  — full-text search memories by query term.
memory_remove  — delete a memory entry by key.

history_latest — return the most recent conversation turns.
history_search — full-text search past conversation turns.

RULES:
- When told "remember X" → call memory_store immediately.
- When asked about memories or past context → call memory_search first.
- After gathering enough information, respond clearly — do not call tools indefinitely.`
