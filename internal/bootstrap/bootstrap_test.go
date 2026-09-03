package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
)

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

	gi, err := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if err != nil {
		t.Fatalf(".shell3_project/.gitignore missing: %v", err)
	}
	if !hasLine(string(gi), "*") {
		t.Fatalf(".shell3_project/.gitignore missing '*' entry:\n%s", gi)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("cwd/.gitignore should not be created by EnsureProject; stat err = %v", err)
	}

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

	gi, _ := os.ReadFile(filepath.Join(l.Root, ".gitignore"))
	if n := strings.Count(string(gi), "*"); n != 1 {
		t.Errorf("'*' appears %d times in .shell3_project/.gitignore, want 1:\n%s", n, gi)
	}
}

func hasLine(content, want string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}
