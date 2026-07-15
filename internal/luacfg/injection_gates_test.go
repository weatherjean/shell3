package luacfg

import (
	"testing"
)

func TestToolGatesParsed(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({
  name="full", model="opus", prompt="p",
  tools={ bash=true, edit=true },
})
local bare = shell3.subagent({ name="bare", description="d", model="opus", prompt="p", tools={} })
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	full := c.FirstAgent()
	bare, _ := c.SubagentByName("bare")

	if !full.Gates.Bash || !full.Gates.Edit {
		t.Fatalf("full: Bash=%v Edit=%v, want both true", full.Gates.Bash, full.Gates.Edit)
	}
	if bare.Gates.Bash || bare.Gates.Edit {
		t.Fatalf("bare: expected bash/edit false, got bash=%v edit=%v",
			bare.Gates.Bash, bare.Gates.Edit)
	}
}

func TestToolDefsGates(t *testing.T) {
	bare := ToolDefs(ToolGates{}, nil)
	if len(bare) != 0 {
		t.Fatalf("bare gates should yield 0 tool defs, got %d: %v", len(bare), bare)
	}

	with := ToolDefs(ToolGates{Bash: true, Edit: true}, nil)
	names := make(map[string]bool, len(with))
	for _, d := range with {
		names[d.Name] = true
	}
	if !names["bash"] || !names["edit_file"] {
		t.Fatalf("Bash+Edit gates should expose both tools, got %v", names)
	}

	onlyBash := ToolDefs(ToolGates{Bash: true}, nil)
	if len(onlyBash) != 1 || onlyBash[0].Name != "bash" {
		t.Fatalf("Bash-only gate should yield exactly bash, got %v", onlyBash)
	}
}

func TestBuildPersonaPromptOnly(t *testing.T) {
	// No engine-injected blocks: a skill-less agent's persona is the verbatim prompt.
	bare := &LoadedConfig{agents: []Agent{{AgentCommon: AgentCommon{Name: "bare", Prompt: "ONLY PROMPT"}}}}
	if got := bare.BuildPersonaFor(bare.FirstAgent()); got != "ONLY PROMPT" {
		t.Fatalf("bare persona should be the verbatim prompt only, got:\n%s", got)
	}
}
