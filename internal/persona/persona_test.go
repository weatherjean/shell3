package persona_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/store"
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
	p, err := persona.Load(dir, "base", data, false, false, nil)
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
	p, err := persona.Load(dir, "base", data, false, false, nil)
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

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
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

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
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

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bash", "shell_interactive", "edit_file", "write_file"} {
		if hasToolNamed(p.Tools, name) {
			t.Errorf("noBash=true but tool %q present", name)
		}
	}
}

func TestLoad_EditToolsPresentByDefault(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"edit_file", "write_file"} {
		if !hasToolNamed(p.Tools, name) {
			t.Errorf("missing %q tool; tools: %v", name, toolNames(p.Tools))
		}
	}
}

func TestLoad_StoreToolsIncludedWhenHasStore(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, true, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory_upsert", "memory_list", "memory_search", "history_get", "history_search"} {
		if !hasToolNamed(p.Tools, name) {
			t.Errorf("hasStore=true but tool %q missing", name)
		}
	}
}

func TestLoad_StoreToolsAbsentWithoutStore(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"memory_upsert", "history_get", "history_search"} {
		if hasToolNamed(p.Tools, name) {
			t.Errorf("hasStore=false but tool %q present", name)
		}
	}
}

func TestLoad_MissingFileReturnsError(t *testing.T) {
	_, err := persona.Load(t.TempDir(), "nonexistent", persona.TemplateData{}, false, false, nil)
	if err == nil {
		t.Error("expected error for missing persona file")
	}
}

func TestLoad_InvalidTemplateReturnsError(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "bad", "---\nname: bad\n---\n{{.Unclosed")
	_, err := persona.Load(dir, "bad", persona.TemplateData{}, false, false, nil)
	if err == nil {
		t.Error("expected error for invalid template")
	}
}

func TestLoad_NameSet(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", simplePersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "base" {
		t.Errorf("got name %q, want base", p.Name)
	}
}

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
	if cfg.Model != "" {
		t.Errorf("model should be empty (null); got %q", cfg.Model)
	}
	if cfg.Provider != "" {
		t.Errorf("provider should be empty (null); got %q", cfg.Provider)
	}
	if cfg.DB != "" {
		t.Errorf("db should be empty (null); got %q", cfg.DB)
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
	cfg := persona.PersonaConfig{Name: "code"}
	if err := persona.Validate(cfg, "base"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_EmptyPersonaStillOK(t *testing.T) {
	if err := persona.Validate(persona.PersonaConfig{}, "base"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestLoad_ConfigEmbedded(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", fullPersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{Time: "now"}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Config.OnToolCall.Command != ".shell3/hooks/guard.sh" {
		t.Errorf("expected hook, got %q", p.Config.OnToolCall.Command)
	}
}

func TestLoad_NameFromFrontmatter(t *testing.T) {
	dir := t.TempDir()
	writePersona(t, dir, "base", fullPersona)

	p, err := persona.Load(dir, "base", persona.TemplateData{}, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "code" {
		t.Errorf("got name %q, want code (from frontmatter)", p.Name)
	}
}

func contains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
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

func TestLoad_CoreMemoriesRendered(t *testing.T) {
	dir := t.TempDir()
	body := `---
name: t
---
Persona body.
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}`
	writePersona(t, dir, "t", body)

	p, err := persona.Load(dir, "t", persona.TemplateData{
		CoreMemories: []store.MemoryEntry{
			{Key: "stack", Value: "Go + SQLite"},
			{Key: "style", Value: "terse"},
		},
	}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.SystemPrompt, "## Core memories") {
		t.Fatalf("expected core memories section, got:\n%s", p.SystemPrompt)
	}
	if !strings.Contains(p.SystemPrompt, "- stack: Go + SQLite") {
		t.Fatalf("expected memory line, got:\n%s", p.SystemPrompt)
	}
}

func TestParseConfigParameters(t *testing.T) {
	dir := t.TempDir()
	body := `---
name: x
parameters:
  reasoning_effort: high
  verbosity: low
  parallel_tool_calls: true
  temperature: 0.4
---
hello
`
	writePersona(t, dir, "x", body)
	cfg, err := persona.ParseConfig(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Parameters.ReasoningEffort != "high" {
		t.Fatalf("effort: %+v", cfg.Parameters)
	}
	if cfg.Parameters.Verbosity != "low" {
		t.Fatalf("verbosity: %+v", cfg.Parameters)
	}
	if cfg.Parameters.ParallelToolCalls == nil || !*cfg.Parameters.ParallelToolCalls {
		t.Fatalf("parallel: %+v", cfg.Parameters)
	}
	if cfg.Parameters.Temperature == nil || *cfg.Parameters.Temperature != 0.4 {
		t.Fatalf("temp: %+v", cfg.Parameters)
	}
}
