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
	Skills string // output of skills.BuildSection
	Time   string // formatted current time
	CWD    string // working directory
	Model  string // active model name
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
	tools = append(tools, docsTool)
	if !noBash {
		tools = append(tools, bashTool, shellInteractiveTool)
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
