package orchestrator

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/lispconfig"
)

func TestCoreToolDefinitionsAreExactlyTheOrchestratorSurface(t *testing.T) {
	defs := coreToolDefinitions()
	var names []string
	for _, def := range defs {
		names = append(names, def.Name)
	}
	if got, want := strings.Join(names, ","), "bash,bash_bg,edit_file"; got != want {
		t.Fatalf("tools = %q, want %q", got, want)
	}
}

func TestRenderSkillsIndexesEmbeddedBodiesWithoutInliningThem(t *testing.T) {
	skills := []lispconfig.Skill{{Name: "web", Description: "Search the live web", Instructions: "SECRET SKILL BODY"}}
	rendered := renderSkills(skills, "/tmp/shell3", "/tmp/shell3.lisp")
	if !strings.Contains(rendered, "web: Search the live web") || !strings.Contains(rendered, "'/tmp/shell3' config skill '/tmp/shell3.lisp' SKILL_NAME") {
		t.Fatalf("rendered skills = %q", rendered)
	}
	if strings.Contains(rendered, "SECRET SKILL BODY") {
		t.Fatalf("skill body was inlined: %q", rendered)
	}
}
