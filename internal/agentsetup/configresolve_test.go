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

	explicit := t.TempDir()
	if err := os.WriteFile(filepath.Join(explicit, "shell3.sh"), []byte("#---\n# shell3:\n#   models: {}\n#---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigDir(explicit, home); err != nil || got != explicit {
		t.Errorf("explicit dir: got %q err %v, want %q", got, err, explicit)
	}

	if _, err := ResolveConfigDir(t.TempDir(), home); err == nil || !strings.Contains(err.Error(), "shell3 boot") {
		t.Errorf("empty dir: want boot hint, got %v", err)
	}

	if err := os.WriteFile(filepath.Join(cwd, "shell3.sh"), []byte("#---\n# shell3:\n#   models: {}\n#---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveConfigDir("", home); err == nil {
		t.Error("empty flag: expected error (cwd tree must be ignored, ~/.shell3 empty)")
	}

	if err := os.WriteFile(filepath.Join(shell3Dir, "shell3.sh"), []byte("#---\n# shell3:\n#   models: {}\n#---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveConfigDir("", home); err != nil || got != shell3Dir {
		t.Errorf("default: got %q err %v, want %q", got, err, shell3Dir)
	}
}
