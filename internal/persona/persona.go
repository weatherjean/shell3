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

	// Skills and Tools are optional allowlists. Empty means load everything
	// available. When non-empty, only items whose names appear in the list
	// are loaded for this persona.
	Skills []string `yaml:"skills,omitempty"`
	Tools  []string `yaml:"tools,omitempty"`

	Parameters PersonaParams `yaml:"parameters"`

	hooks.Config `yaml:",inline"`
}

// PersonaParams is the YAML shape of the `parameters:` frontmatter block.
// Pointers distinguish "absent" from "explicitly false/zero".
type PersonaParams struct {
	ReasoningEffort   string   `yaml:"reasoning_effort"`
	ParallelToolCalls *bool    `yaml:"parallel_tool_calls"`
	Temperature       *float64 `yaml:"temperature"`
	MaxTokens         int      `yaml:"max_tokens"`
}

// ToRequestParams maps the YAML block onto the adapter-facing struct.
func (pp PersonaParams) ToRequestParams() llm.RequestParams {
	return llm.RequestParams{
		ReasoningEffort:   pp.ReasoningEffort,
		ParallelToolCalls: pp.ParallelToolCalls,
		Temperature:       pp.Temperature,
		MaxTokens:         pp.MaxTokens,
	}
}

// TemplateData holds values injected into persona template bodies.
type TemplateData struct {
	Skills       string              // output of skills.BuildSection
	Time         string              // formatted current time
	CWD          string              // working directory
	Model        string              // active model name
	CoreMemories []store.MemoryEntry // memories with core=true; rendered into prompt
	UserTools    []ToolDef           // user-defined tools loaded for this session
}

// Persona holds a rendered persona ready for use in a chat session.
type Persona struct {
	Config       PersonaConfig
	Name         string // convenience alias for Config.Name
	SystemPrompt string
	Tools        []ToolDef
	Parameters   llm.RequestParams
}

// ParseConfig searches dirs in order for <name>.md, parses frontmatter, and
// returns both the config and the raw template body. First dir that has the
// file wins (project dir before global dir). Pass both to Load to avoid
// reading the file a second time.
func ParseConfig(dirs []string, name string) (PersonaConfig, string, error) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, name+".md")
		raw, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return PersonaConfig{}, "", fmt.Errorf("persona: read %s: %w", path, err)
		}
		fm, body := extractParts(string(raw))
		var cfg PersonaConfig
		if err := yaml.Unmarshal([]byte(fm), &cfg); err != nil {
			return PersonaConfig{}, "", fmt.Errorf("persona: parse frontmatter %s: %w", name, err)
		}
		if cfg.Name == "" {
			cfg.Name = name
		}
		return cfg, body, nil
	}
	return PersonaConfig{}, "", fmt.Errorf("persona %q not found — check .shell3/personas/ or ~/.shell3/personas/", name)
}

// Load renders a persona given a pre-parsed config and template body.
// Obtain cfg and body from ParseConfig to avoid reading the file twice.
//
// userTools are merged after built-ins in the returned Persona.Tools.
func Load(cfg PersonaConfig, body string, data TemplateData, hasStore, noBash bool, userTools []ToolDef) (Persona, error) {
	tmpl, err := template.New(cfg.Name).Parse(body)
	if err != nil {
		return Persona{}, fmt.Errorf("persona: parse template %s: %w", cfg.Name, err)
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return Persona{}, fmt.Errorf("persona: render %s: %w", cfg.Name, err)
	}

	var tools []ToolDef
	tools = append(tools, docsTool, pruneToolResultTool, compactHistoryTool)
	if !noBash {
		tools = append(tools, bashTool, shellInteractiveTool, editFileTool)
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
		Parameters:   cfg.Parameters.ToRequestParams(),
	}, nil
}

// BuiltinToolNames returns the names of all built-in tools. Used by usertools
// to prevent name collisions.
func BuiltinToolNames() map[string]struct{} {
	all := append([]ToolDef{docsTool, pruneToolResultTool, compactHistoryTool, bashTool, shellInteractiveTool,
		editFileTool}, storeTools...)
	names := make(map[string]struct{}, len(all))
	for _, t := range all {
		names[t.Name] = struct{}{}
	}
	return names
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
	Description: "Return shell3's own documentation. Use when asked what shell3 is or how to configure commands, personas, tools, skills, hooks, providers, secrets, or storage.",
	Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
}

var pruneToolResultTool = ToolDef{
	Name: "prune_tool_result",
	Description: "Replace a prior tool result with a short stub to free context. " +
		"Use whenever a result is no longer needed — any size, any content. " +
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

var compactHistoryTool = ToolDef{
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
		Name: "memory_list",
		Description: "List project memory entries newest-first. No search — for that, use memory_search. " +
			"Set core_only=true to restrict to memories marked core=true.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"core_only": map[string]any{"type": "boolean", "description": "Only return core memories"},
				"limit":     map[string]any{"type": "integer", "description": "Maximum results (default 50)"},
			},
		},
	},
	{
		Name: "memory_search",
		Description: "Full-text search project memories. Use `terms` as focused concepts, one per array element; do not pass whole sentences. " +
			"Default match=any; use match=all only to narrow. Multi-word terms match as phrases.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"terms":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "One concept per element. Each becomes a quoted FTS5 phrase."},
				"match":     map[string]any{"type": "string", "enum": []string{"any", "all"}, "description": "any = OR (default, broad recall); all = AND (narrow)."},
				"core_only": map[string]any{"type": "boolean", "description": "Only search core memories"},
				"limit":     map[string]any{"type": "integer", "description": "Maximum results (default 50)"},
			},
			"required": []string{"terms"},
		},
	},
	{
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
	},
	{
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
	},
}
