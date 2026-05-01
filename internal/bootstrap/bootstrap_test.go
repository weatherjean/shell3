package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
)

func TestEnsureGlobal(t *testing.T) {
	home := t.TempDir()
	g := paths.NewGlobal(home)
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	for _, dir := range []string{g.Skills, g.Tools, g.Hooks, g.Personas, g.Projects} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("dir missing: %s", dir)
		}
	}
	// Default persona and example tool written to global.
	if _, err := os.Stat(filepath.Join(g.Personas, "base.md")); err != nil {
		t.Fatal("global base.md missing after EnsureGlobal")
	}
	if _, err := os.Stat(filepath.Join(g.Tools, "brave_search.yaml")); err != nil {
		t.Fatal("global brave_search.yaml missing after EnsureGlobal")
	}
}

func TestEnsureProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	os.MkdirAll(cwd, 0755)

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)

	bootstrap.EnsureGlobal(g)
	id, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}

	for _, dir := range []string{l.Skills, l.Tools, l.Hooks, l.Personas} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("local dir missing: %s", dir)
		}
	}

	loaded, _ := ref.Load(l)
	if loaded != id {
		t.Fatalf("ref mismatch: %q vs %q", loaded, id)
	}

	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if !strings.Contains(string(gi), ".ref") {
		t.Fatal(".gitignore missing .ref entry")
	}
}

func TestEnsureProjectIdempotent(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	os.MkdirAll(cwd, 0755)
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)

	id1, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject 1: %v", err)
	}
	id2, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject 2: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("not idempotent: %q vs %q", id1, id2)
	}
}

func TestEnsureGitignoreAppends(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	os.MkdirAll(cwd, 0755)
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	bootstrap.EnsureGlobal(g)

	// Pre-existing gitignore
	os.MkdirAll(l.Root, 0755)
	os.WriteFile(filepath.Join(l.Root, ".gitignore"), []byte("shell3.db\nsecrets.shell3\n"), 0644)

	bootstrap.EnsureProject(l, g, cwd)

	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	content := string(gi)
	if !strings.Contains(content, ".ref") {
		t.Fatal(".ref not appended to existing .gitignore")
	}
	if !strings.Contains(content, "shell3.db") {
		t.Fatal("existing entries were lost")
	}
}
