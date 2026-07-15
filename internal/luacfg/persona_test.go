package luacfg

import (
	"strings"
	"testing"
)

func TestBuildPersonaSystemPrompt(t *testing.T) {
	c := &LoadedConfig{
		agents: []Agent{{AgentCommon: AgentCommon{Name: "base", Prompt: "You are base.", Skills: []Skill{
			{Name: "web-search", Description: "search the web", Path: "/x/web.md"},
		}}}},
	}
	sp := c.BuildPersonaFor(c.FirstAgent())
	for _, want := range []string{"You are base.", "web-search", "search the web"} {
		if !strings.Contains(sp, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, sp)
		}
	}
}
