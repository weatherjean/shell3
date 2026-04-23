package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestLoadProjectConfig(t *testing.T) {
	dir := t.TempDir()
	shell3Dir := filepath.Join(dir, ".shell3")
	os.MkdirAll(shell3Dir, 0755)

	yaml := `
model: llama3.2
provider: ollama
default_personality: coder
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ".shell3/hooks/guard.sh"
`
	os.WriteFile(filepath.Join(shell3Dir, "config.yaml"), []byte(yaml), 0644)

	cfg, err := config.LoadProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "llama3.2" {
		t.Errorf("got model %q, want llama3.2", cfg.Model)
	}
	if cfg.Hooks.OnToolCall != ".shell3/hooks/guard.sh" {
		t.Errorf("got hook %q", cfg.Hooks.OnToolCall)
	}
}

func TestLoadProjectConfig_Missing(t *testing.T) {
	_, err := config.LoadProject(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestLoadCredentials(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, ".shell3"), 0755)

	yaml := `
providers:
  ollama:
    base_url: http://localhost:11434/v1
  openai:
    api_key: sk-test123
    base_url: https://api.openai.com/v1
`
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
	_, err := config.LoadCredentials(t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing credentials")
	}
}

func TestValidate_OK(t *testing.T) {
	cfg := &config.ProjectConfig{Model: "llama3.2", Provider: "ollama"}
	creds := &config.Credentials{
		Providers: map[string]config.ProviderCredentials{
			"ollama": {BaseURL: "http://localhost:11434/v1"},
		},
	}
	if err := config.Validate(cfg, creds); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidate_MissingProvider(t *testing.T) {
	cfg := &config.ProjectConfig{Model: "llama3.2", Provider: "openai"}
	creds := &config.Credentials{Providers: map[string]config.ProviderCredentials{}}
	if err := config.Validate(cfg, creds); err == nil {
		t.Error("expected error for missing provider credentials")
	}
}
