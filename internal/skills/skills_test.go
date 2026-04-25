package skills_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/skills"
)

func TestLoadSkills(t *testing.T) {
	dir := t.TempDir()
	content := "---\nname: git-workflow\ndescription: Git conventions\n---\nAlways squash before merging."
	os.WriteFile(filepath.Join(dir, "git-workflow.md"), []byte(content), 0644)

	loaded, err := skills.LoadAll([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 skill, got %d", len(loaded))
	}
	if loaded[0].Name != "git-workflow" {
		t.Errorf("got name %q", loaded[0].Name)
	}
	if loaded[0].Description != "Git conventions" {
		t.Errorf("got description %q", loaded[0].Description)
	}
	if !strings.HasSuffix(loaded[0].Path, "git-workflow.md") {
		t.Errorf("expected path to end with git-workflow.md, got %q", loaded[0].Path)
	}
}

func TestLoadSkillsSkipsNoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "no-fm.md"), []byte("just body, no frontmatter"), 0644)

	loaded, err := skills.LoadAll([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 0 {
		t.Fatalf("expected 0 skills, got %d", len(loaded))
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	s := []skills.Skill{{Name: "git", Description: "git stuff", Path: "/proj/.shell3/skills/git.md"}}
	out := skills.BuildSection(s)
	if strings.Contains(out, "# Skills") {
		t.Error("BuildSection must not include # Skills header — belongs in persona template")
	}
	if !strings.Contains(out, "## git") {
		t.Error("expected ## git entry")
	}
	if !strings.Contains(out, "/proj/.shell3/skills/git.md") {
		t.Error("expected file path")
	}
	if strings.Contains(out, "always squash") {
		t.Error("skill body must not appear")
	}
}

func TestBuildSection_EmptyReturnsEmpty(t *testing.T) {
	if got := skills.BuildSection(nil); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}
