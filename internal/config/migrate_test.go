package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrate_FromLegacyYAML(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	yaml := `providers:
  openai-prod:
    api_key: sk-test
    base_url: https://api.openai.com/v1
    default_model: gpt-4o
  ollama-local:
    api_key: ""
    base_url: http://localhost:11434/v1
    default_model: llama3.2
`
	if err := os.WriteFile(filepath.Join(dir, "credentials.yaml"), []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore: %v", err)
	}
	adapter, fields, ok := store.Get("openai-prod")
	if !ok || adapter != "openai" || fields["api_key"] != "sk-test" {
		t.Fatalf("openai-prod not migrated: ok=%v adapter=%q fields=%v", ok, adapter, fields)
	}
	_, _, ok = store.Get("ollama-local")
	if !ok {
		t.Fatal("ollama-local not migrated")
	}

	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml.bak")); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.yaml")); !os.IsNotExist(err) {
		t.Fatalf("legacy file should have been renamed, got err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "credentials.shell3")); err != nil {
		t.Fatalf("new file missing: %v", err)
	}
}

func TestMigrate_FromLegacyCodexTokens(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(30 * time.Minute).UTC().Format(time.RFC3339Nano)
	tokens := map[string]any{
		"access_token":  "at",
		"refresh_token": "rt",
		"id_token":      "idt",
		"account_id":    "acc",
		"plan_type":     "pro",
		"expires_at":    expires,
	}
	data, _ := json.Marshal(tokens)
	if err := os.WriteFile(filepath.Join(dir, "codex_tokens.json"), data, 0600); err != nil {
		t.Fatal(err)
	}

	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	store, err := LoadCredStore(home)
	if err != nil {
		t.Fatalf("LoadCredStore: %v", err)
	}
	adapter, fields, ok := store.Get("codex")
	if !ok {
		t.Fatal("codex instance missing")
	}
	if adapter != "codex" {
		t.Fatalf("adapter=%q want codex", adapter)
	}
	for _, k := range []string{"access_token", "refresh_token", "id_token", "account_id", "plan_type", "expires_at"} {
		if fields[k] == "" {
			t.Errorf("missing field %s", k)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "codex_tokens.json")); !os.IsNotExist(err) {
		t.Errorf("codex_tokens.json should be removed after migrate")
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	home := t.TempDir()
	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate on empty home: %v", err)
	}
	if err := Migrate(home); err != nil {
		t.Fatalf("Migrate second pass: %v", err)
	}
}
