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
	for _, dir := range []string{g.Root, g.Projects} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("dir missing: %s", dir)
		}
	}
	// Starter config + .env template are written to ~/.shell3/.
	cfg := filepath.Join(g.Root, "shell3.lua")
	if data, err := os.ReadFile(cfg); err != nil {
		t.Fatalf("global shell3.lua missing after EnsureGlobal: %v", err)
	} else if !strings.Contains(string(data), "shell3.model") {
		t.Error("global shell3.lua does not define a model")
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".env.example")); err != nil {
		t.Fatalf("global .env.example missing after EnsureGlobal: %v", err)
	}
}

func TestEnsureBootstrapEndToEnd(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	id, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if id == "" {
		t.Fatal("empty project id")
	}

	for _, path := range []string{
		filepath.Join(g.Root, "shell3.lua"),
		filepath.Join(g.Root, ".env.example"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("global starter file missing: %s: %v", path, err)
		}
	}

	if _, err := os.Stat(l.Root); err != nil {
		t.Fatalf("local .shell3/ missing: %v", err)
	}
	if loaded, err := ref.Load(l); err != nil {
		t.Fatalf("load ref: %v", err)
	} else if loaded != id {
		t.Fatalf("ref mismatch: %q vs %q", loaded, id)
	}
	if _, err := os.Stat(filepath.Join(g.Projects, id)); err != nil {
		t.Fatalf("project state dir missing: %v", err)
	}
}

func TestEnsureProject(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	_ = os.MkdirAll(cwd, 0755)

	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)

	_ = bootstrap.EnsureGlobal(g)
	id, err := bootstrap.EnsureProject(l, g, cwd)
	if err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}
	if id == "" {
		t.Fatal("empty uuid")
	}

	if _, err := os.Stat(l.Root); err != nil {
		t.Fatalf("local .shell3/ missing: %v", err)
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
	_ = os.MkdirAll(cwd, 0755)
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	_ = bootstrap.EnsureGlobal(g)

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

func TestGlobalGitignore(t *testing.T) {
	home := t.TempDir()
	g := paths.NewGlobal(home)
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(g.Root, ".gitignore"))
	if err != nil {
		t.Fatalf("global .gitignore missing: %v", err)
	}
	content := string(gi)
	for _, want := range []string{
		"ai-do-not-read.*",
		"shell3.log",
		"shell3.log.*",
		"projects/",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("global .gitignore missing %q:\n%s", want, content)
		}
	}

	// Idempotent: second call must not duplicate.
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal second call: %v", err)
	}
	gi2, _ := os.ReadFile(filepath.Join(g.Root, ".gitignore"))
	if strings.Count(string(gi2), "shell3.log") != strings.Count(content, "shell3.log") {
		t.Error("global .gitignore duplicated entries on second EnsureGlobal call")
	}
}

func TestEnsureGitignoreAppends(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "project")
	_ = os.MkdirAll(cwd, 0755)
	g := paths.NewGlobal(home)
	l := paths.NewLocal(cwd)
	_ = bootstrap.EnsureGlobal(g)

	// Pre-existing gitignore
	_ = os.MkdirAll(l.Root, 0755)
	_ = os.WriteFile(filepath.Join(l.Root, ".gitignore"), []byte("shell3.db\nai-do-not-read.*\n"), 0644)

	_, _ = bootstrap.EnsureProject(l, g, cwd)

	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	content := string(gi)
	if !strings.Contains(content, ".ref") {
		t.Fatal(".ref not appended to existing .gitignore")
	}
	if !strings.Contains(content, "shell3.db") {
		t.Fatal("existing entries were lost")
	}
}
