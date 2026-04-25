# File-Based Personas Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace hardcoded `code`/`agent` personality types with file-based markdown personas that support Go template injection (`{{.Skills}}`, `{{.Time}}`, `{{.CWD}}`, `{{.Model}}`), and make `init` scaffold a `base.md` persona the user edits directly.

**Architecture:** New `internal/persona` package owns loading, template rendering, and tool assembly. Personas live in `.shell3/personas/*.md` (frontmatter + Go template body). The `internal/personality` package is deleted entirely. `chat.Config.Personality` field type changes to `persona.Persona`.

**Tech Stack:** Go `text/template`, existing `llm.ToolDefinition`, `skills.BuildSection()`.

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/persona/persona.go` | Load, render, assemble tools |
| Create | `internal/persona/persona_test.go` | Unit tests for Load |
| Modify | `internal/config/config.go` | `Personality` → `Persona` field |
| Modify | `internal/scaffold/scaffold.go` | Create personas dir, write base.md, update config |
| Modify | `internal/scaffold/scaffold_test.go` | Test base.md created |
| Modify | `internal/chat/chat.go` | Swap import + field type |
| Modify | `cmd/shell3/run.go` | Use persona.Load, pass TemplateData |
| Delete | `internal/personality/personality.go` | Gone |
| Delete | `internal/personality/personality_test.go` | Gone |

---

### Task 1: Create `internal/persona` package — test first

**Files:**
- Create: `internal/persona/persona_test.go`
- Create: `internal/persona/persona.go`

- [ ] **Step 1: Write failing tests**

```go
// internal/persona/persona_test.go
package persona_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/persona"
)

func writePersona(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

const simplePersona = `---
name: base
description: Test persona
---
You are a test agent. Time: {{.Time}}. Dir: {{.CWD}}. Model: {{.Model}}.
{{- if .Skills}}
{{.Skills}}
{{- end}}`

func TestLoad_RendersTemplate(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	data := persona.TemplateData{Time: "noon", CWD: "/tmp", Model: "llama3"}
	p, err := persona.Load(dir, "base", data, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.SystemPrompt == "" {
		t.Fatal("empty system prompt")
	}
	for _, want := range []string{"noon", "/tmp", "llama3"} {
		if !contains(p.SystemPrompt, want) {
			t.Errorf("system prompt missing %q; got:\n%s", want, p.SystemPrompt)
		}
	}
}

func TestLoad_SkillsInjected(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	data := persona.TemplateData{Skills: "## MySkill\nDoes things.\n"}
	p, err := persona.Load(dir, "base", data, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(p.SystemPrompt, "MySkill") {
		t.Errorf("skills not injected; got:\n%s", p.SystemPrompt)
	}
}

func TestLoad_SkillsAbsentWhenEmpty(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if contains(p.SystemPrompt, "{{") {
		t.Errorf("unrendered template tags in output:\n%s", p.SystemPrompt)
	}
}

func TestLoad_HasBashTool(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !hasToolNamed(p.Tools, "bash") {
		t.Errorf("missing bash tool; tools: %v", toolNames(p.Tools))
	}
}

func TestLoad_NoBashDropsBashTools(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "shell_interactive"} {
		if hasToolNamed(p.Tools, name) {
			t.Errorf("noBash=true but tool %q present", name)
		}
	}
}

func TestLoad_StoreToolsIncludedWhenHasStore(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory_store", "memory_list", "memory_search", "memory_remove", "history_latest", "history_search"} {
		if !hasToolNamed(p.Tools, name) {
			t.Errorf("hasStore=true but tool %q missing", name)
		}
	}
}

func TestLoad_StoreToolsAbsentWithoutStore(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory_store", "history_latest"} {
		if hasToolNamed(p.Tools, name) {
			t.Errorf("hasStore=false but tool %q present", name)
		}
	}
}

func TestLoad_MissingFileReturnsError(t *testing.T) {
	_, err := persona.Load(t.TempDir(), "nonexistent", persona.TemplateData{}, false, false)
	if err == nil {
		t.Error("expected error for missing persona file")
	}
}

