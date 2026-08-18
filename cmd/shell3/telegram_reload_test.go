//go:build unix

package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// fakeReloader stands in for *shell3.Runtime: Reload returns a scripted result,
// Cron returns the jobs the reloaded config would expose.
type fakeReloader struct {
	res     shell3.ReloadResult
	err     error
	jobs    []shell3.CronJob
	reloads int
}

func (f *fakeReloader) Reload() (shell3.ReloadResult, error) {
	f.reloads++
	return f.res, f.err
}
func (f *fakeReloader) Cron() []shell3.CronJob { return f.jobs }

// fakeBot records the rearm calls reloadAndRearm makes on the bot. The bot's
// host tools are re-applied by the runtime session decorator on reload, not by
// reloadAndRearm, so there is no redecorate call to record. posts is guarded
// by mu: a tool job's fire runs on its own goroutine (Scheduler.Run), so
// PostCompletion and a test's read of posts race without a lock.
type fakeBot struct {
	runnerSet int
	runnerNil bool
	mu        sync.Mutex
	posts     []shell3.CompletionPost
}

func (b *fakeBot) SetJobRunner(fn func(name string) error) {
	b.runnerSet++
	b.runnerNil = fn == nil
}

func (b *fakeBot) PostCompletion(p shell3.CompletionPost) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.posts = append(b.posts, p)
}

func (b *fakeBot) postSnapshot() []shell3.CompletionPost {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]shell3.CompletionPost(nil), b.posts...)
}

// fakeDispatcher satisfies cron.Dispatcher so cron.New can build a real
// scheduler without a live session.
type fakeDispatcher struct{}

func (fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	return "sub-1", nil
}

// TestReloadAndRearm_ArmsNewScheduler pins the happy path: a reload whose config
// declares a cron job stops nothing (no prior scheduler) and arms exactly one
// new scheduler wired to the bot's /run handler.
func TestReloadAndRearm_ArmsNewScheduler(t *testing.T) {
	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}}}
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, fakeDispatcher{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	if ns == nil {
		t.Fatal("expected a new scheduler, got nil")
	}
	t.Cleanup(ns.Stop)

	if b.runnerSet != 1 || b.runnerNil {
		t.Errorf("SetJobRunner set=%d nil=%v, want set=1 nil=false", b.runnerSet, b.runnerNil)
	}
	if len(ns.Jobs()) != 1 {
		t.Errorf("new scheduler has %d jobs, want 1", len(ns.Jobs()))
	}
}

// fakeReloadToolRunner is a minimal cron.ToolRunner for the reload-ordering
// test below.
type fakeReloadToolRunner struct{ result string }

func (f fakeReloadToolRunner) RunTool(_ context.Context, name, workDir string, args map[string]any) (string, error) {
	return f.result, nil
}

// TestReloadAndRearm_WiresPostBeforeStarting is the regression test for the
// reviewed defect: the old code called ns.Start() (inside reloadAndRearm)
// and wired post only afterwards (in the caller, hostwiring.go). A fire
// landing in that gap found post nil and silently dropped its result —
// AGENTS.md promises a completion is never lost. reloadAndRearm now wires
// post itself, before Start(), so nothing external needs to do it — this
// fires the new scheduler's job immediately on return and expects the post
// to land with no further wiring from the caller.
func TestReloadAndRearm_WiresPostBeforeStarting(t *testing.T) {
	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "sync", Schedule: "@every 1h", Tool: "sync-notion-recent"}}}
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, fakeDispatcher{}, fakeReloadToolRunner{result: "3 rows updated"}, nil, nil, nil)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	t.Cleanup(ns.Stop)

	// No call to wireCronPost here — reloadAndRearm must have already done it.
	if err := ns.Run("sync"); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, func() bool { return len(b.postSnapshot()) == 1 })
	posts := b.postSnapshot()
	if !strings.Contains(posts[0].Text, "3 rows updated") {
		t.Fatalf("posts = %+v, want the tool result — post must not be nil this early", posts)
	}
}

