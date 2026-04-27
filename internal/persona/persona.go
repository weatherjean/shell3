// Package persona loads markdown persona files and renders them as Go templates.
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
	"gopkg.in/yaml.v3"
)

// ToolDef is an alias so callers don't import llm directly.
type ToolDef = llm.ToolDefinition

// PersonaConfig holds all configuration parsed from a persona file's
// frontmatter. All fields are optional — null (yaml: ~) means "use
// runtime default".
//
// Lifecycle hooks live in the embedded [hooks.Config], which is YAML-
// flattened so its fields appear at the top level of the persona file:
//
//	on_tool_call: ~                               # disabled
//	on_tool_call: ".shell3/hooks/guard.sh"        # string shorthand
//	on_tool_call:                                 # mapping form
//	  command: ".shell3/hooks/guard.sh"
//	  needs_tty: true
type PersonaConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
	Provider    string `yaml:"provider"`
	DB          string `yaml:"db"`
	NoBash      bool   `yaml:"no_bash"`
	NoMemory    bool   `yaml:"no_memory"`

	hooks.Config `yaml:",inline"`
}

// TemplateData holds values injected into persona template bodies.
type TemplateData struct {
	Skills       string              // output of skills.BuildSection
	Time         string              // formatted current time
	CWD          string              // working directory
	Model        string              // active model name
	CoreMemories []store.MemoryEntry // memories with core=true; rendered into prompt
}

// Persona holds a rendered persona ready for use in a chat session.
type Persona struct {
	Config       PersonaConfig
	Name         string // convenience alias for Config.Name
	SystemPrompt string
	Tools        []ToolDef
}

// ParseConfig reads only the frontmatter of <personasDir>/<name>.md.
// Cheap — call before Load to resolve model/provider/hooks/db.
func ParseConfig(personasDir, name string) (PersonaConfig, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return PersonaConfig{}, fmt.Errorf("persona %q not found in %s — run: shell3 init", name, personasDir)
		}
		return PersonaConfig{}, fmt.Errorf("persona: read %s: %w", path, err)
	}
	fm, _ := extractParts(string(raw))
	var cfg PersonaConfig
	if err := yaml.Unmarshal([]byte(fm), &cfg); err != nil {
		return PersonaConfig{}, fmt.Errorf("persona: parse frontmatter %s: %w", name, err)
	}
	if cfg.Name == "" {
		cfg.Name = name
	}
	return cfg, nil
}

// Validate checks persona config for problems. All fields are optional — model,
// provider, and db all have runtime fallbacks. Reserved for future required fields.
func Validate(_ PersonaConfig, _ string) error {
	return nil
}

// Load reads <personasDir>/<name>.md, parses frontmatter, renders the body
// as a Go template with data, and assembles the tool list.
//
// userTools are merged after built-ins in the returned Persona.Tools.
func Load(personasDir, name string, data TemplateData, hasStore, noBash bool, userTools []ToolDef) (Persona, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Persona{}, fmt.Errorf("persona %q not found in %s — run: shell3 init", name, personasDir)
		}
		return Persona{}, fmt.Errorf("persona: read %s: %w", path, err)
	}

	fm, body := extractParts(string(raw))
	var cfg PersonaConfig
	if err := yaml.Unmarshal([]byte(fm), &cfg); err != nil {
		return Persona{}, fmt.Errorf("persona: parse frontmatter %s: %w", name, err)
	}
	if cfg.Name == "" {
		cfg.Name = name
	}

	tmpl, err := template.New(name).Parse(body)
	if err != nil {
		return Persona{}, fmt.Errorf("persona: parse template %s: %w", name, err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return Persona{}, fmt.Errorf("persona: render %s: %w", name, err)
	}

	var tools []ToolDef
	tools = append(tools, docsTool, pruneToolResultTool)
	if !noBash {
		tools = append(tools, bashTool, shellInteractiveTool, editFileTool, writeFileTool)
	}
	if hasStore {
		tools = append(tools, storeTools...)
	}
	tools = append(tools, userTools...)

	return Persona{
		Config:       cfg,
		Name:         cfg.Name,
		SystemPrompt: buf.String(),
		Tools:        tools,
	}, nil
}

// extractParts splits a persona file into (frontmatter YAML, template body).
func extractParts(content string) (frontmatter, body string) {
	if !strings.HasPrefix(content, "---") {
		return "", content
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return "", content
	}
	return parts[1], strings.TrimLeft(parts[2], "\n")
}

