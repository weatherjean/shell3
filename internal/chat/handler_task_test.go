package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

func TestTaskToolStartsSubagent(t *testing.T) {
	var gotAgent, gotPrompt string
	cfg := ToolConfig{
		StartSubagent: func(agent, prompt, desc string, report notify.ReportMode, note string) (string, error) {
			gotAgent, gotPrompt = agent, prompt
			return "sub1", nil
		},
	}
	args := json.RawMessage(`{"subagent_type":"researcher","prompt":"find X","description":"find"}`)
	out, err := TaskHandler{}.Execute(context.Background(), "t", args, cfg)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if gotAgent != "researcher" || gotPrompt != "find X" {
		t.Fatalf("callback got (%q,%q)", gotAgent, gotPrompt)
	}
	if !strings.Contains(out, "sub1") {
		t.Fatalf("output %q missing id", out)
	}
}

func TestTaskRefusesRemovedDirectArg(t *testing.T) {
	started := false
	cfg := ToolConfig{
		StartSubagent: func(agent, prompt, desc string, report notify.ReportMode, note string) (string, error) {
			started = true
			return "sub1", nil
		},
	}
	out, err := (TaskHandler{}).Execute(context.Background(), "1",
		json.RawMessage(`{"subagent_type":"x","prompt":"p","direct":true}`), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "report") || started {
		t.Fatalf("stale direct must be refused without spawning: %q started=%v", out, started)
	}
}
