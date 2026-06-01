package luacfg

import (
	"strings"
	"testing"
)

func TestBuildPersonaSystemPrompt(t *testing.T) {
	c := &LoadedConfig{
		Agent:  Agent{Name: "base", Prompt: "You are base.", Skills: []string{"web-search"}},
		Skills: []Skill{{Name: "web-search", Description: "search the web", Body: "..."}},
	}
	rd := RuntimeData{Time: "Mon Jun 1", CWD: "/work", Model: "m-1"}
	sp := c.BuildPersona(rd)
	for _, want := range []string{"You are base.", "/work", "m-1", "web-search", "search the web"} {
		if !strings.Contains(sp, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, sp)
		}
	}
}