func TestLoad_InvalidTemplateReturnsError(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "bad", "---\nname: bad\n---\n{{.Unclosed")
	_, err := persona.Load(dir, "bad", persona.TemplateData{}, false, false)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestLoad_NameSet(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "base" {
		t.Errorf("got name %q, want base", p.Name)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func toolNames(tools []persona.ToolDef) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func hasToolNamed(tools []persona.ToolDef, name string) bool {
	for _, t := range tools {
		if t.Name == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run tests — verify they fail (package does not exist)**

```bash
go test ./internal/persona/... 2>&1 | head -5
```
Expected: compile error "no required module provides package"

- [ ] **Step 3: Implement `internal/persona/persona.go`**

```go
// Package persona loads markdown persona files and renders them as Go templates.
package persona

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/weatherjean/shell3/internal/llm"
)

// ToolDef is an alias so callers don't import llm directly.
type ToolDef = llm.ToolDefinition

// TemplateData holds values injected into persona template bodies.
type TemplateData struct {
	Skills string // output of skills.BuildSection
	Time   string // formatted current time
	CWD    string // working directory
	Model  string // active model name
}

// Persona holds a rendered persona ready for use in a chat session.
type Persona struct {
	Name         string
	SystemPrompt string
	Tools        []ToolDef
}

// Load reads <personasDir>/<name>.md, strips frontmatter, renders the body
// as a Go template with data, and assembles the tool list.
func Load(personasDir, name string, data TemplateData, hasStore, noBash bool) (Persona, error) {
	path := filepath.Join(personasDir, name+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return Persona{}, fmt.Errorf("persona: read %s: %w", path, err)
	}

	body := extractBody(string(raw))
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
		Name:         name,
		SystemPrompt: buf.String(),
		Tools:        tools,
	}, nil
}

// extractBody strips YAML frontmatter and returns the template body.
func extractBody(content string) string {
	if !strings.HasPrefix(content, "---") {
		return content
	}
	parts := strings.SplitN(content, "---", 3)
	if len(parts) < 3 {
		return content
	}
	return strings.TrimLeft(parts[2], "\n")
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
```

- [ ] **Step 4: Run tests — verify they pass**

```bash
go test ./internal/persona/... -v 2>&1 | tail -20
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/persona/persona.go internal/persona/persona_test.go
git commit -m "feat(persona): file-based persona package with Go template rendering"
```

---

### Task 2: Update `internal/config/config.go` — rename field

**Files:**
- Modify: `internal/config/config.go:23` (`Personality` → `Persona`)

- [ ] **Step 1: Rename field**

In `internal/config/config.go`, change:
```go
Personality string `yaml:"personality"`
```
to:
```go
Persona string `yaml:"persona"`
```

- [ ] **Step 2: Verify config test still passes**

```bash
go test ./internal/config/... -v 2>&1 | tail -10
```
Expected: PASS (the config_test.go YAML uses `default_personality` which is an unknown field — ignored silently. No test checks the Personality field value.)

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): rename personality field to persona"
```

---

### Task 3: Update scaffold — personas dir, two templates, write base.md

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Add failing test for personas/base.md creation**

Add to `internal/scaffold/scaffold_test.go`:

```go
func TestInit_CreatesPersonaFile(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	personaPath := filepath.Join(dir, ".shell3", "personas", "base.md")
	data, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("expected .shell3/personas/base.md to exist: %v", err)
	}
	// Must contain at least one template injection tag.
	if !strings.Contains(string(data), "{{.") {
		t.Error("base.md has no template injection tags")
	}
}

func TestInit_ConfigContainsPersonaField(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "persona:") {
		t.Error("config.yaml missing persona: field")
	}
}
```

- [ ] **Step 2: Run tests — verify they fail**

```bash
go test ./internal/scaffold/... -run TestInit_CreatesPersonaFile -v 2>&1
go test ./internal/scaffold/... -run TestInit_ConfigContainsPersonaField -v 2>&1
```
Expected: FAIL

- [ ] **Step 3: Rewrite scaffold.go**

Replace the entire content of `internal/scaffold/scaffold.go` with:

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
	"gopkg.in/yaml.v3"
)

const defaultGitignore = `# shell3 runtime files — do not commit
shell3.db
memory.db
history.md
`

func buildConfig(provider, model, persona string) string {
	return fmt.Sprintf(`# shell3 project configuration
model: %s
provider: %s
persona: %s
store_db: .shell3/shell3.db
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_session_start: ""
  on_session_end: ""
  on_turn_start: ""
  on_turn_end: ""
  on_tool_call: ""
  on_tool_result: ""
  on_context_build: ""
  on_error: ""
`, model, provider, persona)
}

// codePersonaTemplate is the default base.md for a coding assistant.
// Demonstrates {{.Time}}, {{.CWD}}, {{.Model}}, and {{.Skills}} injection.
const codePersonaTemplate = `---
name: base
description: Agentic coding assistant with bash and memory tools
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
{{.Skills}}
{{- end}}`

// agentPersonaTemplate is the default base.md for a general-purpose agent.
const agentPersonaTemplate = `---
name: base
description: General-purpose agent with bash and memory tools
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
{{.Skills}}
{{- end}}`

// pickPersona prompts the user to choose a persona template. Returns "code" or "agent".
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

// checkExisting returns true and prints a status message if .shell3/config.yaml already exists.
func checkExisting(shell3Dir string) (exists bool) {
	cfgPath := filepath.Join(shell3Dir, "config.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		return false
	}

	fmt.Printf("Configuration already exists: %s\n", cfgPath)

	var cfg map[string]any
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		fmt.Printf("  ✗ config.yaml is invalid YAML: %v\n", err)
		return true
	}

	required := []string{"model", "provider"}
	ok := true
	for _, key := range required {
		if v, exists := cfg[key]; !exists || v == "" {
			fmt.Printf("  ✗ missing required field: %s\n", key)
			ok = false
		}
	}
	if ok {
		fmt.Printf("  ✓ model:    %v\n", cfg["model"])
		fmt.Printf("  ✓ provider: %v\n", cfg["provider"])
	}
	fmt.Println("  Run `shell3 destroy` to reset and re-init.")
	return true
}

func initShell3Dir(projectDir, provider, model, personaChoice string) error {
	shell3Dir := filepath.Join(projectDir, ".shell3")
	if checkExisting(shell3Dir) {
		return nil
	}
	dirs := []string{
		shell3Dir,
		filepath.Join(shell3Dir, "skills"),
		filepath.Join(shell3Dir, "hooks"),
		filepath.Join(shell3Dir, "personas"),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("scaffold: mkdir %s: %w", d, err)
		}
	}

	personaBody := codePersonaTemplate
	if personaChoice == "agent" {
		personaBody = agentPersonaTemplate
	}

	files := map[string]string{
		filepath.Join(shell3Dir, "config.yaml"):               buildConfig(provider, model, "base"),
		filepath.Join(shell3Dir, ".gitignore"):                defaultGitignore,
		filepath.Join(shell3Dir, "personas", "base.md"):       personaBody,
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
	provider, model, err := firstProviderModel(homeDir)
	if err != nil {
		return err
	}
	personaChoice := pickPersona()
	if err := initShell3Dir(projectDir, provider, model, personaChoice); err != nil {
		return err
	}
	fmt.Printf("Initialized .shell3/ in %s\n", projectDir)
	fmt.Printf("  provider: %s\n  model:    %s\n  persona:  base (%s)\n", provider, model, personaChoice)
	return nil
}

// firstProviderModel loads credentials and returns the first provider name and first model.
func firstProviderModel(homeDir string) (provider, model string, err error) {
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return "", "", fmt.Errorf("run `shell3 auth` before `shell3 init`: %w", err)
	}
	name, provCreds, ok := creds.First()
	if !ok {
		return "", "", fmt.Errorf("no providers configured — run: shell3 auth")
	}
	m := provCreds.DefaultModel
	for _, part := range splitComma(m) {
		if part != "" {
			m = part
			break
		}
	}
	return name, m, nil
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

- [ ] **Step 4: Run all scaffold tests**

```bash
go test ./internal/scaffold/... -v 2>&1 | tail -20
```
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): write personas/base.md with template injection on init"
```

---

### Task 4: Update `internal/chat/chat.go` — swap import and field type

**Files:**
- Modify: `internal/chat/chat.go:12-13,28`

- [ ] **Step 1: Update import and field type**

In `internal/chat/chat.go`:

Replace:
```go
"github.com/weatherjean/shell3/internal/personality"
```
with:
```go
"github.com/weatherjean/shell3/internal/persona"
```

Replace:
```go
Personality   personality.Personality
```
with:
```go
Personality   persona.Persona
```

- [ ] **Step 2: Verify chat compiles**

```bash
go build ./internal/chat/... 2>&1
```
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add internal/chat/chat.go
git commit -m "feat(chat): use persona.Persona type in Config"
```

---

### Task 5: Update `cmd/shell3/run.go` — use persona.Load

**Files:**
- Modify: `cmd/shell3/run.go`

- [ ] **Step 1: Update imports**

Replace:
```go
"github.com/weatherjean/shell3/internal/personality"
```
with:
```go
"time"

"github.com/weatherjean/shell3/internal/persona"
```

(Keep all other imports.)

- [ ] **Step 2: Replace personality.Build block**

Replace lines 77–81:
```go
loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})
pType := personality.TypeCode
if projCfg.Personality == "agent" {
    pType = personality.TypeAgent
}
pers := personality.Build(pType, loadedSkills, st != nil, f.noBash)
```
with:
```go
loadedSkills, _ := skills.LoadAll([]string{filepath.Join(cwd, ".shell3/skills")})
personaName := coalesce(projCfg.Persona, "base")
personaData := persona.TemplateData{
    Skills: skills.BuildSection(loadedSkills),
    Time:   time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
    CWD:    cwd,
    Model:  model,
}
pers, err := persona.Load(filepath.Join(cwd, ".shell3/personas"), personaName, personaData, st != nil, f.noBash)
if err != nil {
    return err
}
```

- [ ] **Step 3: Replace modeLabel block**

Replace lines 87–91:
```go
modeLabel := "c"
switch pType {
case personality.TypeAgent:
    modeLabel = "a"
}
```
with:
```go
modeLabel := personaName
```

- [ ] **Step 4: Verify run.go compiles**

```bash
go build ./cmd/shell3/... 2>&1
```
Expected: no errors (personality import still exists but will be cleaned up in Task 6)

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(run): load file-based persona with template data injection"
```

