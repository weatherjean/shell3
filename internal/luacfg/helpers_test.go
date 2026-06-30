package luacfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
func contains(s, sub string) bool { return strings.Contains(s, sub) }

// mustLoad writes script to a temp dir as shell3.lua and loads it. Fatal on error.
func mustLoad(t *testing.T, script string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", script)
	cfg, err := Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("mustLoad: %v", err)
	}
	t.Cleanup(func() { cfg.Close() })
	return cfg
}
