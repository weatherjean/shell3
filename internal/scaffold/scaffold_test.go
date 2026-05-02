package scaffold_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/scaffold"
)

func TestWriteDefaults_CreatesPersona(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(personasDir, 0755)
	os.MkdirAll(toolsDir, 0755)

	if err := scaffold.WriteDefaults(personasDir, toolsDir, filepath.Join(dir, "skills"), filepath.Join(dir, "hooks")); err != nil {
		t.Fatalf("WriteDefaults: %v", err)
	}

	personaPath := filepath.Join(personasDir, scaffold.DefaultPersonaName+".md")
	data, err := os.ReadFile(personaPath)
	if err != nil {
		t.Fatalf("persona file missing: %v", err)
	}
	if !strings.Contains(string(data), "{{.") {
		t.Error("persona has no template injection tags")
	}
	if !strings.Contains(string(data), "name: base") {
		t.Error("persona missing name: base frontmatter")
	}
}

func TestWriteDefaults_CreatesExampleTool(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(personasDir, 0755)
	os.MkdirAll(toolsDir, 0755)

	if err := scaffold.WriteDefaults(personasDir, toolsDir, filepath.Join(dir, "skills"), filepath.Join(dir, "hooks")); err != nil {
		t.Fatalf("WriteDefaults: %v", err)
	}

	toolPath := filepath.Join(toolsDir, "brave_search.yaml")
	if _, err := os.Stat(toolPath); err != nil {
		t.Fatalf("brave_search.yaml missing: %v", err)
	}
}

func TestWriteDefaults_CreatesDefaultToolsSkillsAndHooks(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	toolsDir := filepath.Join(dir, "tools")
	skillsDir := filepath.Join(dir, "skills")
	hooksDir := filepath.Join(dir, "hooks")

	if err := scaffold.WriteDefaults(personasDir, toolsDir, skillsDir, hooksDir); err != nil {
		t.Fatalf("WriteDefaults: %v", err)
	}

	for _, path := range []string{
		filepath.Join(toolsDir, "brave_search.yaml"),
		filepath.Join(toolsDir, "web_fetch.yaml"),
		filepath.Join(skillsDir, "codebase-discovery.md"),
		filepath.Join(skillsDir, "writing-plans.md"),
		filepath.Join(skillsDir, "executing-plans.md"),
		filepath.Join(skillsDir, "web-search.md"),
		filepath.Join(hooksDir, "confirm-bash.sh"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("default missing: %s: %v", path, err)
		}
	}

	info, err := os.Stat(filepath.Join(hooksDir, "confirm-bash.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0111 == 0 {
		t.Fatalf("confirm-bash.sh is not executable: mode %s", info.Mode())
	}
}

func TestWriteDefaults_Idempotent(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(personasDir, 0755)
	os.MkdirAll(toolsDir, 0755)

	scaffold.WriteDefaults(personasDir, toolsDir, filepath.Join(dir, "skills"), filepath.Join(dir, "hooks"))

	// Modify the persona file.
	personaPath := filepath.Join(personasDir, scaffold.DefaultPersonaName+".md")
	os.WriteFile(personaPath, []byte("custom content"), 0644)

	if err := scaffold.WriteDefaults(personasDir, toolsDir, filepath.Join(dir, "skills"), filepath.Join(dir, "hooks")); err != nil {
		t.Fatalf("second WriteDefaults: %v", err)
	}

	// Custom content should be preserved.
	data, _ := os.ReadFile(personaPath)
	if string(data) != "custom content" {
		t.Error("WriteDefaults overwrote existing persona file")
	}
}

func TestWriteDefaults_PersonaHasNullModelDefaults(t *testing.T) {
	dir := t.TempDir()
	personasDir := filepath.Join(dir, "personas")
	toolsDir := filepath.Join(dir, "tools")
	os.MkdirAll(personasDir, 0755)
	os.MkdirAll(toolsDir, 0755)

	scaffold.WriteDefaults(personasDir, toolsDir, filepath.Join(dir, "skills"), filepath.Join(dir, "hooks"))

	data, _ := os.ReadFile(filepath.Join(personasDir, scaffold.DefaultPersonaName+".md"))
	for _, field := range []string{"model: ~", "provider: ~", "db: ~"} {
		if !strings.Contains(string(data), field) {
			t.Errorf("persona missing null default %q", field)
		}
	}
}
