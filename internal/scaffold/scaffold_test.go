package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/scaffold"
)

func TestWriteStarterConfig_WritesFiles(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lua")
	envExamplePath := filepath.Join(dir, ".env.example")

	if err := scaffold.WriteStarterConfig(configPath, envExamplePath); err != nil {
		t.Fatalf("WriteStarterConfig: %v", err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("shell3.lua missing: %v", err)
	}
	if !strings.Contains(string(data), "shell3.model") {
		t.Error("starter shell3.lua does not define a model")
	}

	env, err := os.ReadFile(envExamplePath)
	if err != nil {
		t.Fatalf(".env.example missing: %v", err)
	}
	for _, key := range []string{"OPENCODE_KEY", "BRAVE_API_KEY"} {
		if !strings.Contains(string(env), key) {
			t.Errorf(".env.example missing key %q", key)
		}
	}
}

func TestWriteStarterConfig_StatErrorSurfaced(t *testing.T) {
	dir := t.TempDir()

	// Create a regular file, then aim a config path *through* it. os.Stat on
	// "afile/child" returns ENOTDIR (not fs.ErrNotExist), which must be
	// surfaced rather than silently treated as "absent".
	afile := filepath.Join(dir, "afile")
	if err := os.WriteFile(afile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(afile, "shell3.lua")
	envExamplePath := filepath.Join(dir, ".env.example")

	err := scaffold.WriteStarterConfig(configPath, envExamplePath)
	if err == nil {
		t.Fatal("expected error from non-NotExist Stat, got nil")
	}
	if !strings.Contains(err.Error(), "scaffold: stat") {
		t.Errorf("expected wrapped stat error, got: %v", err)
	}
	// Nothing should have been created under the bogus path.
	if _, statErr := os.Stat(configPath); statErr == nil {
		t.Error("WriteStarterConfig created a file despite stat error")
	}
}

func TestWriteStarterConfig_Idempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lua")
	envExamplePath := filepath.Join(dir, ".env.example")

	if err := scaffold.WriteStarterConfig(configPath, envExamplePath); err != nil {
		t.Fatalf("WriteStarterConfig: %v", err)
	}

	// Modify the config file; a second call must not overwrite it.
	if err := os.WriteFile(configPath, []byte("custom content"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := scaffold.WriteStarterConfig(configPath, envExamplePath); err != nil {
		t.Fatalf("second WriteStarterConfig: %v", err)
	}

	data, _ := os.ReadFile(configPath)
	if string(data) != "custom content" {
		t.Error("WriteStarterConfig overwrote existing shell3.lua")
	}
}
