//go:build unix

package cron

import (
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

type fakeDispatcher struct {
	mu    sync.Mutex
	calls []shell3.CronJob
}

func (f *fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, shell3.CronJob{Agent: agent, Prompt: prompt, WorkDir: opts.WorkDir, Name: opts.Description, Notify: opts.Notify})
	return "subX", nil
}
func (f *fakeDispatcher) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func TestScheduler_FireDispatches(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1s", Agent: "explorer", Prompt: "go", Notify: true}}
	s, err := New(fd, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if fd.count() != 1 {
		t.Fatalf("want 1 dispatch, got %d", fd.count())
	}
	got := fd.calls[0]
	if got.Agent != "explorer" || got.Prompt != "go" || got.Name != "cron:j1" || !got.Notify {
		t.Fatalf("bad dispatch args: %+v", got)
	}
	js := s.Jobs()
	if len(js) != 1 || js[0].Name != "j1" || js[0].LastSubID != "subX" {
		t.Fatalf("bad job status: %+v", js)
	}
}

// TestScheduler_Run covers the manual-trigger path (e.g. the /run command):
// Run fires exactly the named job and returns an error for an unknown name
// without dispatching anything.
func TestScheduler_Run(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{
		{Name: "nightly", Schedule: "@every 1h", Agent: "explorer", Prompt: "go"},
		{Name: "weekly", Schedule: "@every 1h", Agent: "explorer", Prompt: "go"},
	}
	s, err := New(fd, jobs)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.Run("nightly"); err != nil {
		t.Fatalf("Run(nightly): %v", err)
	}
	if fd.count() != 1 {
		t.Fatalf("want exactly 1 dispatch for the named job, got %d", fd.count())
	}
	if got := fd.calls[0].Name; got != "cron:nightly" {
		t.Errorf("dispatched label = %q, want cron:nightly", got)
	}

	if err := s.Run("nope"); err == nil {
		t.Fatal("Run(nope): want error for unknown job name")
	}
	if fd.count() != 1 {
		t.Fatalf("unknown-name Run fired a dispatch: count=%d", fd.count())
	}
}

func TestScheduler_BadScheduleRejected(t *testing.T) {
	if _, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "x", Schedule: "not a cron", Agent: "a"}}); err == nil {
		t.Fatal("expected error for malformed schedule")
	}
}

func TestScheduler_StartStopClean(t *testing.T) {
	s, _ := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Agent: "explorer", Prompt: "p"}})
	s.Start()
	time.Sleep(20 * time.Millisecond)
	s.Stop() // must not hang
}
