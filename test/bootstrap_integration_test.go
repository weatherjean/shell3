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

	for _, dir := range []string{g.Root, g.Data} {
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("global dir missing: %s", dir)
		}
	}

	// EnsureGlobal no longer auto-writes a starter config; shell3.lua and
	// .env.example are created explicitly by `shell3 boot`.
	if _, err := os.Stat(filepath.Join(g.Root, "shell3.lua")); !os.IsNotExist(err) {
		t.Errorf("EnsureGlobal must not write shell3.lua; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".env.example")); !os.IsNotExist(err) {
		t.Errorf("EnsureGlobal must not write .env.example; stat err = %v", err)
	}

	// It must write the global .gitignore protecting credentials and logs.
	ggi, err := os.ReadFile(filepath.Join(g.Root, ".gitignore"))
	if err != nil {
		t.Errorf("global .gitignore missing: %v", err)
	} else if !strings.Contains(string(ggi), ".env") {
		t.Error("global .gitignore missing .env entry")
	}

	// ── project bootstrap ─────────────────────────────────────────────────────
	uuid, err := bootstrap.EnsureProject(l)
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

	// .gitignore must ignore the whole folder via "*".
	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if !strings.Contains(string(gi), "*") {
		t.Error(".gitignore missing \"*\" entry")
	}

	// Canonical data dir lives at <home>/.shell3/data/ (single shared DB location).
	if _, err := os.Stat(g.Data); err != nil {
		t.Errorf("canonical data dir missing: %s", g.Data)
	}

	// ── idempotency ───────────────────────────────────────────────────────────
	uuid2, err := bootstrap.EnsureProject(l)
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
