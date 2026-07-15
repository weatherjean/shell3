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

const historyMD = "---\nname: history\ndescription: query past runs\n---\nthe history body\n"

func TestSkillsFromDir(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "lib/skills" } })
`, map[string]string{
		"lib/skills/history.md": historyMD,
		"lib/skills/web.md":     "---\ndescription: searches the web\n---\nbody\n",
	})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sk := c.FirstAgent().Skills
	if len(sk) != 2 {
		t.Fatalf("want 2 skills, got %+v", sk)
	}
	// os.ReadDir is sorted: history before web.
	if sk[0].Name != "history" || sk[0].Description != "query past runs" {
		t.Fatalf("bad skill[0]: %+v", sk[0])
	}
	// name defaults to the filename sans .md when frontmatter omits it.
	if sk[1].Name != "web" || sk[1].Description != "searches the web" {
		t.Fatalf("bad skill[1]: %+v", sk[1])
	}
	wantPath := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if sk[0].Path != wantPath {
		t.Fatalf("Path = %q, want abs %q", sk[0].Path, wantPath)
	}
	if len(c.Warnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", c.Warnings())
	}
}

func TestSkillIndexInPersona(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="BASE", skills={ "lib/skills" } })
`, map[string]string{"lib/skills/history.md": historyMD})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	persona := c.BuildPersonaFor(c.FirstAgent())
	abs := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if !strings.Contains(persona, "cat") {
		t.Fatalf("persona missing cat guidance:\n%s", persona)
	}
	if !strings.Contains(persona, "- history ("+abs+"): query past runs") {
		t.Fatalf("persona missing path-indexed skill line:\n%s", persona)
	}
}

func TestNoSkillsNoIndex(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="ONLY PROMPT" })
`, nil)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.BuildPersonaFor(c.FirstAgent()); got != "ONLY PROMPT" {
		t.Fatalf("skill-less persona should be the verbatim prompt, got:\n%s", got)
	}
}

func TestSubagentSkillsFromDir(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.subagent({ name="helper", description="d", model="m", prompt="p", skills={ "lib/skills" } })
shell3.agent({ name="code", model="m", prompt="p" })
`, map[string]string{"lib/skills/history.md": historyMD})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sa, _ := c.SubagentByName("helper")
	if len(sa.Skills) != 1 || sa.Skills[0].Name != "history" {
		t.Fatalf("subagent skills not resolved: %+v", sa.Skills)
	}
}

// Skills dirs resolve relative to the config dir, including ../ traversal out
// of it.
func TestSkillsDotDotDirResolves(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "history.md"), []byte(historyMD), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(root, "cfg", "shell3.lua")
	lua := skillHdr + `
shell3.agent({ name="code", model="m", prompt="p", skills={ "../skills" } })
`
	if err := os.WriteFile(cfg, []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sk := c.FirstAgent().Skills
	if len(sk) != 1 || sk[0].Name != "history" {
		t.Fatalf("../ skills dir not resolved: %+v", sk)
	}
	want := filepath.Join(root, "skills", "history.md")
	if sk[0].Path != want {
		t.Fatalf("Path = %q, want cleaned abs %q", sk[0].Path, want)
	}
}

func TestSkillsAbsoluteDir(t *testing.T) {
	skdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(skdir, "history.md"), []byte(historyMD), 0o644); err != nil {
		t.Fatal(err)
	}
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ [[`+skdir+`]] } })
`, nil)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if sk := c.FirstAgent().Skills; len(sk) != 1 || sk[0].Path != filepath.Join(skdir, "history.md") {
		t.Fatalf("absolute skills dir not resolved: %+v", sk)
	}
}

func TestSkillsMissingDirFailsLoad(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "nope" } })
`, nil)
	if _, err := Load(p); err == nil || !contains(err.Error(), "nope") {
		t.Fatalf("missing skills dir should fail Load naming it, got %v", err)
	}
}

