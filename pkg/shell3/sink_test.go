package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/sink"
)

// TestFormatNotification_AgentDone verifies the agent_done branch renders a
// short POINTER (id, status, preview, transcript path) and never inlines the
// transcript — the preview is the result to act on, and the transcript carries
// the schema-aware extraction one-liner for when it isn't enough.
//
// This still exercises sink.go's formatNotification(sink.Notification), which
// remains intact (dead-code) until Phase 7. The live socket path is covered by
// renderNotification in transport_test.go.
func TestFormatNotification_AgentDone(t *testing.T) {
	got := formatNotification(sink.Notification{
		Kind: "agent_done", ID: "explore1", Status: "ok",
		Transcript: ".shell3/agents/explore1.jsonl",
		Preview:    "Found 3 call sites in pkg/foo.",
	})
	for _, want := range []string{
		"explore1", "(ok)", "Found 3 call sites", ".shell3/agents/explore1.jsonl",
		"act on it directly", // preview framed as the answer
		"jq -rs 'map(select(.kind==\"assistant_message\"))[-1].text'", // exact extraction recipe
	} {
		if !strings.Contains(got, want) {
			t.Errorf("agent_done pointer %q missing %q", got, want)
		}
	}
	// With no preview, the message points straight at the transcript extraction.
	noPrev := formatNotification(sink.Notification{
		Kind: "agent_done", ID: "x", Status: "ok",
		Transcript: ".shell3/agents/x.jsonl",
	})
	if !strings.Contains(noPrev, "Read its result from the transcript") {
		t.Errorf("preview-less agent_done = %q, want transcript-read instruction", noPrev)
	}
	// A status-less notification still renders a sane default.
	if g := formatNotification(sink.Notification{Kind: "agent_done", ID: "x"}); !strings.Contains(g, "subagent x finished (done)") {
		t.Errorf("status-less agent_done = %q, want default 'done' status", g)
	}
}
