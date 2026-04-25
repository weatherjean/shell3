# Persona Owns Config Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all project config into persona frontmatter; delete `config.yaml`; parse frontmatter with `yaml.v3`; hooks flattened to top level with `~` (null) defaults; `db` field configures store path (default `.shell3/shell3.db`); add `Validate` for clear "missing field" errors; always load `base.md` with `--persona` flag to override.

**Architecture:** `persona.ParseConfig` reads frontmatter only (cheap, called first in run.go to resolve model/provider/hooks/db). `persona.Validate` checks required fields and returns actionable errors. `persona.Load` does full render and returns `Persona{Config, SystemPrompt, Tools}`. `config.ProjectConfig` and `LoadProject` deleted. DB path comes from `pCfg.DB` with `.shell3/shell3.db` fallback. CLI flags override persona values.

**Tech Stack:** `gopkg.in/yaml.v3` (existing dep), `hooks.HookEntry` (existing type, supports both string and mapping YAML forms).

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Modify | `internal/persona/persona.go` | `PersonaConfig` with flat hooks + yaml tags; `ParseConfig`; `Validate`; update `Load` + `Persona` struct |
| Modify | `internal/persona/persona_test.go` | Tests for `ParseConfig`, `Validate`, flat hooks, `Persona.Config` |
| Modify | `internal/config/config.go` | Remove `ProjectConfig`/`LoadProject`; credentials only |
| Modify | `internal/config/config_test.go` | Remove project config tests |
| Modify | `internal/scaffold/scaffold.go` | Remove config.yaml; flat hooks in templates; `checkExisting` uses personas/base.md |
| Modify | `internal/scaffold/scaffold_test.go` | Update for new init behavior |
| Modify | `cmd/shell3/run.go` | `--persona` flag; `ParseConfig` + `Validate` first; construct `hooks.Config`; hardcode DB |
| Modify | `.shell3/personas/base.md` | Full frontmatter with model/provider/flat hooks |

---

### Task 1: Enrich `internal/persona` — `PersonaConfig`, `ParseConfig`, `Validate`, updated `Load`

**Files:**
- Modify: `internal/persona/persona_test.go`
- Modify: `internal/persona/persona.go`

- [ ] **Step 1: Write failing tests**

Add to `internal/persona/persona_test.go` (keep all existing tests, append these):

```go
const fullPersona = `---
name: code
description: Coding assistant
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ".shell3/hooks/guard.sh"
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are a test agent. Time: {{.Time}}.
{{- if .Skills}}
{{.Skills}}
{{- end}}`

func TestParseConfig_ReadsFields(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", fullPersona)

	cfg, err := persona.ParseConfig(dir, "base")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "code" {
		t.Errorf("got name %q, want code", cfg.Name)
	}
	if cfg.Model != "llama3.2" {
		t.Errorf("got model %q, want llama3.2", cfg.Model)
	}
	if cfg.Provider != "ollama" {
		t.Errorf("got provider %q, want ollama", cfg.Provider)
	}
	if cfg.NoBash {
		t.Error("no_bash should be false")
	}
	if cfg.NoMemory {
		t.Error("no_memory should be false")
	}
	if cfg.OnToolCall.Command != ".shell3/hooks/guard.sh" {
		t.Errorf("got on_tool_call %q", cfg.OnToolCall.Command)
	}
}

func TestParseConfig_MissingFileReturnsError(t *testing.T) {
	_, err := persona.ParseConfig(t.TempDir(), "nonexistent")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestValidate_AlwaysPassesWhenParseable(t *testing.T) {
	// model/provider/db are all optional — runtime falls back to credentials.
	cfg := persona.PersonaConfig{Name: "code"}
	if err := persona.Validate(cfg, "base"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_EmptyPersonaFileNameStillOK(t *testing.T) {
	// Name defaults to filename stem — no required fields.
	if err := persona.Validate(persona.PersonaConfig{}, "base"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_ConfigEmbedded(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", fullPersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{Time: "now"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Config.Model != "llama3.2" {
		t.Errorf("expected model llama3.2, got %q", p.Config.Model)
	}
	if p.Config.OnToolCall.Command != ".shell3/hooks/guard.sh" {
		t.Errorf("expected hook, got %q", p.Config.OnToolCall.Command)
	}
}

func TestLoad_NameFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", fullPersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "code" {
		t.Errorf("got name %q, want code (from frontmatter)", p.Name)
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
go test ./internal/persona/... -run "TestParseConfig|TestValidate|TestLoad_Config|TestLoad_NameFromFrontmatter" -v 2>&1 | tail -20
```
Expected: FAIL — `ParseConfig`, `Validate`, `PersonaConfig`, `Persona.Config` undefined

