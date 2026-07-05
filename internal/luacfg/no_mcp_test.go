package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// After removal, shell3.mcp must not exist and `tools = { mcp = ... }` must be
// rejected as an unknown tool key.
func TestMCPBuiltinRemoved(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfg, []byte(`shell3.mcp({ name = "x", command = "y" })`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfg); err == nil {
		t.Fatal("expected error: shell3.mcp should be undefined after removal")
	}

	cfg2 := filepath.Join(dir, "two.lua")
	body := `shell3.model("m", { base_url = "http://x/v1", api_key = "k", model = "z" })
shell3.agent({ name = "a", model = "m", prompt = "p", tools = { mcp = {} } })
`
	if err := os.WriteFile(cfg2, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(cfg2); err == nil {
		t.Fatal("expected error: tools.mcp should be rejected as an unknown key")
	}
}