// TestReloadAndRearm_NoJobsClearsSchedule pins that reloading into a jobless
// config stops the prior scheduler, returns nil, and clears the /run handler.
func TestReloadAndRearm_NoJobsClearsSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, nil, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()

	r := &fakeReloader{} // no jobs after reload
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, fakeDispatcher{}, nil, nil, nil, old)
	if err != nil {
		t.Fatalf("reloadAndRearm: %v", err)
	}
	if ns != nil {
		ns.Stop()
		t.Fatalf("expected nil scheduler for jobless config, got %v", ns)
	}
	if b.runnerSet != 1 || !b.runnerNil {
		t.Errorf("SetJobRunner set=%d nil=%v, want set=1 nil=true", b.runnerSet, b.runnerNil)
	}
}

// TestReloadAndRearm_ReloadErrorKeepsOldSchedule pins the fail-safe: a reload
// error leaves the running scheduler untouched (returned unchanged).
func TestReloadAndRearm_ReloadErrorKeepsOldSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, nil, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()
	t.Cleanup(old.Stop)

	r := &fakeReloader{err: errors.New("bad config")}
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, fakeDispatcher{}, nil, nil, nil, old)
	if err == nil {
		t.Fatal("expected reload error")
	}
	if ns != old {
		t.Error("scheduler should be unchanged on reload failure")
	}
	if b.runnerSet != 0 {
		t.Errorf("no rearm should happen on reload failure: runnerSet=%d", b.runnerSet)
	}
}

// A malformed-but-non-empty schedule passes config validation (validateCron
// doesn't parse expressions) and fails only at cron.New. The old scheduler
// must survive that failure — build-new-before-stop-old.
func TestReloadAndRearm_BadScheduleKeepsOldSchedule(t *testing.T) {
	old, err := cron.New(fakeDispatcher{}, nil, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	old.Start()
	t.Cleanup(old.Stop)

	r := &fakeReloader{jobs: []shell3.CronJob{{Name: "bad", Schedule: "not a schedule", Agent: "explorer", Prompt: "p"}}}
	b := &fakeBot{}

	ns, _, err := reloadAndRearm(r, b, fakeDispatcher{}, nil, nil, nil, old)
	if err == nil {
		t.Fatal("expected cron.New parse error")
	}
	if ns != old {
		t.Error("scheduler should be unchanged when the new schedule fails to arm")
	}
	// The surviving scheduler must still fire manually.
	if err := old.Run("j"); err != nil {
		t.Errorf("old scheduler should still be running: %v", err)
	}
}

// TestSchedulerJobsReflectsRunAsync pins what b.SetCronStatus wires directly
// to sched.Jobs() now (see hostwiring.go): a never-run job's LastRun stays
// empty, a fired one picks it up, and Scheduler.Run's own goroutine means the
// effect lands asynchronously — a caller reading Jobs() right after Run must
// not assume it's already reflected.
func TestSchedulerJobsReflectsRunAsync(t *testing.T) {
	sched, err := cron.New(fakeDispatcher{}, nil, []shell3.CronJob{
		{Name: "fired", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"},
		{Name: "never", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range sched.Jobs() {
		if j.LastRun != "" {
			t.Errorf("a freshly armed scheduler has no last runs, got %+v", j)
		}
	}
	if err := sched.Run("fired"); err != nil {
		t.Fatal(err)
	}
	// Scheduler.Run fires off its own goroutine (it must not block the bot's
	// single update loop for a slow tool job), so its effect on Jobs() lands
	// asynchronously.
	hasFired := func() bool {
		for _, j := range sched.Jobs() {
			if j.Name == "fired" && j.LastRun != "" {
				return true
			}
		}
		return false
	}
	waitForCondition(t, hasFired)
}

// waitForCondition polls cond until it returns true or a 1s deadline passes,
// failing the test on timeout.
func waitForCondition(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitForCondition: condition not met within 1s")
}
