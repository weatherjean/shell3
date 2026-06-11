package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfigWithFiles writes shell3.lua plus a set of sibling files in the
// same temp dir (keyed by path relative to that dir). It returns the config
// path so command-backed bodies can reference the siblings with cwd = config
// dir. Relative keys with subdirs (e.g. "skills/x.md") have their parent
// directories created.
func writeConfigWithFiles(t *testing.T, body string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAgentPromptCmdResolves(t *testing.T) {
	p := writeConfigWithFiles(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt_cmd="cat agent.md" })
`, map[string]string{"agent.md": "agent prompt text\n"})
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.FirstAgent().Prompt; got != "agent prompt text" {
		t.Fatalf("resolved prompt = %q", got)
	}
}

func TestSubagentPromptCmdResolves(t *testing.T) {
	p := writeConfigWithFiles(t, twoModelsHdr+`
local helper = shell3.subagent({ name="helper", description="d", model="opus", prompt_cmd="cat sub.md" })
shell3.agent({ name="build", model="opus", prompt="b", tools={ subagents={helper} } })
`, map[string]string{"sub.md": "subagent prompt text\n"})
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sa, ok := c.SubagentByName("helper")
	if !ok {
		t.Fatal("subagent helper not found")
	}
	if sa.Prompt != "subagent prompt text" {
		t.Fatalf("resolved subagent prompt = %q", sa.Prompt)
	}
}

func TestAgentBothPromptAndPromptCmdErrors(t *testing.T) {
	p := writeConfigWithFiles(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt="inline", prompt_cmd="cat agent.md" })
`, map[string]string{"agent.md": "f"})
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("agent with both prompt and prompt_cmd should error")
	}
}

func TestSubagentBothPromptAndPromptCmdErrors(t *testing.T) {
	p := writeConfigWithFiles(t, twoModelsHdr+`
shell3.subagent({ name="helper", description="d", model="opus", prompt="inline", prompt_cmd="cat sub.md" })
shell3.agent({ name="build", model="opus", prompt="b" })
`, map[string]string{"sub.md": "f"})
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("subagent with both prompt and prompt_cmd should error")
	}
}

func TestBodyCmdFailingCommandErrors(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt_cmd="exit 3" })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("failing prompt_cmd should error")
	}
}

func TestBodyCmdEmptyOutputErrors(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt_cmd="true" })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("empty prompt_cmd output should error")
	}
}

func TestBodyCmdCwdIsConfigDir(t *testing.T) {
	// A relative path resolves only because cwd = the config directory.
	p := writeConfigWithFiles(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt_cmd="cat skills/x.md" })
`, map[string]string{"skills/x.md": "nested prompt\n"})
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.Agents()[0].Prompt; got != "nested prompt" {
		t.Fatalf("resolved prompt = %q, want %q", got, "nested prompt")
	}
}

func TestBodyCmdReResolvesOnReload(t *testing.T) {
	p := writeConfigWithFiles(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus", prompt_cmd="cat body.md" })
`, map[string]string{"body.md": "first\n"})

	c1, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	if got := c1.Agents()[0].Prompt; got != "first" {
		t.Fatalf("first load prompt = %q", got)
	}
	c1.Close()

	// Change the sibling file; a fresh Load (the reload path) must pick it up.
	if err := os.WriteFile(filepath.Join(filepath.Dir(p), "body.md"), []byte("second\n"), 0644); err != nil {
		t.Fatal(err)
	}
	c2, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if got := c2.Agents()[0].Prompt; got != "second" {
		t.Fatalf("reloaded prompt = %q, want %q", got, "second")
	}
}
