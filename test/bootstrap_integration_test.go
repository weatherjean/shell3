package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
	"github.com/weatherjean/shell3/internal/scaffold"
)

// TestBootstrap_FullFlow simulates a first-run bootstrap with an isolated
// tmp home and tmp workdir, then asserts the complete resulting filesystem.
// No LLM or running services required.
func TestBootstrap_FullFlow(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)

	// ── global bootstrap ──────────────────────────────────────────────────────
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}

	globalDirs := []string{g.Root, g.Skills, g.Tools, g.Hooks, g.Personas, g.Projects}
	for _, dir := range globalDirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("global dir missing: %s", dir)
		}
	}

	// Default persona and example tool written to global personas/tools.
	personaPath := filepath.Join(g.Personas, scaffold.DefaultPersonaName+".md")
	if data, err := os.ReadFile(personaPath); err != nil {
		t.Errorf("global %s.md missing: %v", scaffold.DefaultPersonaName, err)
	} else {
		if !strings.Contains(string(data), "name: "+scaffold.DefaultPersonaName) {
			t.Errorf("persona frontmatter missing name: %s", scaffold.DefaultPersonaName)
		}
		if !strings.Contains(string(data), "{{.") {
			t.Error("persona has no template injection tags")
		}
	}
	if _, err := os.Stat(filepath.Join(g.Tools, "brave_search.yaml")); err != nil {
		t.Error("global brave_search.yaml missing")
	}

	// ── project bootstrap ─────────────────────────────────────────────────────
	uuid, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if uuid == "" {
		t.Fatal("EnsureProject returned empty uuid")
	}

	localDirs := []string{l.Root, l.Skills, l.Tools, l.Hooks, l.Personas}
	for _, dir := range localDirs {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("local dir missing: %s", dir)
		}
	}

	// .ref must exist and round-trip the uuid.
	loaded, err := ref.Load(l)
	if err != nil {
		t.Fatalf("ref.Load: %v", err)
	}
	if loaded != uuid {
		t.Errorf("ref mismatch: got %q want %q", loaded, uuid)
	}

	// .gitignore must contain .ref.
	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if !strings.Contains(string(gi), ".ref") {
		t.Error(".gitignore missing .ref entry")
	}

	// Project state dir must exist under ~/.shell3/projects/<uuid>/.
	proj := paths.NewProject(g, uuid)
	if _, err := os.Stat(proj.Dir); err != nil {
		t.Errorf("project state dir missing: %s", proj.Dir)
	}

	// ── idempotency ───────────────────────────────────────────────────────────
	uuid2, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("second EnsureProject: %v", err)
	}
	if uuid2 != uuid {
		t.Errorf("not idempotent: %q vs %q", uuid, uuid2)
	}

	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("second EnsureGlobal: %v", err)
	}
	data, _ := os.ReadFile(personaPath)
	if !strings.Contains(string(data), "name: "+scaffold.DefaultPersonaName) {
		t.Error("EnsureGlobal overwrote existing persona on second call")
	}
}
