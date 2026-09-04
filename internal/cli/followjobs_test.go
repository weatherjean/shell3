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

func TestFollowAskJobs_WaitsForBackgroundJob(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{
			ID: "x", Name: "bash_bg", RawArgs: `{"command":"sleep 0.4; echo hi"}`,
		}}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "dispatched the job"},
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
	if err := renderAskEvents(&buf, sess.Send(ctx, "start a job")); err != nil {
		t.Fatalf("turn: %v", err)
	}
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

	if strings.Contains(buf.String(), "narrating") {
		t.Fatalf("background completion started a legacy follow-up turn:\n%s", buf.String())
	}
	if got := len(fake.CallsSnapshot()); got != 2 {
		t.Fatalf("model calls = %d, want only the original tool round and answer round", got)
	}
	for _, j := range sess.Jobs() {
		if !j.Done {
			t.Errorf("job %s still running after FollowAskJobs returned", j.ID)
		}
	}
}

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
	if err := renderAskEvents(&buf, sess.Send(ctx, "start a slow job")); err != nil {
		t.Fatalf("turn: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- FollowAskJobs(ctx, &buf, rt, sess) }()
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

func TestWaitForChange_ClosedBusReturnsPromptly(t *testing.T) {
	jobEvents := make(chan shell3.JobProgress)
	close(jobEvents)

	done := make(chan error, 1)
	go func() { done <- waitForChange(context.Background(), jobEvents) }()
	select {
	case err := <-done:
		if !errors.Is(err, errJobBusClosed) {
			t.Fatalf("waitForChange on a closed bus = %v, want errJobBusClosed", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("waitForChange did not return promptly on a closed bus")
	}
}

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
	if err := renderAskEvents(&buf, sess.Send(ctx, "start a slow job")); err != nil {
		t.Fatalf("turn: %v", err)
	}

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
