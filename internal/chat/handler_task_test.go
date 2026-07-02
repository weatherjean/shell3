package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestTaskToolStartsSubagent(t *testing.T) {
	var gotAgent, gotPrompt string
	cfg := ToolConfig{
		StartSubagent: func(agent, prompt, desc string) (string, error) {
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
