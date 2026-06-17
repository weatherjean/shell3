package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
)

// TestBootstrap_FullFlow simulates a first-run bootstrap with an isolated tmp
// home and tmp workdir, then asserts the complete resulting filesystem for the
// file-native layout. No LLM or running services required.
//
// This is the end-to-end pairing of EnsureGlobal + EnsureProject; the
// individual idempotency/gitignore details are covered by the bootstrap
// package's own unit tests, so this test focuses on the combined first-run
// shape and the absence of the old SQLite/.ref artifacts.
func TestBootstrap_FullFlow(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)

	// ── global bootstrap ──────────────────────────────────────────────────────
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}

	// g.Root must exist; there must be NO data/ dir (no SQLite anymore).
	if _, err := os.Stat(g.Root); err != nil {
		t.Errorf("global root missing: %s", g.Root)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "data")); !os.IsNotExist(err) {
		t.Errorf("EnsureGlobal must NOT create data/; stat err = %v", err)
	}

	// EnsureGlobal does not auto-write a starter config; shell3.lua and
	// .env.example are created explicitly by `shell3 boot`.
	if _, err := os.Stat(filepath.Join(g.Root, "shell3.lua")); !os.IsNotExist(err) {
		t.Errorf("EnsureGlobal must not write shell3.lua; stat err = %v", err)
	}

	// It must write the global .gitignore protecting credentials and logs.
	ggi, err := os.ReadFile(filepath.Join(g.Root, ".gitignore"))
	if err != nil {
		t.Errorf("global .gitignore missing: %v", err)
	} else if !strings.Contains(string(ggi), ".env") {
		t.Error("global .gitignore missing .env entry")
	}

	// ── project bootstrap ─────────────────────────────────────────────────────
	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// .shell3_project/ and .shell3_project/runs/ must exist.
	if _, err := os.Stat(l.Root); err != nil {
		t.Errorf("local .shell3_project/ missing: %s", l.Root)
	}
	if _, err := os.Stat(l.Runs); err != nil {
		t.Errorf(".shell3_project/runs/ missing: %s", l.Runs)
	}

	// No legacy .ref file, no .shell3/ subdir under the project.
	if _, err := os.Stat(filepath.Join(cwd, ".shell3")); !os.IsNotExist(err) {
		t.Errorf("project .shell3/ should not exist; stat err = %v", err)
	}

	// .shell3_project/ self-ignores via a "*" .gitignore inside it; the
	// enclosing repo's own .gitignore is never touched.
	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if !strings.Contains(string(gi), "*") {
		t.Errorf(".shell3_project/.gitignore missing '*' entry:\n%s", gi)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".gitignore")); !os.IsNotExist(err) {
		t.Errorf("cwd/.gitignore should not be created by EnsureProject; stat err = %v", err)
	}

	// ── idempotency ───────────────────────────────────────────────────────────
	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("second EnsureProject: %v", err)
	}
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("second EnsureGlobal: %v", err)
	}
	// "*" must appear exactly once after the repeat call.
	gi2, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if n := strings.Count(string(gi2), "*"); n != 1 {
		t.Errorf("'*' appears %d times in .shell3_project/.gitignore, want 1:\n%s", n, gi2)
	}
}
