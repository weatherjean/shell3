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
	if !strings.Contains(loaded[0].Body, "squash") {
		t.Errorf("expected body to contain content")
	}
}

func TestBuildSystemPromptSection(t *testing.T) {
	s := []skills.Skill{{Name: "git", Description: "git stuff", Body: "always squash"}}
	prompt := skills.BuildSection(s)
	if !strings.Contains(prompt, "# Skills") {
		t.Error("expected # Skills header")
	}
	if !strings.Contains(prompt, "always squash") {
		t.Error("expected skill body in prompt")
	}
}