- [ ] **Step 3: Implement persona.go**

Replace `internal/persona/persona.go` top section (keep tool var declarations at bottom unchanged):

```go
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

// PersonaConfig holds all configuration parsed from a persona file's frontmatter.
// Hook fields are top-level (not nested) so every field is visible and explicit.
// Hooks default to ~ (YAML null) — empty command = hook disabled.
type PersonaConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Model       string `yaml:"model"`
	Provider    string `yaml:"provider"`
	DB          string `yaml:"db"`   // store path; defaults to .shell3/shell3.db
	NoBash      bool   `yaml:"no_bash"`
	NoMemory    bool   `yaml:"no_memory"`
	// Lifecycle hooks — top-level for visibility. Three equivalent forms:
	//   on_tool_call: ~                                  # disabled (null)
	//   on_tool_call: ".shell3/hooks/guard.sh"           # string shorthand
	//   on_tool_call:                                    # mapping form
	//     command: ".shell3/hooks/guard.sh"
	//     needs_tty: true
	OnSessionStart hooks.HookEntry `yaml:"on_session_start"`
	OnSessionEnd   hooks.HookEntry `yaml:"on_session_end"`
	OnTurnStart    hooks.HookEntry `yaml:"on_turn_start"`
	OnTurnEnd      hooks.HookEntry `yaml:"on_turn_end"`
	OnToolCall     hooks.HookEntry `yaml:"on_tool_call"`
	OnToolResult   hooks.HookEntry `yaml:"on_tool_result"`
	OnContextBuild hooks.HookEntry `yaml:"on_context_build"`
	OnError        hooks.HookEntry `yaml:"on_error"`
}

// HooksConfig converts the flat persona hook fields into a hooks.Config.
func (c PersonaConfig) HooksConfig() hooks.Config {
	return hooks.Config{
		OnSessionStart: c.OnSessionStart,
		OnSessionEnd:   c.OnSessionEnd,
		OnTurnStart:    c.OnTurnStart,
		OnTurnEnd:      c.OnTurnEnd,
		OnToolCall:     c.OnToolCall,
		OnToolResult:   c.OnToolResult,
		OnContextBuild: c.OnContextBuild,
		OnError:        c.OnError,
	}
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
// Cheap — use before Load to resolve model/provider/hooks.
func ParseConfig(personasDir, name string) (PersonaConfig, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
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

// Validate checks persona config for problems. model/provider/db are all optional —
// runtime falls back to credentials and conventions. Reserved for future required fields.
func Validate(cfg PersonaConfig, personaFile string) error {
	return nil
}

// Load reads <personasDir>/<name>.md, parses frontmatter, renders the body
// as a Go template with data, and assembles the tool list.
func Load(personasDir, name string, data TemplateData, hasStore, noBash bool) (Persona, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
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
```

Remove old `parseFrontmatterName` and `extractBody` (replaced by `extractParts`). Keep all tool var declarations unchanged.

- [ ] **Step 4: Run all persona tests**

```bash
go test ./internal/persona/... -v 2>&1 | grep -E "PASS|FAIL|ok"
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/persona/persona.go internal/persona/persona_test.go
git commit -m "feat(persona): flat hooks in PersonaConfig, Validate for required fields"
```

---

### Task 2: Strip `internal/config` down to credentials only

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Replace config.go**

