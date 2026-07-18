package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// minYAML is the smallest valid shell3.yaml (one model).
const minYAML = `models:
  m1:
    base_url: https://api.example.com/v1
    api_key: k
    model: test-model
`

// minAgent is the smallest valid agent.md.
const minAgent = `---
model: m1
tools: [bash]
---
You are a test agent.
`

// writeTree writes a minimal valid config tree plus the given extra files
// (path → content, paths relative to dir, subdirs created).
func writeTree(t *testing.T, dir string, extra map[string]string) {
	t.Helper()
	if _, ok := extra["shell3.yaml"]; !ok {
		writeFile(t, dir, "shell3.yaml", minYAML)
	}
	if _, ok := extra["agent.md"]; !ok {
		writeFile(t, dir, "agent.md", minAgent)
	}
	for name, body := range extra {
		writeFile(t, dir, name, body)
	}
}

// mustLoad writes a minimal tree (plus extras) and loads it. Fatal on error.
func mustLoad(t *testing.T, extra map[string]string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, extra)
	c, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// loadErr writes a minimal tree (plus extras) and expects Load to fail,
// returning the error message.
func loadErr(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, extra)
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected load error, got nil")
	}
	return err.Error()
}
