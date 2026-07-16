//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWebCommandRequiresConfigBlock: a valid config without shell3.web{} must
// fail fast with a message pointing at the missing block.
func TestWebCommandRequiresConfigBlock(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "shell3.lua")
	err := os.WriteFile(cfg, []byte(`
shell3.model("m", { base_url = "http://127.0.0.1:1", api_key = "k", model = "m" })
shell3.agent{ name = "code", prompt = "p" }
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newWebCommand()
	cmd.SetArgs([]string{"--config", cfg})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "shell3.web") {
		t.Fatalf("want missing shell3.web error, got %v", err)
	}
}

// TestWebCommandRequiresSecret: a web block without a secret must also fail —
// an empty secret must never mean "no auth".
func TestWebCommandRequiresSecret(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "shell3.lua")
	err := os.WriteFile(cfg, []byte(`
shell3.model("m", { base_url = "http://127.0.0.1:1", api_key = "k", model = "m" })
shell3.agent{ name = "code", prompt = "p" }
shell3.web({ addr = "127.0.0.1:0" })
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := newWebCommand()
	cmd.SetArgs([]string{"--config", cfg})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("want missing-secret error, got %v", err)
	}
}