---

### Task 6: Delete `internal/personality` package

**Files:**
- Delete: `internal/personality/personality.go`
- Delete: `internal/personality/personality_test.go`

- [ ] **Step 1: Delete the package**

```bash
rm internal/personality/personality.go internal/personality/personality_test.go
rmdir internal/personality
```

- [ ] **Step 2: Full build and test**

```bash
go build ./... 2>&1
go test ./... 2>&1 | tail -30
```
Expected: no compile errors, all tests PASS

- [ ] **Step 3: Commit**

```bash
git add -u internal/personality/
git commit -m "chore: delete personality package — replaced by persona"
```

---

## Self-Review

**Spec coverage check:**
- [x] Remove hardcoded `code`/`agent` types → Task 6 deletes personality package
- [x] New `internal/persona` package → Task 1
- [x] `init` creates `base.md` in `.shell3/personas/` → Task 3
- [x] Two init templates (code and agent) → Task 3 (`codePersonaTemplate`, `agentPersonaTemplate`)
- [x] Go template injection: `{{.Skills}}`, `{{.Time}}`, `{{.CWD}}`, `{{.Model}}` → Task 1 + Task 3
- [x] `config.yaml` uses `persona:` field → Task 2 + Task 3
- [x] `chat.Config` updated → Task 4
- [x] `run.go` updated → Task 5
- [x] No `{{.Tools}}` — tools stay as JSON API definitions, not injected into prompt

**Placeholder scan:** No TBDs, all code blocks complete.

**Type consistency:**
- `persona.Persona` defined in Task 1, used in Task 4 (chat.go field type)
- `persona.TemplateData` defined in Task 1, used in Task 5 (run.go)
- `persona.ToolDef` alias defined in Task 1, used in test helpers
- `persona.Load(personasDir, name string, data TemplateData, hasStore, noBash bool) (Persona, error)` — consistent across all tasks
