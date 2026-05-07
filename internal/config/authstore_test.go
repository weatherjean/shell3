package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func writeAuthYAML(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, ".shell3", "ai-do-not-read.auth.yaml")
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

const testAuthYAML = `
instances:
  - name: myopenai
    type: openai
    base_url: https://api.openai.com/v1
    api_key: sk-test
    models:
      - id: gpt-4o
        context_window: 128000
      - id: o3
        context_window: 200000
  - name: ollama
    type: openai
    base_url: http://localhost:11434/v1
    models:
      - id: llama3.2
        context_window: 131072
  - name: anthropic
    type: anthropic
    api_key: ant-test
    models:
      - id: claude-sonnet-4-6
        context_window: 200000
`

func TestLoadAuthStore_MissingFile(t *testing.T) {
	dir := t.TempDir()
	store, err := config.LoadAuthStore(dir)
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if len(store.List()) != 0 {
		t.Errorf("expected empty store for missing file")
	}
}

func TestLoadAuthStore_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, err := config.LoadAuthStore(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(store.List()) != 3 {
		t.Fatalf("expected 3 instances, got %d", len(store.List()))
	}
}

func TestAuthStore_Type(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)

	oai, ok := store.Get("myopenai")
	if !ok || oai.Type != "openai" {
		t.Fatalf("openai instance: type=%q ok=%v", oai.Type, ok)
	}
	ant, ok := store.Get("anthropic")
	if !ok || ant.Type != "anthropic" {
		t.Fatalf("anthropic instance: type=%q ok=%v", ant.Type, ok)
	}
}

func TestAuthStore_Get(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)

	inst, ok := store.Get("myopenai")
	if !ok {
		t.Fatal("expected to find myopenai")
	}
	if inst.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("wrong base_url: %q", inst.BaseURL)
	}
	if inst.APIKey != "sk-test" {
		t.Errorf("wrong api_key: %q", inst.APIKey)
	}
	if len(inst.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(inst.Models))
	}
	if inst.Models[0].ID != "gpt-4o" {
		t.Errorf("wrong first model: %q", inst.Models[0].ID)
	}
	if inst.Models[0].ContextWindow != 128000 {
		t.Errorf("wrong context_window: %d", inst.Models[0].ContextWindow)
	}
}

func TestAuthStore_Get_Missing(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	_, ok := store.Get("nonexistent")
	if ok {
		t.Error("expected not found for nonexistent instance")
	}
}

func TestAuthStore_Get_NullAPIKey(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	inst, ok := store.Get("ollama")
	if !ok {
		t.Fatal("expected to find ollama")
	}
	if inst.APIKey != "" {
		t.Errorf("expected empty api_key for ollama, got %q", inst.APIKey)
	}
}

func TestAuthStore_List_Order(t *testing.T) {
	dir := t.TempDir()
	writeAuthYAML(t, dir, testAuthYAML)
	store, _ := config.LoadAuthStore(dir)
	insts := store.List()
	if insts[0].Name != "myopenai" {
		t.Errorf("expected first instance myopenai, got %q", insts[0].Name)
	}
}