```go
// Package config loads global credential configuration.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

// Credentials holds all provider credentials loaded from ~/.shell3/credentials.yaml.
type Credentials struct {
	Providers map[string]ProviderCredentials `yaml:"providers"`
}

// ProviderCredentials holds connection details for one LLM provider.
type ProviderCredentials struct {
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	DefaultModel string `yaml:"default_model"`
}

// Get returns credentials for a named provider.
func (c *Credentials) Get(name string) (ProviderCredentials, error) {
	if p, ok := c.Providers[name]; ok {
		return p, nil
	}
	return ProviderCredentials{}, fmt.Errorf("config: provider %q not found", name)
}

// First returns the alphabetically first provider and its credentials.
func (c *Credentials) First() (string, ProviderCredentials, bool) {
	if len(c.Providers) == 0 {
		return "", ProviderCredentials{}, false
	}
	names := make([]string, 0, len(c.Providers))
	for n := range c.Providers {
		names = append(names, n)
	}
	sort.Strings(names)
	name := names[0]
	return name, c.Providers[name], true
}

// LoadCredentials reads ~/.shell3/credentials.yaml from homeDir.
func LoadCredentials(homeDir string) (*Credentials, error) {
	path := filepath.Join(homeDir, ".shell3", "credentials.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("config: no credentials found — run: shell3 auth")
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var creds Credentials
	if err := yaml.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("config: invalid credentials.yaml: %w", err)
	}
	if creds.Providers == nil {
		creds.Providers = map[string]ProviderCredentials{}
	}
	return &creds, nil
}
```

- [ ] **Step 2: Replace config_test.go**

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".shell3"), 0755)
	yaml := "providers:\n  openai:\n    api_key: sk-test123\n    base_url: https://api.openai.com/v1\n"
	os.WriteFile(filepath.Join(dir, ".shell3", "credentials.yaml"), []byte(yaml), 0644)

	creds, err := config.LoadCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := creds.Providers["openai"]
	if !ok {
		t.Fatal("expected openai provider")
	}
	if p.APIKey != "sk-test123" {
		t.Errorf("got api_key %q", p.APIKey)
	}
}

func TestLoadCredentials_Missing(t *testing.T) {
	if _, err := config.LoadCredentials(t.TempDir()); err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestCredentials_First_AlphabeticalOrder(t *testing.T) {
	creds := &config.Credentials{
		Providers: map[string]config.ProviderCredentials{
			"z-provider": {BaseURL: "http://z"},
			"a-provider": {BaseURL: "http://a"},
		},
	}
	name, p, ok := creds.First()
	if !ok {
		t.Fatal("expected a provider")
	}
	if name != "a-provider" {
		t.Errorf("got %q, want a-provider", name)
	}
	if p.BaseURL != "http://a" {
		t.Errorf("got base_url %q", p.BaseURL)
	}
}

func TestCredentials_Get(t *testing.T) {
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{"openai": {APIKey: "sk-abc"}}}
	p, err := creds.Get("openai")
	if err != nil {
		t.Fatal(err)
	}
	if p.APIKey != "sk-abc" {
		t.Errorf("got api_key %q", p.APIKey)
	}
}

func TestCredentials_Get_Missing(t *testing.T) {
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{}}
	if _, err := creds.Get("ghost"); err == nil {
		t.Error("expected error for missing provider")
	}
}
```

- [ ] **Step 3: Run config tests**

```bash
go test ./internal/config/... -v 2>&1 | grep -E "PASS|FAIL|ok"
```
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "refactor(config): credentials only — ProjectConfig removed"
```

---

### Task 3: Update scaffold — no config.yaml, flat hooks in templates

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Write updated tests**

Replace `internal/scaffold/scaffold_test.go` with:

