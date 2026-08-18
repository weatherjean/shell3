package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCronFile(t *testing.T) {
	j, err := parseCronFile([]byte("---\nschedule: \"@daily\"\nagent: explorer\ndirect: true\n---\nSummarize the day.\n"), "daily")
	if err != nil {
		t.Fatal(err)
	}
	if j.Name != "daily" || j.Schedule != "@daily" || j.Agent != "explorer" || !j.Direct {
		t.Fatalf("job = %+v", j)
	}
	if j.Prompt != "Summarize the day.\n" {
		t.Fatalf("prompt = %q", j.Prompt)
	}
}

func TestParseCronFileErrors(t *testing.T) {
	for name, in := range map[string]string{
		"no schedule":       "---\nagent: a\n---\nbody\n",
		"no agent, no tool": "---\nschedule: \"@daily\"\n---\nbody\n",
		"no body":           "---\nschedule: \"@daily\"\nagent: a\n---\n",
		"unknown key":       "---\nschedule: \"@daily\"\nagent: a\nprompt: inline\n---\nbody\n",
		"bad schedule":      "---\nschedule: every 5 min\nagent: a\n---\nbody\n",
	} {
		if _, err := parseCronFile([]byte(in), "x"); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseCronFile_ToolJob(t *testing.T) {
	src := []byte("---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\n---\nignored body\n")
	j, err := parseCronFile(src, "sync")
	if err != nil {
		t.Fatal(err)
	}
	if j.Tool != "sync-notion-recent" || j.Agent != "" {
		t.Fatalf("got tool=%q agent=%q", j.Tool, j.Agent)
	}
}

func TestParseCronFile_ToolJobNeedsNoBody(t *testing.T) {
	src := []byte("---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\n---\n")
	if _, err := parseCronFile(src, "sync"); err != nil {
		t.Fatalf("a tool job has no prompt; want no error, got %v", err)
	}
}

func TestParseCronFile_AgentAndToolIsAnError(t *testing.T) {
	src := []byte("---\nschedule: \"@every 30m\"\nagent: a\ntool: t\n---\nbody\n")
	_, err := parseCronFile(src, "sync")
	if err == nil || !strings.Contains(err.Error(), "exactly one of agent: or tool:") {
		t.Fatalf("want the XOR error, got %v", err)
	}
}

func TestParseCronFile_NeitherAgentNorToolIsAnError(t *testing.T) {
	src := []byte("---\nschedule: \"@every 30m\"\n---\nbody\n")
	if _, err := parseCronFile(src, "sync"); err == nil {
		t.Fatal("want an error when neither agent: nor tool: is set")
	}
}

// TestParseCronFile_ToolAndDirectIsAnError: direct: true only means
// something for an agent job (raw post, no report turn) — a tool job already
// posts its own result with no agent turn at all, so direct: true on one
// does nothing. A parser that already refuses "both agent and tool" and
// "neither" by name must not silently swallow this third invalid
// combination.
func TestParseCronFile_ToolAndDirectIsAnError(t *testing.T) {
	src := []byte("---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\ndirect: true\n---\n")
	_, err := parseCronFile(src, "sync")
	if err == nil {
		t.Fatal("want an error when tool: and direct: are both set")
	}
	if !strings.Contains(err.Error(), "direct") || !strings.Contains(err.Error(), "tool") {
		t.Fatalf("error should name both offending fields: %v", err)
	}
}

// TestLoadCronToolJobWithoutKitFails pins that a tool: cron job under a
// markdown config (no shell3.sh) is refused at Load, not left to fail
// silently on the scheduler's first tick — a markdown config has no kit for
// a tool job to resolve against.
func TestLoadCronToolJobWithoutKitFails(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"shell3.yaml":  "models:\n  m: { base_url: \"http://x\", api_key: k, model: id }\n",
		"agent.md":     "---\nmodel: m\n---\np\n",
		"cron/sync.md": "---\nschedule: \"@every 30m\"\ntool: sync-notion-recent\n---\n",
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := Load(dir)
	if err == nil {
		t.Fatal("want an error: a tool: cron job needs a kit, and this config has none")
	}
	if !strings.Contains(err.Error(), "sync.md") {
		t.Fatalf("error should name the offending job, got %v", err)
	}
}
