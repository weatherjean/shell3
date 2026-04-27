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

func TestInit_CreatesPersonaFile(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	personaPath := filepath.Join(dir, ".shell3", "personas", "base.md")
	data, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("expected .shell3/personas/base.md to exist: %v", err)
	}
	if !strings.Contains(string(data), "{{.") {
		t.Error("base.md has no template injection tags")
	}
}

func TestInit_PersonaHasNullDefaults(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", "personas", "base.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"model: ~", "provider: ~", "db: ~"} {
		if !strings.Contains(string(data), field) {
			t.Errorf("base.md missing null default %q", field)
		}
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

func TestInit_CreatesToolsDirAndExample(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)
	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		".shell3/tools",
		".shell3/tools/brave_search.yaml",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("expected %s: %v", p, err)
		}
	}
}

func TestInit_GitignoreContainsSecretsShell3(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)
	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "secrets.shell3") {
		t.Errorf("gitignore missing secrets.shell3 line:\n%s", data)
	}
}
