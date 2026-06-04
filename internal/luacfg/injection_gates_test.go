package luacfg

import (
	"path/filepath"
	"testing"
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
