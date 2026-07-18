package config

import (
	"strings"
	"testing"
)

func TestLoadReservedSubagentName(t *testing.T) {
	// agents/agent.md would silently shadow the main agent in every name
	// lookup (hooks, task dispatch), so it must fail the load.
	msg := loadErr(t, map[string]string{
		"agents/agent.md": "---\ndescription: shadow\n---\nShadow.\n",
	})
	if !strings.Contains(msg, "reserved") {
		t.Fatalf("err = %q", msg)
	}
}

func TestLoadFullTree(t *testing.T) {
	c := mustLoad(t, map[string]string{
		".env":               "KEY=val\n",
		"agents/explorer.md": "---\ndescription: explores\ntools: [bash]\n---\nExplore.\n",
		"agents/writer.md":   "---\ndescription: writes\ntools: [bash, edit]\n---\nWrite.\n",
		"skills/history.md":  "---\ndescription: search history\n---\nUse rg.\n",
		"cron/daily.md":      "---\nschedule: \"@daily\"\nagent: explorer\n---\nDo the rounds.\n",
		"heartbeat.md":       "---\nevery: 30m\n---\n- anything urgent?\n",
	})
	a := c.FirstAgent()
	if a.Name != "agent" || a.ModelName != "m1" {
		t.Fatalf("agent = %+v", a)
	}
	if len(a.Subagents) != 2 || a.Subagents[0] != "explorer" || a.Subagents[1] != "writer" {
		t.Fatalf("subagents = %v", a.Subagents)
	}
	if len(a.Skills) != 1 || a.Skills[0].Name != "history" || !strings.HasSuffix(a.Skills[0].Path, "skills/history.md") {
		t.Fatalf("skills = %+v", a.Skills)
	}
	if sa, ok := c.SubagentByName("explorer"); !ok || sa.ModelName != "m1" || len(sa.Skills) != 0 {
		t.Fatalf("explorer = %+v", sa)
	}
	if len(c.Cron()) != 1 || c.Cron()[0].Agent != "explorer" {
		t.Fatalf("cron = %+v", c.Cron())
	}
	if c.Heartbeat() == nil {
		t.Fatal("heartbeat missing")
	}
	if len(c.Warnings()) != 0 {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

func TestLoadMigrationError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", "-- old config\n")
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "shell3 boot") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadMissingPieces(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "shell3.yaml") {
		t.Fatalf("empty dir err = %v", err)
	}
	dir = t.TempDir()
	writeFile(t, dir, "shell3.yaml", minYAML)
	if _, err := Load(dir); err == nil || !strings.Contains(err.Error(), "agent.md") {
		t.Fatalf("no agent err = %v", err)
	}
}

func TestLoadCrossRefErrors(t *testing.T) {
	if msg := loadErr(t, map[string]string{
		"agent.md": "---\nmodel: ghost\n---\nbody\n",
	}); !strings.Contains(msg, "unknown model") {
		t.Fatalf("msg = %q", msg)
	}
	if msg := loadErr(t, map[string]string{
		"cron/j.md": "---\nschedule: \"@daily\"\nagent: nobody\n---\nbody\n",
	}); !strings.Contains(msg, "nobody") {
		t.Fatalf("msg = %q", msg)
	}
	if msg := loadErr(t, map[string]string{
		"agent.md": "---\nmodel: m1\nmcp: [ghost]\n---\nbody\n",
	}); !strings.Contains(msg, "mcp server") {
		t.Fatalf("msg = %q", msg)
	}
}

func TestLoadInvalidSkillWarnsNotFails(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"skills/good.md": "---\ndescription: fine\n---\nbody\n",
		"skills/bad.md":  "no frontmatter here\n",
	})
	if len(c.FirstAgent().Skills) != 1 {
		t.Fatalf("skills = %+v", c.FirstAgent().Skills)
	}
	if len(c.Warnings()) != 1 || !strings.Contains(c.Warnings()[0], "bad.md") {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

func TestLoadSecrets(t *testing.T) {
	c := mustLoad(t, map[string]string{
		".env":        "MY_KEY=s3cret\n",
		"shell3.yaml": "models:\n  m1:\n    base_url: u\n    model: x\n    api_key: env:MY_KEY\n",
	})
	m, _ := c.Model("m1")
	if m.APIKey != "s3cret" {
		t.Fatalf("api key = %q", m.APIKey)
	}
}

func TestBuildPersonaFor(t *testing.T) {
	c := mustLoad(t, map[string]string{
		"skills/history.md": "---\ndescription: search history\n---\nUse rg.\n",
	})
	p := c.BuildPersonaFor(c.FirstAgent())
	if !strings.Contains(p, "You are a test agent.") || !strings.Contains(p, "## Skills") || !strings.Contains(p, "history") {
		t.Fatalf("persona = %q", p)
	}
}
