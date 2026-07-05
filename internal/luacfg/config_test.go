package luacfg

import (
	"path/filepath"
	"testing"
)

func TestSubagentAndBackgroundConfig(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
shell3.subagents{ max_depth = 4 }
shell3.background{ max_concurrent = 3 }
`)
	if cfg.SubagentMaxDepth != 4 {
		t.Fatalf("SubagentMaxDepth = %d, want 4", cfg.SubagentMaxDepth)
	}
	if cfg.BackgroundMaxConcurrent != 3 {
		t.Fatalf("BackgroundMaxConcurrent = %d, want 3", cfg.BackgroundMaxConcurrent)
	}
}

// mustLoadFail writes script to a temp dir as shell3.lua, loads it, and
// asserts that Load returns an error. Returns the error for optional inspection.
func mustLoadFail(t *testing.T, script string) error {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", script)
	_, err := Load(filepath.Join(dir, "shell3.lua"))
	if err == nil {
		t.Fatal("expected Load to fail, but it succeeded")
	}
	return err
}

const minCfgHdr = `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p" })
`

func TestSubagentsMaxDepthZeroRejects(t *testing.T) {
	mustLoadFail(t, minCfgHdr+`shell3.subagents{ max_depth = 0 }`)
}

func TestSubagentsMaxDepthNegativeRejects(t *testing.T) {
	mustLoadFail(t, minCfgHdr+`shell3.subagents{ max_depth = -1 }`)
}

func TestSubagentsMaxDepthStringRejects(t *testing.T) {
	mustLoadFail(t, minCfgHdr+`shell3.subagents{ max_depth = "foo" }`)
}

func TestBackgroundMaxConcurrentZeroRejects(t *testing.T) {
	mustLoadFail(t, minCfgHdr+`shell3.background{ max_concurrent = 0 }`)
}
