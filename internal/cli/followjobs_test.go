package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// TestFollowAskJobs_WaitsForBackgroundJob is the Q2 regression: a one-shot
// (`ask -p`-style) run that dispatches a background job must NOT return at turn
// end — it must stay alive until the job completes and its wake turn is
// rendered, so the in-process job's result is never silently killed by the
// process exiting.
//
// The scripted model: turn 1 launches a slow bash_bg job then replies; on the
// wake turn (RunQueued) it narrates the completion. The narration only appears
// in the output if FollowAskJobs actually waited for the wake — which is the
// behavior under test.
func TestFollowAskJobs_WaitsForBackgroundJob(t *testing.T) {
	fake := fakellm.New(
		// turn 1, round 1: launch a slow background job.
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{
			ID: "x", Name: "bash_bg", RawArgs: `{"command":"sleep 0.4; echo hi"}`,
		}}}},
		// turn 1, round 2: the assistant's reply after the tool result.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "dispatched the job"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
		// wake turn: narrate the completion the host woke us with.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "JOB-FINISHED-NARRATION"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	rt := shell3test.NewRuntimeForTestClient(t, fake)
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	var buf strings.Builder
	if err := RunAskTurn(ctx, &buf, sess, "start a job"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	// The job should still be running right after the turn (sleep 0.4).
	// FollowAskJobs must block until it completes and its wake turn renders.
	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(ctx, &buf, rt, sess) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FollowAskJobs did not return after the background job completed")
	}

	if !strings.Contains(buf.String(), "JOB-FINISHED-NARRATION") {
		t.Fatalf("wake-turn narration missing — the run did not wait for the background job:\n%s", buf.String())
	}
	// No job should still be running once FollowAskJobs returns.
	for _, j := range sess.Jobs() {
		if !j.Done {
			t.Errorf("job %s still running after FollowAskJobs returned", j.ID)
		}
	}
}

// TestFollowAskJobs_NoJobsReturnsImmediately is the negative case: with no
// running jobs and an empty inbox, FollowAskJobs returns without blocking.
func TestFollowAskJobs_NoJobsReturnsImmediately(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(context.Background(), &strings.Builder{}, rt, sess) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("FollowAskJobs blocked with no running jobs")
	}
}

// TestFollowAskJobs_CtxCancelStopsWait verifies SIGINT semantics: a cancelled
// ctx unblocks the wait (returning ctx.Err()) even while a background job is
// still running, so the run can quit on demand.
func TestFollowAskJobs_CtxCancelStopsWait(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{
			ID: "x", Name: "bash_bg", RawArgs: `{"command":"sleep 5"}`,
		}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "dispatched"}, {Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}}}},
	)
	rt := shell3test.NewRuntimeForTestClient(t, fake)
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var buf strings.Builder
	if err := RunAskTurn(ctx, &buf, sess, "start a slow job"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(ctx, &buf, rt, sess) }()
	// The job sleeps 5s; cancel well before it finishes.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected ctx cancellation error, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("FollowAskJobs ignored ctx cancellation")
	}
}

// contentRule matches a Stream call by substring(s) found in the concatenated
// content of every message in the call. Used by contentLLM below.
type contentRule struct {
	contains []string
	reply    string
	// gate, when set, blocks the reply until it is closed (or ctx is done) —
	// used to hold a turn open long enough for a test to observe state that
	// exists only in the gap before the turn completes.
	gate <-chan struct{}
}

// contentLLM is an llm.Streamer that picks its scripted response by
// inspecting message content rather than call order. Needed once a test
// spans a root session AND a subagent's child session sharing one client:
// the two call it concurrently (the child's turn runs on its own goroutine),
// so a strict per-call script index (fakellm.Script) can't be trusted to land
// on the intended turn. Falls back to emitting a bash_bg tool call (the
// child's main-turn round 1) when nothing matches.
type contentLLM struct {
	mu    sync.Mutex
	rules []contentRule
}

