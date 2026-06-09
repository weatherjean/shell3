package luacfg

import (
	"path/filepath"
	"testing"
)

func TestToolGatesParsed(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({
  name="full", model="opus", prompt="p",
  tools={ prune=true, compact=true },
})
shell3.agent({ name="bare", model="opus", prompt="p", tools={} })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	agents := c.Agents()
	full, bare := agents[0], agents[1]

	if !full.Gates.Prune || !full.Gates.Compact {
		t.Fatalf("full: Prune=%v Compact=%v, want both true", full.Gates.Prune, full.Gates.Compact)
	}
	if bare.Gates.Prune || bare.Gates.Compact {
		t.Fatalf("bare: expected prune/compact false, got prune=%v compact=%v",
			bare.Gates.Prune, bare.Gates.Compact)
	}
}

func TestToolDefsGatesPruneCompact(t *testing.T) {
	bare := ToolDefs(ToolGates{}, nil, false)
	if len(bare) != 0 {
		t.Fatalf("bare gates should yield 0 tool defs, got %d: %v", len(bare), bare)
	}

	with := ToolDefs(ToolGates{Prune: true, Compact: true}, nil, false)
	names := make(map[string]bool, len(with))
	for _, d := range with {
		names[d.Name] = true
	}
	if !names["prune_tool_result"] || !names["compact_history"] {
		t.Fatalf("Prune+Compact gates should expose both tools, got %v", names)
	}

	onlyPrune := ToolDefs(ToolGates{Prune: true}, nil, false)
	if len(onlyPrune) != 1 || onlyPrune[0].Name != "prune_tool_result" {
		t.Fatalf("Prune-only gate should yield exactly prune_tool_result, got %v", onlyPrune)
	}
}

func TestBuildPersonaPromptOnly(t *testing.T) {
	// No engine-injected blocks: a skill-less agent's persona is the verbatim prompt.
	bare := &LoadedConfig{agents: []Agent{{Name: "bare", Prompt: "ONLY PROMPT"}}}
	if got := bare.BuildPersonaFor(bare.FirstAgent()); got != "ONLY PROMPT" {
		t.Fatalf("bare persona should be the verbatim prompt only, got:\n%s", got)
	}
}
