//go:build unix

package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// fakePoster records every shell3.CompletionPost it receives — the narrow
// fake wireCronPost's tests need, standing in for the real telegram.Bot's
// PostCompletion without any Telegram wiring. A tool job's fire runs on its
// own goroutine (Scheduler.Run), so PostCompletion and a test's read of
// posts race without a lock.
type fakePoster struct {
	mu    sync.Mutex
	posts []shell3.CompletionPost
}

func (f *fakePoster) PostCompletion(p shell3.CompletionPost) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.posts = append(f.posts, p)
}

func (f *fakePoster) snapshot() []shell3.CompletionPost {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]shell3.CompletionPost(nil), f.posts...)
}

// TestWireCronPost pins the composed shape that reaches the host: a tool
// job's own post arrives at PostCompletion completely unmodified (a straight
// passthrough) — the reviewed defect was a host-side prefix collision, so
// this test exists specifically to catch a regression of that shape.
func TestWireCronPost(t *testing.T) {
	fd, ft := &fakeCronDispatcher{}, &fakeCronToolRunner{result: "3 rows updated"}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1h", Tool: "sync-notion-recent"}}
	sched, err := cron.New(fd, ft, jobs)
	if err != nil {
		t.Fatal(err)
	}

	fp := &fakePoster{}
	wireCronPost(sched, fp)
	if err := sched.Run("sync"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool { return len(fp.snapshot()) == 1 })

	got := fp.snapshot()[0]
	if got.CronJob != "sync" {
		t.Errorf("CronJob = %q, want the job name (so PostCompletion adds exactly one ⏰ prefix)", got.CronJob)
	}
	if !strings.Contains(got.Text, "3 rows updated") {
		t.Errorf("Text = %q, want the tool result", got.Text)
	}
	if strings.Contains(got.Text, "sync") {
		t.Errorf("Text = %q, must not embed the job name — CronJob already carries it, doubling it here reads as the shipped defect (🔔 ⏰ sync: ...)", got.Text)
	}
}

// TestWireCronPost_NilSchedulerIsNoop pins that wireCronPost tolerates "no
// cron jobs configured" (armCron returns a nil *Scheduler in that case).
func TestWireCronPost_NilSchedulerIsNoop(t *testing.T) {
	wireCronPost(nil, &fakePoster{}) // must not panic
}

// fakeCronDispatcher/fakeCronToolRunner are minimal cron.Dispatcher/ToolRunner
// fakes local to this file — internal/cron's own fakes are unexported to that
// package.
type fakeCronDispatcher struct{}

func (fakeCronDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	return "sub-1", nil
}

type fakeCronToolRunner struct {
	result string
	err    error
}

func (f *fakeCronToolRunner) RunTool(_ context.Context, name, workDir string, args map[string]any) (string, error) {
	return f.result, f.err
}
