package bootstrap_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/bootstrap"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/ref"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/usertools"
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
	// Default persona, tools, skills, and hooks are written to global.
	if _, err := os.Stat(filepath.Join(g.Personas, "base.md")); err != nil {
		t.Fatal("global base.md missing after EnsureGlobal")
	}
	for _, path := range []string{
		filepath.Join(g.Tools, "brave_search.yaml"),
		filepath.Join(g.Tools, "web_fetch.yaml"),
		filepath.Join(g.Skills, "web-search.md"),
		filepath.Join(g.Hooks, "confirm-bash.sh"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("global default missing after EnsureGlobal: %s", path)
		}
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

	wantGlobalFiles := []string{
		filepath.Join(g.Personas, "base.md"),
		filepath.Join(g.Tools, "brave_search.yaml"),
		filepath.Join(g.Tools, "web_fetch.yaml"),
		filepath.Join(g.Skills, "codebase-discovery.md"),
		filepath.Join(g.Skills, "writing-plans.md"),
		filepath.Join(g.Skills, "executing-plans.md"),
		filepath.Join(g.Skills, "web-search.md"),
		filepath.Join(g.Hooks, "confirm-bash.sh"),
	}
	for _, path := range wantGlobalFiles {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("global default missing: %s: %v", path, err)
		}
	}
	if info, err := os.Stat(filepath.Join(g.Hooks, "confirm-bash.sh")); err != nil {
		t.Fatal(err)
	} else if info.Mode()&0111 == 0 {
		t.Fatalf("confirm-bash.sh is not executable: %s", info.Mode())
	}

	base, err := os.ReadFile(filepath.Join(g.Personas, "base.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, skill := range []string{"codebase-discovery", "writing-plans", "executing-plans", "web-search"} {
		if !strings.Contains(string(base), "- "+skill) {
			t.Fatalf("base persona does not reference default skill %q", skill)
		}
	}

	loadedSkills, err := skills.LoadAll([]string{g.Skills, l.Skills})
	if err != nil {
		t.Fatalf("load skills: %v", err)
	}
	for _, name := range []string{"codebase-discovery", "writing-plans", "executing-plans", "web-search"} {
		if !hasSkill(loadedSkills, name) {
			t.Fatalf("default skill %q was not loadable; loaded: %#v", name, loadedSkills)
		}
	}

	loadedTools, warnings, err := usertools.LoadAll([]string{g.Tools, l.Tools}, map[string]struct{}{})
	if err != nil {
		t.Fatalf("load tools: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected tool warnings: %v", warnings)
	}
	if !hasTool(loadedTools, "web_fetch") {
		t.Fatalf("default web_fetch tool was not loadable; loaded: %#v", loadedTools)
	}

	for _, dir := range []string{l.Root, l.Skills, l.Tools, l.Hooks, l.Personas} {
		if _, err := os.Stat(dir); err != nil {
			t.Fatalf("local dir missing: %s", dir)
		}
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
	_ = os.WriteFile(filepath.Join(l.Root, ".gitignore"), []byte("shell3.db\nsecrets.shell3\n"), 0644)

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

func hasSkill(all []skills.Skill, name string) bool {
	for _, s := range all {
		if s.Name == name {
			return true
		}
	}
	return false
}

func hasTool(all []usertools.Tool, name string) bool {
	for _, tool := range all {
		if tool.Name == name {
			return true
		}
	}
	return false
}