func (c *contentLLM) Stream(ctx context.Context, msgs []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	// Match on the LAST message only (the newly-added tool result or injected
	// notice): a child session's conversation keeps its full history, so
	// matching over the whole transcript would let an earlier turn's text
	// (e.g. "started background job" from round 2) falsely match a later
	// turn's call (e.g. round 3, the follow-up).
	text := ""
	if len(msgs) > 0 {
		text = msgs[len(msgs)-1].Content
	}

	c.mu.Lock()
	rules := c.rules
	c.mu.Unlock()
	for _, r := range rules {
		matched := true
		for _, s := range r.contains {
			if !strings.Contains(text, s) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		if r.gate != nil {
			select {
			case <-r.gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		onEvent(llm.StreamEvent{TextDelta: r.reply})
		onEvent(llm.StreamEvent{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}})
		return nil
	}
	// Default: a subagent child's very first turn — launch a background job
	// that will outlive the turn.
	onEvent(llm.StreamEvent{ToolCall: &llm.ToolCall{
		ID: "x", Name: "bash_bg", RawArgs: `{"command":"sleep 0.15; echo hi"}`,
	}})
	return nil
}

// TestFollowAskJobs_LingeringSubagentFollowUp is the race regression from the
// FollowAskJobs review: a subagent job reports Done=true at MAIN-turn end
// (jobs.go markDoneLocked) even while its child session lingers to run a
// follow-up turn for a bash_bg job that outlived the turn. The bug: once that
// bash_bg job's own JobProgress{Done:true} wakes waitForJobChange, a running
// count built from `!j.Done` alone sees the subagent as done too and returns
// early — dropping the follow-up's narration. JobInfo.ChildOpen fixes this:
// FollowAskJobs must keep waiting while a subagent job is Done but ChildOpen.
//
// The child's follow-up turn (triggered by the bash_bg completion) is gated:
// held open so the test can assert FollowAskJobs is STILL waiting at exactly
// the moment the old code would have returned, then released to let the
// follow-up complete and its agent_update notice reach — and be narrated by —
// the root session.
func TestFollowAskJobs_LingeringSubagentFollowUp(t *testing.T) {
	followupGate := make(chan struct{})
	fake := &contentLLM{rules: []contentRule{
		// child main turn, round 2: tool result for the bash_bg launch.
		{contains: []string{"started background job"}, reply: "child main answer"},
		// child follow-up turn (RunQueued over the bg_done notice): gated.
		{contains: []string{"background job", "exited"}, reply: "FOLLOWUP-NARRATION", gate: followupGate},
		// root narrating the agent_update notice from the follow-up turn.
		{contains: []string{"follow-up (background job finished)"}, reply: "ROOT-SAW-FOLLOWUP"},
		// root narrating the agent_done notice from the subagent's main turn.
		{contains: []string{"finished (done)"}, reply: "ROOT-SAW-DONE"},
	}}
	rt := shell3test.NewRuntimeForTestClient(t, fake)
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sess.Dispatch("", "do the thing", shell3.DispatchOpts{
		Description: "lingering-followup test", Direct: true,
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	ctx := context.Background()
	var buf strings.Builder
	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(ctx, &buf, rt, sess) }()

	// Give the child's main turn, its bash_bg job, and the (gated) follow-up
	// call time to reach the gate. FollowAskJobs must NOT have returned yet:
	// the subagent is Done but its child is still open running the follow-up.
	select {
	case err := <-done:
		t.Fatalf("FollowAskJobs returned before the follow-up notice was delivered (err=%v):\n%s", err, buf.String())
	case <-time.After(800 * time.Millisecond):
	}

	close(followupGate) // let the follow-up turn complete
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("FollowAskJobs did not return after the follow-up notice was narrated")
	}

	out := buf.String()
	if !strings.Contains(out, "ROOT-SAW-FOLLOWUP") {
		t.Fatalf("FollowAskJobs returned without narrating the follow-up (agent_update) notice:\n%s", out)
	}
	for _, j := range sess.Jobs() {
		if !j.Done || j.ChildOpen {
			t.Errorf("job %s still open after FollowAskJobs returned: Done=%v ChildOpen=%v", j.ID, j.Done, j.ChildOpen)
		}
	}
}

// TestWaitForChange_ClosedBusReturnsPromptly is the Minor regression: a
// closed Wake or job-progress bus can never report a future change, so
// waitForChange must return the terminal errJobBusClosed sentinel rather than
// nil (which would make FollowAskJobs loop straight back into a select on the
// same already-closed channel — a busy spin).
func TestWaitForChange_ClosedBusReturnsPromptly(t *testing.T) {
	hostEvents := make(chan shell3.HostEvent)
	close(hostEvents)
	jobEvents := make(chan shell3.JobProgress) // left open; the closed bus alone must trip it

	done := make(chan error, 1)
	go func() { done <- waitForChange(context.Background(), "sess1", hostEvents, jobEvents) }()
	select {
	case err := <-done:
		if !errors.Is(err, errJobBusClosed) {
			t.Fatalf("waitForChange on a closed bus = %v, want errJobBusClosed", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("waitForChange did not return promptly on a closed bus")
	}
}

// TestFollowAskJobs_ClosedBusNoBusySpin verifies FollowAskJobs itself treats a
// closed bus as terminal: it must return promptly after exactly ONE wait call
// rather than looping (busy-spinning) forever re-entering a wait function
// that keeps reporting the bus closed, even while a job is still (and will
// always be, in this test) reported running.
func TestFollowAskJobs_ClosedBusNoBusySpin(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{
			ID: "x", Name: "bash_bg", RawArgs: `{"command":"sleep 30"}`,
		}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "dispatched"}, {Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}}}},
	)
	rt := shell3test.NewRuntimeForTestClient(t, fake)
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var buf strings.Builder
	if err := RunAskTurn(ctx, &buf, sess, "start a slow job"); err != nil {
		t.Fatalf("turn: %v", err)
	}
	// The bash_bg job (sleep 30) is still running: sess.Jobs() reports it as
	// running for the lifetime of this test.

	var calls int
	var mu sync.Mutex
	orig := waitForJobChangeFn
	t.Cleanup(func() { waitForJobChangeFn = orig })
	waitForJobChangeFn = func(context.Context, *shell3.Runtime, *shell3.Session) error {
		mu.Lock()
		calls++
		mu.Unlock()
		return errJobBusClosed
	}

	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(ctx, &strings.Builder{}, rt, sess) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("FollowAskJobs busy-spun on a closed bus instead of returning")
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Fatalf("waitForJobChangeFn called %d times, want exactly 1 (no spin)", got)
	}
}
