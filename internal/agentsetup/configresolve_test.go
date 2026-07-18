package agentsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigDir(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	shell3Dir := filepath.Join(home, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	// An explicit dir with shell3.yaml resolves to itself.
	explicit := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicit, "shell3.yaml"), []byte("models: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigDir(explicit, home); err != nil || got != explicit {
		t.Errorf("explicit dir: got %q err %v, want %q", got, err, explicit)
	}

	// A dir without shell3.yaml fails with a clear message.
	if _, err := ResolveConfigDir(t.TempDir(), home); err == nil || !strings.Contains(err.Error(), "shell3 boot") {
		t.Errorf("empty dir: want boot hint, got %v", err)
	}

	// A dir carrying only a legacy shell3.lua gets the migration message.
	legacy := t.TempDir()
	if err := os.WriteFile(filepath.Join(legacy, "shell3.lua"), []byte("--"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveConfigDir(legacy, home); err == nil || !strings.Contains(err.Error(), "no longer read") {
		t.Errorf("legacy: want migration error, got %v", err)
	}

	// A project-local config tree must NOT be picked up for an empty flag.
	if err := os.WriteFile(filepath.Join(cwd, "shell3.yaml"), []byte("models: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveConfigDir("", home); err == nil {
		t.Error("empty flag: expected error (cwd tree must be ignored, ~/.shell3 empty)")
	}

	// With ~/.shell3/shell3.yaml present, empty flag resolves to ~/.shell3.
	if err := os.WriteFile(filepath.Join(shell3Dir, "shell3.yaml"), []byte("models: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigDir("", home); err != nil || got != shell3Dir {
		t.Errorf("default: got %q err %v, want %q", got, err, shell3Dir)
	}
}
