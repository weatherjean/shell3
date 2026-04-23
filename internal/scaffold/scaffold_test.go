package scaffold_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/scaffold"
)

func TestInit_CreatesShell3Dir(t *testing.T) {
	dir := t.TempDir()
	if err := scaffold.InitProject(dir); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(dir, ".shell3", "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		t.Errorf("expected .shell3/config.yaml to exist: %v", err)
	}

	gitignorePath := filepath.Join(dir, ".shell3", ".gitignore")
	if _, err := os.Stat(gitignorePath); err != nil {
		t.Errorf("expected .shell3/.gitignore to exist: %v", err)
	}
}

func TestInit_AlreadyExists(t *testing.T) {
	dir := t.TempDir()
	scaffold.InitProject(dir)
	if err := scaffold.InitProject(dir); err != nil {
		t.Errorf("re-init should be safe: %v", err)
	}
}
