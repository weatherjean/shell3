package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
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

	for _, dir := range []string{g.Root, g.Projects} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("global dir missing: %s", dir)
		}
	}

	// Starter shell3.lua + .env.example written to ~/.shell3/.
	configPath := filepath.Join(g.Root, "shell3.lua")
	if data, err := os.ReadFile(configPath); err != nil {
		t.Errorf("global shell3.lua missing: %v", err)
	} else if !strings.Contains(string(data), "shell3.model") {
		t.Error("global shell3.lua does not define a model")
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".env.example")); err != nil {
		t.Error("global .env.example missing")
	}

	// ── project bootstrap ─────────────────────────────────────────────────────
	uuid, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if uuid == "" {
		t.Fatal("EnsureProject returned empty uuid")
	}

	if _, err := os.Stat(l.Root); err != nil {
		t.Errorf("local .shell3/ missing: %s", l.Root)
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
}
