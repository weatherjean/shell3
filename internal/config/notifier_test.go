package config

import (
	"strings"
	"testing"
)

const minNotifier = `---
model: m1
---
Judge background completions. When unsure, send.
`

func TestParseNotifierFile(t *testing.T) {
	n, err := parseNotifierFile([]byte(minNotifier))
	if err != nil {
		t.Fatal(err)
	}
	if n.Name != "notifier" || n.ModelName != "m1" {
		t.Fatalf("notifier = %+v", n)
	}
	if !strings.Contains(n.Prompt, "Judge background completions") {
		t.Fatalf("prompt = %q", n.Prompt)
	}
}

func TestParseNotifierFileErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no model":    "---\n---\nbody\n",
		"no body":     "---\nmodel: m1\n---\n",
		"tools":       "---\nmodel: m1\ntools: [bash]\n---\nbody\n",
		"description": "---\nmodel: m1\ndescription: judge\n---\nbody\n",
		"context":     "---\nmodel: m1\ncontext: [notes.md]\n---\nbody\n",
		"mcp":         "---\nmodel: m1\nmcp: all\n---\nbody\n",
	} {
		if _, err := parseNotifierFile([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadNotifierOptional(t *testing.T) {
	c := mustLoad(t, nil)
	if c.Notifier() != nil {
		t.Fatal("absent notifier.md should load as nil")
	}
	c = mustLoad(t, map[string]string{"notifier.md": minNotifier})
	n := c.Notifier()
	if n == nil || n.ModelName != "m1" {
		t.Fatalf("notifier = %+v", n)
	}
}

func TestLoadNotifierUnknownModel(t *testing.T) {
	msg := loadErr(t, map[string]string{
		"notifier.md": "---\nmodel: nope\n---\nbody\n",
	})
	if !strings.Contains(msg, "nope") {
		t.Fatalf("error = %q", msg)
	}
}

func TestAgentsNotifierReserved(t *testing.T) {
	msg := loadErr(t, map[string]string{
		"agents/notifier.md": "---\nmodel: m1\ndescription: d\n---\nbody\n",
	})
	if !strings.Contains(msg, "reserved") {
		t.Fatalf("error = %q", msg)
	}
}
