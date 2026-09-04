package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func TestWaitForBackgroundJobs_WaitsForBackgroundJob(t *testing.T) {
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
	drainTestTurn(t, sess.Send(ctx, "start a job"))
	done := make(chan error, 1)
	go func() { done <- WaitForBackgroundJobs(ctx, &buf, rt, sess) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("follow: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("WaitForBackgroundJobs did not return after the background job completed")
	}

	if got := len(fake.CallsSnapshot()); got != 2 {
		t.Fatalf("model calls = %d, want only the original tool round and answer round", got)
	}
	if got := sess.RunningJobs(); got != 0 {
		t.Errorf("%d job(s) still running after WaitForBackgroundJobs returned", got)
	}
}

func TestWaitForBackgroundJobs_NoJobsReturnsImmediately(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "ok")
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- WaitForBackgroundJobs(context.Background(), &strings.Builder{}, rt, sess) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("WaitForBackgroundJobs blocked with no running jobs")
	}
}

func TestWaitForBackgroundJobs_CtxCancelStopsWait(t *testing.T) {
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
	drainTestTurn(t, sess.Send(ctx, "start a slow job"))
	var buf strings.Builder
	done := make(chan error, 1)
	go func() { done <- WaitForBackgroundJobs(ctx, &buf, rt, sess) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected ctx cancellation error, got nil")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitForBackgroundJobs ignored ctx cancellation")
	}
}

func drainTestTurn(t *testing.T, events <-chan shell3.Event) {
	t.Helper()
	for ev := range events {
		if ev.Kind == shell3.Error {
			t.Fatalf("turn: %v", ev.Err)
		}
	}
}
