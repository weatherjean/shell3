package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
)

func TestEnsureGlobal(t *testing.T) {
	home := t.TempDir()
	g := paths.NewGlobal(home)
	if err := bootstrap.EnsureGlobal(g); err != nil {
		t.Fatalf("EnsureGlobal: %v", err)
	}
	// Only Root should exist; no data/ dir.
	if _, err := os.Stat(g.Root); err != nil {
		t.Fatalf("Root dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, "data")); !os.IsNotExist(err) {
		t.Fatalf("EnsureGlobal must NOT create data/; stat err = %v", err)
	}
	// EnsureGlobal must NOT write shell3.lua or .env.example.
	if _, err := os.Stat(filepath.Join(g.Root, "shell3.lua")); !os.IsNotExist(err) {
		t.Fatalf("EnsureGlobal must not write shell3.lua; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".env.example")); !os.IsNotExist(err) {
		t.Fatalf("EnsureGlobal must not write .env.example; stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(g.Root, ".gitignore")); err != nil {
		t.Fatalf("gitignore missing: %v", err)
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
	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	// EnsureGlobal no longer writes shell3.lua or .env.example.
	for _, path := range []string{
		filepath.Join(g.Root, "shell3.lua"),
		filepath.Join(g.Root, ".env.example"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("EnsureGlobal must not write %s; stat err = %v", path, err)
		}
	}

	// .shell3_project/ and .shell3_project/runs/ must exist.
	if _, err := os.Stat(l.Root); err != nil {
		t.Fatalf(".shell3_project/ missing: %v", err)
	}
	if _, err := os.Stat(l.Runs); err != nil {
		t.Fatalf(".shell3_project/runs/ missing: %v", err)
	}
}

func TestEnsureProject(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	_ = os.MkdirAll(cwd, 0755)
	l := paths.NewLocal(cwd)

	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("EnsureProject: %v", err)
	}

	if _, err := os.Stat(l.Root); err != nil {
		t.Fatalf(".shell3_project/ missing: %v", err)
	}
	if _, err := os.Stat(l.Runs); err != nil {
		t.Fatalf(".shell3_project/runs/ missing: %v", err)
	}

	// .shell3_project/ self-ignores via a "*" .gitignore written inside it.
	gi, err := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if err != nil {
		t.Fatalf(".shell3_project/.gitignore missing: %v", err)
	}
	if !hasLine(string(gi), "*") {
		t.Fatalf(".shell3_project/.gitignore missing '*' entry:\n%s", gi)
	}
	// The enclosing repo's own .gitignore is NOT touched (self-contained).
	if _, err := os.Stat(filepath.Join(cwd, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("cwd/.gitignore should not be created by EnsureProject; stat err = %v", err)
	}

	// No .ref file, no .shell3/ subdir.
	if _, err := os.Stat(filepath.Join(cwd, ".shell3")); !os.IsNotExist(err) {
		t.Fatalf(".shell3/ should not exist; stat err = %v", err)
	}
}

func TestEnsureProjectIdempotent(t *testing.T) {
	tmp := t.TempDir()
	cwd := filepath.Join(tmp, "project")
	_ = os.MkdirAll(cwd, 0755)
	l := paths.NewLocal(cwd)

	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("EnsureProject 1: %v", err)
	}
	if err := bootstrap.EnsureProject(l); err != nil {
		t.Fatalf("EnsureProject 2: %v", err)
	}

	// "*" must appear exactly once in .shell3_project/.gitignore.
	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if n := strings.Count(string(gi), "*"); n != 1 {
		t.Errorf("'*' appears %d times in .shell3_project/.gitignore, want 1:\n%s", n, gi)
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
	// data/ must NOT appear (no more SQLite).
	if strings.Contains(content, "data/") {
		t.Errorf("global .gitignore must not contain data/:\n%s", content)
	}
	for _, want := range []string{
		".env",
		"shell3.log",
		"shell3.log.*",
		"proxy-*.log",
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

// hasLine reports whether content contains want as its own whole trimmed line.
func hasLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
