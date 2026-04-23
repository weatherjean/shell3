package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/scaffold"
)

func writeTestCredentials(t *testing.T, homeDir string) {
	t.Helper()
	shell3Dir := filepath.Join(homeDir, ".shell3")
	if err := os.MkdirAll(shell3Dir, 0700); err != nil {
		t.Fatal(err)
	}
	creds := "providers:\n  test-provider:\n    api_key: key\n    base_url: http://localhost\n    default_model: test-model\n"
	if err := os.WriteFile(filepath.Join(shell3Dir, "credentials.yaml"), []byte(creds), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestInit_CreatesShell3Dir(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
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
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)
	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Errorf("re-init should be safe: %v", err)
	}
}

func TestInit_FailsWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()

	if err := scaffold.InitProject(dir, homeDir); err == nil {
		t.Error("expected error when no credentials exist")
	}
}

func TestInit_CreatesShell3DB(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, ".shell3", "shell3.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected .shell3/shell3.db to exist: %v", err)
	}
}

func TestInit_GitignoreContainsShell3DB(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shell3.db") {
		t.Error("expected .gitignore to contain shell3.db")
	}
}
