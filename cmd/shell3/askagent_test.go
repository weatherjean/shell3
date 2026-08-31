//go:build unix

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestFindJob(t *testing.T) {
	jobs := []shell3.JobInfo{
		{ID: "bg1", Cmd: "sleep 1"},
		{ID: "sub2", Agent: "drafter", Summary: "SUBJECT: hi"},
	}
	got, ok := findJob(jobs, "sub2")
	if !ok {
		t.Fatal("sub2 not found")
	}
	if got.Summary != "SUBJECT: hi" {
		t.Fatalf("wrong job: %+v", got)
	}
	if _, ok := findJob(jobs, "sub9"); ok {
		t.Fatal("sub9 must not be found")
	}
}

func TestWaitForDispatchWaitsForChildClose(t *testing.T) {
	var calls int
	jobs := func() []shell3.JobInfo {
		calls++
		switch {
		case calls < 3:
			return []shell3.JobInfo{{ID: "sub1"}}
		case calls < 5:
			return []shell3.JobInfo{{ID: "sub1", Done: true, ChildOpen: true, Summary: "partial"}}
		default:
			return []shell3.JobInfo{{ID: "sub1", Done: true, Summary: "final"}}
		}
	}
	events := make(chan shell3.JobProgress)
	info, err := waitForDispatch(context.Background(), events, jobs, "sub1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Summary != "final" {
		t.Fatalf("returned before the child closed: %+v", info)
	}
}

func TestWaitForDispatchWakesOnEvent(t *testing.T) {
	done := make(chan struct{})
	jobs := func() []shell3.JobInfo {
		select {
		case <-done:
			return []shell3.JobInfo{{ID: "sub1", Done: true, Summary: "ok"}}
		default:
			return []shell3.JobInfo{{ID: "sub1"}}
		}
	}
	events := make(chan shell3.JobProgress, 1)
	close(done)
	events <- shell3.JobProgress{JobID: "sub1", Done: true}

	start := time.Now()
	info, err := waitForDispatch(context.Background(), events, jobs, "sub1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Summary != "ok" {
		t.Fatalf("wrong info: %+v", info)
	}
	if elapsed := time.Since(start); elapsed > dispatchPollInterval {
		t.Fatalf("event did not shorten the wait (took %s)", elapsed)
	}
}

// A cancelled context (SIGINT) unblocks the wait instead of hanging until the
// job finishes on its own.
func TestWaitForDispatchCancels(t *testing.T) {
	jobs := func() []shell3.JobInfo { return []shell3.JobInfo{{ID: "sub1"}} }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := waitForDispatch(ctx, make(chan shell3.JobProgress), jobs, "sub1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// --agent is a scripted single shot: without a message there is no interactive
// form to fall back to, so it must refuse rather than block on a huh prompt.
func TestAskAgentRequiresMessage(t *testing.T) {
	cmd := newAskCommand()
	cmd.SetArgs([]string{"--agent", "drafter"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--agent needs a message") {
		t.Fatalf("want a needs-a-message error, got %v", err)
	}
}

func TestAskAgentRejectsResume(t *testing.T) {
	cmd := newAskCommand()
	cmd.SetArgs([]string{"--agent", "drafter", "-p", "draft it", "--resume"})
	cmd.SilenceUsage, cmd.SilenceErrors = true, true
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "cannot --resume") {
		t.Fatalf("want a resume-refusal error, got %v", err)
	}
}
