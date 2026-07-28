//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/scaffold"
)

// setupProjectConfig writes a minimal but loadable config directory (base
// tree + a .env with the model key) so a scaffolded project can be verified to
// round-trip through config.Load.
func setupProjectConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := scaffold.RenderBaseConfig(dir, scaffold.Values{
		Name: "main", BaseURL: "http://localhost:9999/v1", EnvKey: "MAIN_API_KEY", Model: "test-model",
	}, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	// .env carries exactly the keys `shell3 boot` writes.
	env := "MAIN_API_KEY=\nTELEGRAM_TOKEN=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(env), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}
	return dir
}

func TestProjectNewCreatesStructure(t *testing.T) {
	dir := setupProjectConfig(t)
	workdir := t.TempDir()

	var out strings.Builder
	f := &projectFlags{configDir: dir, workdir: workdir, description: "my site"}
	if err := runProjectNew(&out, "site", f); err != nil {
		t.Fatalf("runProjectNew: %v", err)
	}

	pdir := filepath.Join(dir, "projects", "site")
	pm, err := os.ReadFile(filepath.Join(pdir, "project.md"))
	if err != nil {
		t.Fatalf("read project.md: %v", err)
	}
	if !strings.Contains(string(pm), `description: "my site"`) {
		t.Errorf("project.md missing description frontmatter:\n%s", pm)
	}
	if !strings.Contains(string(pm), "workdir:") {
		t.Errorf("project.md missing workdir frontmatter:\n%s", pm)
	}
	if _, err := os.Stat(filepath.Join(pdir, "manager.md")); err != nil {
		t.Errorf("missing manager.md: %v", err)
	}
	if info, err := os.Stat(filepath.Join(pdir, "skills")); err != nil || !info.IsDir() {
		t.Errorf("skills/ dir not created: %v", err)
	}

	// projects.md index line appended (file created).
	idx, err := os.ReadFile(filepath.Join(dir, "projects.md"))
	if err != nil {
		t.Fatalf("read projects.md: %v", err)
	}
	if !strings.Contains(string(idx), "- **site** — my site") {
		t.Errorf("projects.md missing index line:\n%s", idx)
	}

	// Output carries the /reload reminder.
	if !strings.Contains(out.String(), "/reload") {
		t.Errorf("output missing /reload reminder:\n%s", out.String())
	}

	// The end-to-end payoff: the scaffolded project loads cleanly.
	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("scaffolded project failed to load: %v", err)
	}
	c.Close()
}

func TestProjectNewRefusesExisting(t *testing.T) {
	dir := setupProjectConfig(t)
	workdir := t.TempDir()
	f := &projectFlags{configDir: dir, workdir: workdir}

	if err := runProjectNew(&strings.Builder{}, "site", f); err != nil {
		t.Fatalf("first runProjectNew: %v", err)
	}
	err := runProjectNew(&strings.Builder{}, "site", f)
	if err == nil {
		t.Fatal("second run with same name should error")
	}
	if !strings.Contains(err.Error(), "project site already exists") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "project site already exists")
	}
}

func TestProjectNewCopySkills(t *testing.T) {
	dir := setupProjectConfig(t)
	workdir := t.TempDir()

	// Seed a source project's skills dir.
	srcSkills := filepath.Join(dir, "projects", "other", "skills")
	if err := os.MkdirAll(srcSkills, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcSkills, "deploy.md"), []byte("---\ndescription: d\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := &projectFlags{configDir: dir, workdir: workdir, copySkills: "other"}
	if err := runProjectNew(&strings.Builder{}, "site", f); err != nil {
		t.Fatalf("runProjectNew: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "projects", "site", "skills", "deploy.md")); err != nil {
		t.Errorf("copied skill missing: %v", err)
	}

	// copy-skills from a project with no skills dir errors.
	f2 := &projectFlags{configDir: dir, workdir: workdir, copySkills: "nope"}
	if err := runProjectNew(&strings.Builder{}, "site2", f2); err == nil {
		t.Error("copy-skills from a project without a skills dir should error")
	}
}

func TestProjectNewWorkdirValidation(t *testing.T) {
	dir := setupProjectConfig(t)

	// Non-existent workdir errors.
	f := &projectFlags{configDir: dir, workdir: filepath.Join(dir, "does-not-exist")}
	if err := runProjectNew(&strings.Builder{}, "site", f); err == nil {
		t.Error("non-existent workdir should error")
	}

	// Missing --workdir is a usage error at the cobra layer.
	cmd := newProjectCommand()
	cmd.SetArgs([]string{"new", "site", "--config", dir})
	cmd.SetOut(&strings.Builder{})
	cmd.SetErr(&strings.Builder{})
	if err := cmd.Execute(); err == nil {
		t.Error("missing --workdir should be a usage error")
	}
}