func TestSkillsDirIsFileFailsLoad(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "skills.md" } })
`, map[string]string{"skills.md": historyMD})
	if _, err := Load(p); err == nil {
		t.Fatal("skills entry pointing at a file should fail Load")
	}
}

// Invalid skill files are skipped with a load warning — they never fail the
// load (shell3 health surfaces the warnings as errors).
func TestInvalidSkillFilesSkippedWithWarning(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "lib/skills" } })
`, map[string]string{
		"lib/skills/good.md":    historyMD,
		"lib/skills/nofront.md": "just a plain markdown file\n",
		"lib/skills/nodesc.md":  "---\nname: x\n---\nbody\n",
		"lib/skills/empty.md":   "   \n",
		"lib/skills/nobody.md":  "---\ndescription: d\n---\n   \n",
		"lib/skills/open.md":    "---\ndescription: d\nnever closed\n",
	})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sk := c.FirstAgent().Skills
	if len(sk) != 1 || sk[0].Name != "history" {
		t.Fatalf("want only the valid skill, got %+v", sk)
	}
	warns := strings.Join(c.Warnings(), "\n")
	for _, f := range []string{"nofront.md", "nodesc.md", "empty.md", "nobody.md", "open.md"} {
		if !strings.Contains(warns, f) {
			t.Errorf("warnings missing skipped file %s:\n%s", f, warns)
		}
	}
	if len(c.Warnings()) != 5 {
		t.Fatalf("want 5 warnings, got %d: %v", len(c.Warnings()), c.Warnings())
	}
}

// Non-.md entries and subdirectories are ignored silently — a README or asset
// beside the skills is not an error.
func TestNonMdEntriesIgnored(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "lib/skills" } })
`, map[string]string{
		"lib/skills/history.md":     historyMD,
		"lib/skills/README.txt":     "not a skill",
		"lib/skills/assets/x.png":   "binary-ish",
		"lib/skills/assets/deep.md": "---\ndescription: d\n---\nnot scanned (non-recursive)\n",
	})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if sk := c.FirstAgent().Skills; len(sk) != 1 {
		t.Fatalf("want 1 skill, got %+v", sk)
	}
	if len(c.Warnings()) != 0 {
		t.Fatalf("unexpected warnings: %v", c.Warnings())
	}
}

// A later duplicate name (across an agent's dirs) is skipped with a warning so
// the index never carries two entries with one name.
func TestDuplicateSkillNameSkipped(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "a", "b" } })
`, map[string]string{
		"a/history.md": historyMD,
		"b/history.md": "---\ndescription: shadowed duplicate\n---\nbody\n",
	})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sk := c.FirstAgent().Skills
	if len(sk) != 1 || sk[0].Description != "query past runs" {
		t.Fatalf("first declaration should win: %+v", sk)
	}
	if w := strings.Join(c.Warnings(), "\n"); !strings.Contains(w, "duplicate") || !strings.Contains(w, "history") {
		t.Fatalf("want a duplicate warning naming the skill, got %v", c.Warnings())
	}
}

// A typo'd entry in skills={} (anything but a string) must fail the load
// loudly — silently dropping it would yield an agent quietly missing skills.
func TestSkillsListRejectsNonString(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ { path="x" } } })
`, nil)
	if _, err := Load(p); err == nil || !contains(err.Error(), "skills") {
		t.Fatalf("non-string skills entry should fail Load, got %v", err)
	}
}

// The shell3.skill primitive is gone: calling it is a load error.
func TestShell3SkillRemoved(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d", path="x.md" })
shell3.agent({ name="code", model="m", prompt="p" })
`, nil)
	if _, err := Load(p); err == nil {
		t.Fatal("shell3.skill should no longer exist")
	}
}

// The tools = { skill = ... } gate is gone: the key is now unknown.
func TestToolsSkillKeyRejected(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", tools={ skill=false } })
`, nil)
	if _, err := Load(p); err == nil || !contains(err.Error(), "skill") {
		t.Fatalf("tools.skill should be an unknown-key error, got %v", err)
	}
}

// Frontmatter values may be quoted; extra keys are ignored (forward compat).
func TestFrontmatterQuotedAndExtraKeys(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.agent({ name="code", model="m", prompt="p", skills={ "lib/skills" } })
`, map[string]string{
		"lib/skills/web.md": "---\nname: \"web-search\"\ndescription: 'finds things'\nversion: 2\n---\nbody\n",
	})
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sk := c.FirstAgent().Skills
	if len(sk) != 1 || sk[0].Name != "web-search" || sk[0].Description != "finds things" {
		t.Fatalf("quoted frontmatter not parsed: %+v", sk)
	}
}
