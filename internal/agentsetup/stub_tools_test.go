package agentsetup_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
)

// writeStubToolsConfig writes a config that registers stub tools, including one
// ("bash") that collides with a real tool name to exercise the skip-on-collision
// precedence rule.
func writeStubToolsConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.stub_tools({
  read_file = "Use bash: cat <path>",
  bash      = "this stub collides with the real bash tool and must be skipped",
})
shell3.agent({ name = "code", model = "main", prompt = "you are a coder", tools = { bash = true } })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAgentRuntime_StubTools asserts that a registered stub appears in the
// agent's toolDefs (with a no-param object schema), in ActiveTools, and in
// CustomToolNames (so dispatch routes it through CallTool). A stub whose name
// collides with a real tool is skipped — the real tool wins.
func TestAgentRuntime_StubTools(t *testing.T) {
	tmp := t.TempDir()
	writeStubToolsConfig(t, tmp)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	rt, err := parts.AgentRuntime("code")
	if err != nil {
		t.Fatalf("AgentRuntime: %v", err)
	}

	// read_file stub must be present in the schema with a no-param object.
	hasReadFileStub := false
	bashCount := 0
	for _, td := range rt.Personality.Tools {
		switch td.Name {
		case "read_file":
			hasReadFileStub = true
			props, ok := td.Parameters["properties"].(map[string]any)
			if !ok {
				t.Fatalf("read_file stub: properties is %T, want map[string]any", td.Parameters["properties"])
			}
			if len(props) != 0 {
				t.Errorf("read_file stub should have no params, got %v", props)
			}
		case "bash":
			bashCount++
		}
	}
	if !hasReadFileStub {
		t.Error("read_file stub not found in Personality.Tools")
	}
	// The colliding "bash" stub must NOT add a second bash def: the real bash wins.
	if bashCount != 1 {
		t.Errorf("bash appears %d times in tool defs, want 1 (stub collision must be skipped)", bashCount)
	}

	// ActiveTools must contain read_file (the surviving stub).
	inActive := false
	for _, n := range rt.ActiveTools {
		if n == "read_file" {
			inActive = true
		}
	}
	if !inActive {
		t.Error("read_file stub not found in ActiveTools")
	}

	// CustomToolNames routes the stub through CallTool.
	if !rt.CustomToolNames["read_file"] {
		t.Error("read_file stub not in CustomToolNames")
	}
	if rt.CustomToolNames["bash"] {
		t.Error("colliding bash stub must not be added to CustomToolNames")
	}
}
