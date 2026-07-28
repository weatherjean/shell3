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
	if len(c.Warnings()) != 0 {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

func TestLoad_ContextLiteralMissingErrors(t *testing.T) {
	// A literal (non-glob) context entry that does not exist fails the load —
	// strict tradition, same as any other dangling reference.
	msg := loadErr(t, map[string]string{
		"agent.md": "---\nmodel: m1\ncontext: [memory.md]\n---\nbody\n",
	})
	if !strings.Contains(msg, "memory.md") || !strings.Contains(msg, "context") {
		t.Fatalf("want a context/memory.md load error, got %q", msg)
	}
}

func TestLoad_ContextGlobEmptyOK(t *testing.T) {
	// A glob matching zero files is legal (the dir may fill later); the load
	// succeeds but records a warning shell3 health hardens into a failure.
	c := mustLoad(t, map[string]string{
		"agent.md": "---\nmodel: m1\ncontext: [notes/*.md]\n---\nbody\n",
	})
	if got := c.FirstAgent().Context; len(got) != 1 || got[0] != "notes/*.md" {
		t.Fatalf("agent.Context = %v, want [notes/*.md]", got)
	}
	if len(c.Warnings()) == 0 {
		t.Fatalf("zero-match context glob should produce a warning, got none")
	}
}

func TestLoad_ContextLiteralPresent(t *testing.T) {
	// A literal entry that exists loads clean and is retained verbatim.
	c := mustLoad(t, map[string]string{
		"agent.md":  "---\nmodel: m1\ncontext: [memory.md]\n---\nbody\n",
		"memory.md": "# Memory\n",
	})
	if got := c.FirstAgent().Context; len(got) != 1 || got[0] != "memory.md" {
		t.Fatalf("agent.Context = %v, want [memory.md]", got)
	}
	if len(c.Warnings()) != 0 {
		t.Fatalf("warnings = %v", c.Warnings())
	}
}

func TestLoad_SubagentContextRejected(t *testing.T) {
	// context: is a main-agent-only key; a subagent (agents/*.md) declaring it
	// fails the load.
	msg := loadErr(t, map[string]string{
		"agents/x.md": "---\ndescription: d\ncontext: [memory.md]\n---\nbody\n",
	})
	if !strings.Contains(msg, "context") {
		t.Fatalf("want a subagent-context rejection, got %q", msg)
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

func TestLoadReadListFilesTools(t *testing.T) {
	// An agent opting into read + list_files loads with both gates set, and
	// ToolDefs surfaces both tool definitions.
	c := mustLoad(t, map[string]string{
		"agent.md": "---\nmodel: m1\ntools: [read, list_files]\n---\nRead-only agent.\n",
	})
	a := c.FirstAgent()
	if !a.Gates.Read || !a.Gates.List {
		t.Fatalf("gates = %+v, want Read and List set", a.Gates)
	}
	defs := ToolDefs(a.Gates)
	var haveRead, haveList bool
	for _, d := range defs {
		switch d.Name {
		case "read":
			haveRead = true
		case "list_files":
			haveList = true
		}
	}
	if !haveRead || !haveList {
		t.Fatalf("ToolDefs = %+v, want read and list_files", defs)
	}
}

func TestLoadUnknownToolNamesValidList(t *testing.T) {
	// A misspelled tool token fails the load and the error names the full valid
	// list, including read and list_files.
	msg := loadErr(t, map[string]string{
		"agent.md": "---\nmodel: m1\ntools: [reed]\n---\nOops.\n",
	})
	if !strings.Contains(msg, `unknown tool "reed"`) {
		t.Fatalf("msg = %q", msg)
	}
	for _, want := range []string{"bash", "bash_bg", "edit", "media", "read", "list_files"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error should name %q in the valid list, got %q", want, msg)
		}
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
