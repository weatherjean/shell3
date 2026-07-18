//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeWebTree writes a minimal config tree plus the given extra shell3.yaml
// suffix (e.g. a web block) and returns the directory.
func writeWebTree(t *testing.T, webBlock string) string {
	t.Helper()
	dir := t.TempDir()
	yaml := "models:\n  m: { base_url: \"http://127.0.0.1:1\", api_key: k, model: m }\n" + webBlock
	if err := os.WriteFile(filepath.Join(dir, "shell3.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "agent.md"), []byte("---\nmodel: m\n---\np\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestWebCommandRequiresConfigBlock: a valid config without a web block must
// fail fast with a message pointing at the missing block.
func TestWebCommandRequiresConfigBlock(t *testing.T) {
	cfg := writeWebTree(t, "")
	cmd := newWebCommand()
	cmd.SetArgs([]string{"--config", cfg})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "web") {
		t.Fatalf("want missing shell3.web error, got %v", err)
	}
}

// TestWebCommandRequiresSecret: a web block without a secret must also fail —
// an empty secret must never mean "no auth".
func TestWebCommandRequiresSecret(t *testing.T) {
	cfg := writeWebTree(t, "web:\n  addr: \"127.0.0.1:0\"\n")
	cmd := newWebCommand()
	cmd.SetArgs([]string{"--config", cfg})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "secret") {
		t.Fatalf("want missing-secret error, got %v", err)
	}
}
