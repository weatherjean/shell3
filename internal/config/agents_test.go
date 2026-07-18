package config

import (
	"strings"
	"testing"
)

func TestParseMainAgent(t *testing.T) {
	a, err := parseMainAgent([]byte("---\nmodel: m1\ntools: [bash, bash_bg, edit, media]\nmcp: all\n---\nPrompt here.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Name != "agent" || a.ModelName != "m1" || a.Prompt != "Prompt here.\n" {
		t.Fatalf("agent = %+v", a)
	}
	g := a.Gates
	if !g.Bash || !g.BashBg || !g.Edit || !g.Media {
		t.Fatalf("gates = %+v", g)
	}
	if !a.MCPAll {
		t.Fatal("mcp: all not parsed")
	}
}

func TestParseMainAgentErrors(t *testing.T) {
	cases := map[string]string{
		"no model":       "---\ntools: [bash]\n---\nbody\n",
		"description":    "---\nmodel: m\ndescription: nope\n---\nbody\n",
		"empty body":     "---\nmodel: m\n---\n\n",
		"unknown tool":   "---\nmodel: m\ntools: [read_file]\n---\nbody\n",
		"unknown key":    "---\nmodel: m\nbogus: 1\n---\nbody\n",
		"bad mcp scalar": "---\nmodel: m\nmcp: some\n---\nbody\n",
		"no frontmatter": "just a prompt\n",
	}
	for name, in := range cases {
		if _, err := parseMainAgent([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseSubagent(t *testing.T) {
	sa, err := parseSubagentFile([]byte("---\ndescription: Explores code.\ntools: [bash]\nmcp: [github]\n---\nYou explore.\n"), "explorer", "main-model")
	if err != nil {
		t.Fatal(err)
	}
	if sa.Name != "explorer" || sa.Description != "Explores code." {
		t.Fatalf("sub = %+v", sa)
	}
	if sa.ModelName != "main-model" {
		t.Fatalf("model inherit failed: %q", sa.ModelName)
	}
	if len(sa.MCP) != 1 || sa.MCP[0] != "github" {
		t.Fatalf("mcp = %+v", sa.MCP)
	}
}

func TestParseSubagentModelOverride(t *testing.T) {
	sa, err := parseSubagentFile([]byte("---\nmodel: other\ndescription: d\n---\nbody\n"), "x", "main")
	if err != nil {
		t.Fatal(err)
	}
	if sa.ModelName != "other" {
		t.Fatalf("model = %q", sa.ModelName)
	}
}

func TestParseSubagentNeedsDescription(t *testing.T) {
	_, err := parseSubagentFile([]byte("---\ntools: [bash]\n---\nbody\n"), "x", "m")
	if err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("err = %v", err)
	}
}