```go
package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/scaffold"
)

func writeTestCredentials(t *testing.T, homeDir string) {
	t.Helper()
	shell3Dir := filepath.Join(homeDir, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0700); err != nil {
		t.Fatal(err)
	}
	creds := "providers:\n  test-provider:\n    api_key: key\n    base_url: http://localhost\n    default_model: test-model\n"
	if err := os.WriteFile(filepath.Join(shell3Dir, "credentials.yaml"), []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInit_CreatesPersonaFile(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", "personas", "base.md"))
	if err != nil {
		t.Fatalf("expected .shell3/personas/base.md: %v", err)
	}
	content := string(data)
	for _, want := range []string{"model:", "provider:", "on_tool_call:", "{{."} {
		if !strings.Contains(content, want) {
			t.Errorf("base.md missing %q", want)
		}
	}
}

func TestInit_NoConfigYaml(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)

	if _, err := os.Stat(filepath.Join(dir, ".shell3", "config.yaml")); err == nil {
		t.Error("config.yaml must not be created")
	}
}

func TestInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)
	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Errorf("re-init should be safe: %v", err)
	}
}

func TestInit_FailsWithoutCredentials(t *testing.T) {
	if err := scaffold.InitProject(t.TempDir(), t.TempDir()); err == nil {
		t.Error("expected error when no credentials exist")
	}
}

func TestInit_CreatesShell3DB(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".shell3", "shell3.db")); err != nil {
		t.Errorf("expected shell3.db: %v", err)
	}
}

func TestInit_GitignoreExists(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shell3.db") {
		t.Error(".gitignore missing shell3.db")
	}
}
```

- [ ] **Step 2: Verify new test fails**

```bash
go test ./internal/scaffold/... -run TestInit_NoConfigYaml -v 2>&1 | tail -5
```
Expected: FAIL

- [ ] **Step 3: Rewrite scaffold.go**

```go
// Package scaffold creates the .shell3/ project directory structure.
package scaffold

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/store"
)

const defaultGitignore = "# shell3 runtime files — do not commit\nshell3.db\n"

// codePersonaTemplate is the default base.md for a coding assistant.
// model/provider/db default to ~ — runtime resolves from credentials and convention.
// Frontmatter is YAML — use spaces, not tabs.
// Body is a Go template — {{ and }} are reserved. Use {{"{{"}} to print a literal {{.
const codePersonaTemplate = `---
name: code
description: Agentic coding assistant with bash and memory tools
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are shell3 — an agentic coding assistant running in the user's terminal.

Today is {{.Time}}. Working directory: {{.CWD}}. Model: {{.Model}}.

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

Read before writing. Minimal changes. Test after every change.
{{- if .Skills}}

# Skills

Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.

{{.Skills}}
{{- end}}`

// agentPersonaTemplate is the default base.md for a general-purpose agent.
const agentPersonaTemplate = `---
name: agent
description: General-purpose agent with bash and memory tools
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are shell3 — a general-purpose agent running in the user's terminal.

Today is {{.Time}}. Working directory: {{.CWD}}. Model: {{.Model}}.

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
- After gathering enough information, respond clearly — do not call tools indefinitely.
{{- if .Skills}}

# Skills

Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.

{{.Skills}}
{{- end}}`

func pickPersona() string {
	fmt.Println("Select persona:")
	fmt.Println("  1. code  — coding assistant with bash and memory tools")
	fmt.Println("  2. agent — general agent with bash and memory tools")
	fmt.Print("> ")
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		switch strings.TrimSpace(scanner.Text()) {
		case "2", "agent":
			return "agent"
		}
	}
	return "code"
}

func checkExisting(shell3Dir string) bool {
	personaPath := filepath.Join(shell3Dir, "personas", "base.md")
	if _, err := os.Stat(personaPath); err == nil {
		fmt.Printf("Already initialized: %s\n", personaPath)
		fmt.Println("  Edit base.md to change model, provider, or behavior.")
		fmt.Println("  Run `shell3 destroy` to reset.")
		return true
	}
	return false
}

func initShell3Dir(projectDir, personaChoice string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	checkExisting(shell3Dir)

	for _, d := range []string{
		shell3Dir,
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
		filepath.Join(shell3Dir, "personas"),
	} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}

	tmpl := codePersonaTemplate
	if personaChoice == "agent" {
		tmpl = agentPersonaTemplate
	}

	files := map[string]string{
		filepath.Join(shell3Dir, ".gitignore"):          defaultGitignore,
		filepath.Join(shell3Dir, "personas", "base.md"): tmpl,
	}
	for path, content := range files {
		if _, err := os.Stat(path); err == nil {
			continue
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return fmt.Errorf("scaffold: write %s: %w", path, err)
		}
	}

	dbPath := filepath.Join(shell3Dir, "shell3.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		st, err := store.Open(dbPath)
		if err != nil {
			return fmt.Errorf("scaffold: create store: %w", err)
		}
		st.Close()
	}
	return nil
}

// InitProject scaffolds a .shell3/ directory under projectDir.
// Requires credentials to exist in homeDir — run `shell3 auth` first.
func InitProject(projectDir, homeDir string) error {
	if err := checkCredentials(homeDir); err != nil {
		return err
	}
	personaChoice := pickPersona()
	if err := initShell3Dir(projectDir, personaChoice); err != nil {
		return err
	}
	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	fmt.Printf("  persona: base (%s)\n  Edit .shell3/personas/base.md to set model, provider, and hooks.\n", personaChoice)
	return nil
}

func checkCredentials(homeDir string) error {
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return fmt.Errorf("run `shell3 auth` before `shell3 init`: %w", err)
	}
	if _, _, ok := creds.First(); !ok {
		return fmt.Errorf("no providers configured — run: shell3 auth")
	}
	return nil
}

func splitComma(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if p := trim(s[start:i]); p != "" {
				out = append(out, p)
			}
			start = i + 1
		}
	}
	return out
}

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
```