var docsTool = ToolDef{
	Name:        "shell3_docs",
	Description: "Return shell3's own documentation: commands, config format, slash commands, keyboard shortcuts, project structure, and skills. Call when asked what shell3 is, what it can do, or how to create a skill.",
	Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
}

var pruneToolResultTool = ToolDef{
	Name: "prune_tool_result",
	Description: "Replace a previous tool result in the conversation with a short stub to free context. " +
		"Use only when a successful tool call produced output that is now irrelevant to the remaining task " +
		"(e.g. you grepped a large file but only one line mattered, or you read a config you no longer need). " +
		"The tool call itself is preserved; only the result content is shortened. " +
		"Will refuse to prune small results (< 500 bytes) or results that look like errors. " +
		"Every tool result begins with a `[tool_call_id=<id>]` header line — copy that id into tool_call_id.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tool_call_id": map[string]any{"type": "string", "description": "ID of the tool call whose result should be pruned"},
			"reason":       map[string]any{"type": "string", "description": "One-line note on why the result is no longer needed"},
		},
		"required": []string{"tool_call_id", "reason"},
	},
}

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

var editFileTool = ToolDef{
	Name: "edit_file",
	Description: "Edit a file by exact string replacement. Provide old_string (must match exactly) and new_string. " +
		"To create a new file, pass an empty old_string. To delete a chunk, pass an empty new_string. " +
		"By default old_string must be unique in the file; set replace_all=true to replace every occurrence. " +
		"Falls back to fuzzy line-trim/whitespace/indentation/escape matching if exact match fails. " +
		"Prefer this over `bash` heredoc for code edits — it is atomic, diffs cleanly, and refuses ambiguous matches.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path":   map[string]any{"type": "string", "description": "Path to the file (absolute or relative to project root)"},
			"old_string":  map[string]any{"type": "string", "description": "Exact text to replace; empty to create a new file"},
			"new_string":  map[string]any{"type": "string", "description": "Replacement text; empty to delete the matched chunk"},
			"replace_all": map[string]any{"type": "boolean", "description": "Replace every occurrence (default false)"},
		},
		"required": []string{"file_path", "old_string", "new_string"},
	},
}

var writeFileTool = ToolDef{
	Name:        "write_file",
	Description: "Overwrite a file with the given content. Creates parent directories as needed. Prefer edit_file for partial changes; only use write_file when replacing the whole file or creating one from scratch.",
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file_path": map[string]any{"type": "string", "description": "Path to the file (absolute or relative to project root)"},
			"content":   map[string]any{"type": "string", "description": "Full file content to write"},
		},
		"required": []string{"file_path", "content"},
	},
}

var storeTools = []ToolDef{
	{
		Name: "memory_upsert",
		Description: "Insert, update, or delete a project memory entry. " +
			"Pass an empty value to delete. " +
			"Pass core=true to mark a fact important enough to be injected into every session prompt; " +
			"omit core when updating to preserve its current setting.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember; empty string deletes the entry"},
				"core":  map[string]any{"type": "boolean", "description": "If true, memory is injected into the system prompt every session. Omit to preserve existing value."},
			},
			"required": []string{"key", "value"},
		},
	},
	{
		Name: "memory_query",
		Description: "Query project memory. " +
			"Omit query to list newest-first. " +
			"Provide query for full-text search ranked by relevance. " +
			"Set core_only=true to restrict to core memories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":     map[string]any{"type": "string", "description": "Optional FTS query; omit to list all"},
				"core_only": map[string]any{"type": "boolean", "description": "Only return core memories"},
				"limit":     map[string]any{"type": "integer", "description": "Maximum results (default 50)"},
			},
		},
	},
	{
		Name: "history_query",
		Description: "Query past conversation history. " +
			"With a query: full-text search across all sessions; each hit includes session_id and chunk so you can fetch surrounding context. " +
			"Without a query: fetch one chunk of one session — defaults to the most recent COMPLETED session (not the current one), chunk 1. " +
			"Use next_session_id / prev_session_id from a get response to walk the chain. " +
			"Use chunk + total_chunks to page within a long session (25 turns per chunk).",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "Optional FTS query"},
				"session_id": map[string]any{"type": "integer", "description": "Session id to fetch (get mode); 0 or omit for latest completed"},
				"chunk":      map[string]any{"type": "integer", "description": "Chunk index within session, 1-based (get mode); omit or 0 for first chunk"},
				"limit":      map[string]any{"type": "integer", "description": "Max search hits (search mode, default 20)"},
			},
		},
	},
}
