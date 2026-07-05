package luacfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSkillConfig writes shell3.lua plus sibling files into one temp dir and
// returns the config path. files maps a relative path (under the config dir) to
// its contents.
func writeSkillConfig(t *testing.T, lua string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const skillHdr = `shell3.model("m", { base_url="http://x", api_key="k", model="id" })` + "\n"

func TestSkillPathResolvesToAbs(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
local h = shell3.skill({ name="history", description="d", path="lib/skills/history.md" })
shell3.agent({ name="code", model="m", prompt="p", skills={ h } })
`, map[string]string{"lib/skills/history.md": "the history body\n"})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	want := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if c.Skills[0].Path != want {
		t.Fatalf("Path = %q, want abs %q", c.Skills[0].Path, want)
	}
}

func TestSkillMissingPathFileErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d", path="lib/skills/nope.md" })
`, nil)
	if _, err := Load(p); err == nil {
		t.Fatal("missing skill file should fail Load")
	}
}

func TestSkillEmptyPathFileErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d", path="empty.md" })
`, map[string]string{"empty.md": "   \n"})
	if _, err := Load(p); err == nil {
		t.Fatal("empty skill file should fail Load")
	}
}

func TestSkillNoPathErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d" })
`, nil)
	if _, err := Load(p); err == nil {
		t.Fatal("skill with no path should error")
	}
}

func TestSkillIndexUsesAbsPath(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
local h = shell3.skill({ name="history", description="query sqlite", path="lib/skills/history.md" })
shell3.agent({ name="code", model="m", prompt="BASE", skills={ h } })
`, map[string]string{"lib/skills/history.md": "body\n"})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	persona := c.BuildPersonaFor(c.Agents()[0])
	abs := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if !strings.Contains(persona, "cat") {
		t.Fatalf("persona missing cat guidance:\n%s", persona)
	}
	if !strings.Contains(persona, "- history ("+abs+"): query sqlite") {
		t.Fatalf("persona missing path-indexed skill line:\n%s", persona)
	}
	if strings.Contains(persona, "`skill` tool") {
		t.Fatalf("persona still references the removed skill tool:\n%s", persona)
	}
}