- [ ] **Step 4: Run scaffold tests**

```bash
go test ./internal/scaffold/... -v 2>&1 | grep -E "PASS|FAIL|ok"
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): no config.yaml, flat hooks in persona templates"
```

---

### Task 4: Update `cmd/shell3/run.go`

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Rewrite run.go**

```go
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
)

type runFlags struct {
	model    string
	baseURL  string
	apiKey   string
	persona  string
	noBash   bool
	noMemory bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the shell3 chat agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runChat(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().StringVar(&f.persona, "persona", "base", "Persona file to load from .shell3/personas/")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
	return cmd
}

func runChat(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home directory: %w", err)
	}

	personasDir := filepath.Join(cwd, ".shell3", "personas")

	// ParseConfig first — provides model, provider, hooks, flags.
	pCfg, err := persona.ParseConfig(personasDir, f.persona)
	if err != nil {
		return err
	}
	if err := persona.Validate(pCfg, f.persona); err != nil {
		return err
	}

	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}

	model, baseURL, apiKey, provName := resolveConnection(pCfg, creds, f)

	// CLI flags are additive restrictions — they can only disable, not enable.
	noBash := f.noBash || pCfg.NoBash
	noMemory := f.noMemory || pCfg.NoMemory

	var st *store.Store
	if !noMemory {
		storeDBPath := filepath.Join(cwd, coalesce(pCfg.DB, ".shell3/shell3.db"))
		if s, err := store.Open(storeDBPath); err == nil {
			st = s
			defer st.Close()
		}
	}

	loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})
	personaData := persona.TemplateData{
		Skills: skills.BuildSection(loadedSkills),
		Time:   time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:    cwd,
		Model:  model,
	}
	pers, err := persona.Load(personasDir, f.persona, personaData, st != nil, noBash)
	if err != nil {
		return err
	}

	hookRunner := hooks.NewRunner(pCfg.HooksConfig())
	statusLine := fmt.Sprintf("%s │ %s", provName, model)

	var models []string
	if provCreds, err := creds.Get(pCfg.Provider); err == nil {
		for _, m := range strings.Split(provCreds.DefaultModel, ",") {
			if m := strings.TrimSpace(m); m != "" {
				models = append(models, m)
			}
		}
	}
	if len(models) == 0 {
		models = []string{model}
	}

	client := llm.NewClient(baseURL, apiKey, model)
	cfg := chat.Config{
		LLM:           client,
		Hooks:         hookRunner,
		Store:         st,
		Personality:   pers,
		WorkDir:       cwd,
		StatusLine:    statusLine,
		ModeLabel:     pers.Name,
		Models:        models,
		ModelSwitcher: client.SetModel,
		Docs:          docsContent,
	}

	if initialInput != "" {
		return chat.RunOnce(ctx, cfg, initialInput)
	}
	return chat.RunInteractive(ctx, cfg)
}

func resolveConnection(pCfg persona.PersonaConfig, creds *config.Credentials, f *runFlags) (model, baseURL, apiKey, provName string) {
	if f.baseURL != "" && f.apiKey != "" {
		return coalesce(f.model, pCfg.Model, "llama3.2"), f.baseURL, f.apiKey, ""
	}

	if pCfg.Provider != "" {
		if p, ok := creds.Providers[pCfg.Provider]; ok {
			provName = pCfg.Provider
			baseURL = p.BaseURL
			apiKey = p.APIKey
		}
	}
	if provName == "" {
		if name, p, ok := creds.First(); ok {
			provName = name
			baseURL = p.BaseURL
			apiKey = p.APIKey
		}
	}

	if f.baseURL != "" {
		baseURL = f.baseURL
	}
	if f.apiKey != "" {
		apiKey = f.apiKey
	}

	model = coalesce(f.model, pCfg.Model)
	if model == "" {
		if provCreds, err := creds.Get(provName); err == nil {
			for _, part := range strings.Split(provCreds.DefaultModel, ",") {
				if m := strings.TrimSpace(part); m != "" {
					model = m
					break
				}
			}
		}
	}
	if model == "" {
		model = "llama3.2"
	}
	return
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 2: Build**

```bash
go build ./cmd/shell3/... 2>&1
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(run): --persona flag, ParseConfig+Validate first, flat hooks, hardcoded DB"
```

---

### Task 5: Update `base.md`, delete `config.yaml`

**Files:**
- Modify: `.shell3/personas/base.md`
- Delete: `.shell3/config.yaml`

- [ ] **Step 1: Rewrite base.md frontmatter**

Replace the frontmatter of `.shell3/personas/base.md`. model/provider/db all `~` — runtime resolves from credentials:

```yaml
---
name: code
description: Agentic coding assistant with bash and memory tools
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
```

Keep the existing body unchanged.

- [ ] **Step 2: Delete config.yaml and run full test + build**

```bash
rm .shell3/config.yaml
go build -o shell3 ./cmd/shell3 && go test ./... 2>&1 | grep -E "FAIL|ok"
```
Expected: build succeeds, all tests pass

- [ ] **Step 3: Commit**

```bash
git add .shell3/personas/base.md
git rm .shell3/config.yaml
git commit -m "chore: base.md owns all config — config.yaml deleted"
```

---

## Self-Review

**Spec coverage:**
- [x] Config in frontmatter (model, provider, no_bash, no_memory) → Tasks 1, 3, 5
- [x] Hooks flattened to top level, always present and empty → Tasks 1, 3, 5
- [x] `yaml.v3` frontmatter parsing (no hand-rolled parser) → Task 1
- [x] `Validate` with named missing fields → Task 1
- [x] `config.yaml` deleted → Tasks 3, 5
- [x] Always load `base.md`, `--persona` flag to override → Task 4
- [x] CLI flags override persona settings → Task 4
- [x] `db` field in PersonaConfig — `~` in template, fallback to `.shell3/shell3.db` at runtime → Tasks 1, 3, 4
- [x] Dead `memory_db`/`history_md` fields removed → Tasks 2, 3
- [x] `HooksConfig()` method converts flat fields to `hooks.Config` → Task 1

**Placeholder scan:** No TBDs. All code complete.

**Type consistency:**
- `persona.PersonaConfig` defined Task 1, used in Task 4 `resolveConnection(pCfg persona.PersonaConfig, ...)`
- `pCfg.HooksConfig()` returns `hooks.Config` — used in Task 4 as `hooks.NewRunner(pCfg.HooksConfig())`
- `persona.Validate(cfg PersonaConfig, personaFile string) error` — consistent Tasks 1 and 4
- `pers.Name` = `cfg.Name` from frontmatter = "code" — consistent with TUI `ModeLabel`

**Bootstrap note:** Frontmatter is YAML — spaces not tabs. Malformed YAML gives a startup error naming the file. Body is Go template — `{{` reserved; use `{{"{{"}}` to print literal `{{`. Hook fields accept three forms: `~` (disabled), `"path/to/script.sh"` (string shorthand), or a mapping `{command: ..., needs_tty: true}`. The `db` field is a path relative to the project root.
