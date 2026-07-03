package acp

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// TestPumpJobsStreamsProgressCards verifies the pumpJobs goroutine emits
// synthetic per-job tool-call cards over ACP:
//
//   - A StartToolCall (tool_call) with toolCallId == job id (e.g. "bg1")
//   - At least one UpdateToolCall(in_progress) carrying a chunk
//   - A final UpdateToolCall(completed)
//
// The test drives a real bash_bg job (echo hi) via the fake LLM so that real
// JobProgress events flow through rt.JobEvents() into pumpJobs. The synthetic
// card toolCallId starts with "bg" (e.g. "bg1"), which distinguishes it from
// the regular turn's bash_bg tool-call card (toolCallId "call_0").
func TestPumpJobsStreamsProgressCards(t *testing.T) {
	llm := newFakeLLM(t, nil,
		`tool:bash_bg:{"command":"echo hi"}`, // turn 1: model calls bash_bg
		"done",                               // turn 2: model responds after tool result
	)
	lua := fmt.Sprintf(`
shell3.model("test", { base_url = %q, api_key = "test", model = "gpt-4o", context_window = 128000 })
shell3.agent({
  name = "code",
  model = "test",
  prompt = "You are a coding assistant.",
  delegation = true,
  tools = { bash = true, bash_bg = true },
})
`, llm.URL)

	e := buildPumpEnv(t, lua)
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	_, err := e.conn.Prompt(ctx, promptRequest(sessID, "run something in the background"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	// Poll for the synthetic StartToolCall card emitted by pumpJobs.
	// The job id is assigned by the job manager as "bg1", "bg2", etc.;
	// we match by HasPrefix("bg") to be robust without hard-coding the counter.
	var jobID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range e.rec.snapshotUpdates() {
			if tc := n.Update.ToolCall; tc != nil && strings.HasPrefix(string(tc.ToolCallId), "bg") {
				jobID = string(tc.ToolCallId)
				break
			}
		}
		if jobID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if jobID == "" {
		t.Fatal("no synthetic tool_call card for the bash_bg job appeared within 5 s")
	}

	// Poll for an intermediate UpdateToolCall(in_progress) carrying a chunk from
	// the job's output ("hi" from echo hi). This is the core value of the feature.
	var foundChunk bool
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range e.rec.snapshotUpdates() {
			u := n.Update.ToolCallUpdate
			if u == nil || string(u.ToolCallId) != jobID {
				continue
			}
			if u.Status == nil || *u.Status != acpsdk.ToolCallStatusInProgress {
				continue
			}
			for _, tc := range u.Content {
				if tc.Content != nil && tc.Content.Content.Text != nil &&
					strings.Contains(tc.Content.Content.Text.Text, "hi") {
					foundChunk = true
					break
				}
			}
			if foundChunk {
				break
			}
		}
		if foundChunk {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !foundChunk {
		t.Fatalf("no tool_call_update(in_progress) with chunk text \"hi\" for job %s within 5 s; updates recorded: %d",
			jobID, len(e.rec.snapshotUpdates()))
	}

	// Poll for the completed UpdateToolCall for this job.
	var foundCompleted bool
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, n := range e.rec.snapshotUpdates() {
			u := n.Update.ToolCallUpdate
			if u != nil &&
				string(u.ToolCallId) == jobID &&
				u.Status != nil &&
				*u.Status == acpsdk.ToolCallStatusCompleted {
				foundCompleted = true
				break
			}
		}
		if foundCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !foundCompleted {
		t.Fatalf("no tool_call_update(completed) for job %s appeared within 5 s; updates recorded: %d",
			jobID, len(e.rec.snapshotUpdates()))
	}
}

// TestPumpJobsSkipsUnknownParent verifies that pumpJobsFrom emits NO updates
// when the JobProgress event's Parent is not registered in the acpAgent.
//
// Because rt.emitJob is unexported (package shell3), we cannot inject events
// via the runtime directly. Instead we call pumpJobsFrom with a test-owned
// channel — the function is the untestable core extracted from pumpJobs for
// exactly this purpose. The recorder must contain no tool_call or
// tool_call_update for the synthetic "bg99" job id.
func TestPumpJobsSkipsUnknownParent(t *testing.T) {
	llm := newFakeLLM(t, nil, "hello") // never prompted; script is a no-op
	lua := fmt.Sprintf(`
shell3.model("test", { base_url = %q, api_key = "test", model = "gpt-4o", context_window = 128000 })
shell3.agent({
  name = "code",
  model = "test",
  prompt = "You are a coding assistant.",
  tools = { bash = true, bash_bg = true },
})
`, llm.URL)

	e := buildPumpEnv(t, lua)
	a := e.getAgent(t)

	// Feed two events with a parent name that is NOT in a.byName.
	// Close the channel so pumpJobsFrom exits after draining.
	ch := make(chan shell3.JobProgress, 4)
	ch <- shell3.JobProgress{
		JobID: "bg99", Parent: "not-registered",
		Kind: shell3.JobCommand, Title: "echo skip", Chunk: "output",
	}
	ch <- shell3.JobProgress{
		JobID: "bg99", Parent: "not-registered",
		Kind: shell3.JobCommand, Title: "echo skip", Done: true,
	}
	close(ch)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.pumpJobsFrom(ctx, ch)
		close(done)
	}()

	select {
	case <-done:
		// pumpJobsFrom returned after channel closed — good.
	case <-time.After(5 * time.Second):
		t.Fatal("pumpJobsFrom did not return after channel close within 5 s")
	}

	// Assert no synthetic tool_call or tool_call_update for "bg99" appeared.
	for _, n := range e.rec.snapshotUpdates() {
		if tc := n.Update.ToolCall; tc != nil && string(tc.ToolCallId) == "bg99" {
			t.Error("pumpJobsFrom emitted a tool_call for an unknown-parent job; expected skip")
		}
		if u := n.Update.ToolCallUpdate; u != nil && string(u.ToolCallId) == "bg99" {
			t.Error("pumpJobsFrom emitted a tool_call_update for an unknown-parent job; expected skip")
		}
	}
}
