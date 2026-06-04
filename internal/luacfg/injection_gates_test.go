package luacfg

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestInjectionAndToolGatesParsed(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({
  name="full", model="opus", prompt="p",
  environment=true, core_memories=true,
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

	if !full.Environment || !full.CoreMemories {
		t.Fatalf("full: Environment=%v CoreMemories=%v, want both true", full.Environment, full.CoreMemories)
	}
	if !full.Gates.Prune || !full.Gates.Compact {
		t.Fatalf("full: Prune=%v Compact=%v, want both true", full.Gates.Prune, full.Gates.Compact)
	}
	if bare.Environment || bare.CoreMemories || bare.Gates.Prune || bare.Gates.Compact {
		t.Fatalf("bare: expected all four flags false, got env=%v mem=%v prune=%v compact=%v",
			bare.Environment, bare.CoreMemories, bare.Gates.Prune, bare.Gates.Compact)
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

func TestBuildPersonaGatesBlocks(t *testing.T) {
	rd := RuntimeData{Time: "Mon", CWD: "/work", Model: "m-1",
		CoreMemories: []store.MemoryEntry{{Key: "k", Value: "v"}}}

	// Bare agent: no Environment block, no Core memories block.
	bare := &LoadedConfig{agents: []Agent{{Name: "bare", Prompt: "ONLY PROMPT"}}}
	got := bare.BuildPersona(rd)
	if got != "ONLY PROMPT" {
		t.Fatalf("bare persona should be the verbatim prompt only, got:\n%s", got)
	}

	// Opted-in agent: both blocks present.
	full := &LoadedConfig{agents: []Agent{{Name: "full", Prompt: "P", Environment: true, CoreMemories: true}}}
	gotFull := full.BuildPersona(rd)
	for _, want := range []string{"## Environment", "/work", "m-1", "## Core memories", "k: v"} {
		if !strings.Contains(gotFull, want) {
			t.Fatalf("opted-in persona missing %q:\n%s", want, gotFull)
		}
	}
}
