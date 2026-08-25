//go:build unix

package cron

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/shell3"
)

type fakeDispatcher struct {
	mu       sync.Mutex
	calls    []shell3.CronJob
	lastOpts shell3.DispatchOpts
}

func (f *fakeDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, shell3.CronJob{Agent: agent, Prompt: prompt, WorkDir: opts.WorkDir, Name: opts.Description, Report: opts.Report})
	f.lastOpts = opts
	return "subX", nil
}
func (f *fakeDispatcher) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.calls) }

func TestScheduler_FireDispatches(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1s", Agent: "explorer", Prompt: "go", Report: notify.ReportRaw}}
	s, err := New(fd, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if fd.count() != 1 {
		t.Fatalf("want 1 dispatch, got %d", fd.count())
	}
	got := fd.calls[0]
	if got.Agent != "explorer" || got.Prompt != "go" || got.Name != "cron:j1" || got.Report != notify.ReportRaw {
		t.Fatalf("bad dispatch args: %+v", got)
	}
	// The dispatch carries the cron job name so the runtime routes ⏰ posts
	// and the ownerless wake path.
	if fd.lastOpts.CronJob != "j1" {
		t.Fatalf("CronJob = %q, want j1", fd.lastOpts.CronJob)
	}
	js := s.Jobs()
	if len(js) != 1 || js[0].Name != "j1" || js[0].LastSubID != "subX" || js[0].Report != "raw" {
		t.Fatalf("bad job status: %+v", js)
	}
}

// waitFor polls cond until it returns true or a 1s deadline passes, failing
// the test on timeout. Run() fires off its own goroutine (see the doc
// comment on Scheduler.Run), so its effects land asynchronously.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("waitFor: condition not met within 1s")
}

// TestScheduler_Run covers the manual-trigger path (e.g. the /run command):
// Run fires exactly the named job and returns an error for an unknown name
// without dispatching anything. Run returns before the fire completes (see
// its doc comment — /run must not block the bot's update loop), so
// assertions on its effect wait rather than checking immediately.
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
	waitFor(t, func() bool { return fd.count() == 1 })
	if got := fd.calls[0].Name; got != "cron:nightly" {
		t.Errorf("dispatched label = %q, want cron:nightly", got)
	}

	if err := s.Run("nope"); err == nil {
		t.Fatal("Run(nope): want error for unknown job name")
	}
	// An unknown name is rejected synchronously (Run returns the error
	// before spawning anything), so no wait is needed for the negative case.
	if fd.count() != 1 {
		t.Fatalf("unknown-name Run fired a dispatch: count=%d", fd.count())
	}
}

// TestScheduler_RunIsAsync pins that Run returns before the job completes. A
// caller on a single serialized loop (the bot's /run handler) must never be
// blocked by a fire, and Dispatch can take a moment to accept a job when the
// runtime's concurrency cap is contended.
func TestScheduler_RunIsAsync(t *testing.T) {
	release := make(chan struct{})
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1h", Agent: "worker", Prompt: "sync"}}
	s, err := New(&blockingDispatcher{release: release}, jobs)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		if err := s.Run("sync"); err != nil {
			t.Error(err)
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return promptly — it must not block on the fire")
	}
	close(release) // let the blocked dispatch finish, so the goroutine doesn't leak
}

// blockingDispatcher blocks Dispatch until release closes, standing in for a
// slow accept so TestScheduler_RunIsAsync can prove Run doesn't wait on it.
type blockingDispatcher struct{ release chan struct{} }

func (b *blockingDispatcher) Dispatch(string, string, shell3.DispatchOpts) (string, error) {
	<-b.release
	return "subX", nil
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

// errDispatcher rejects every dispatch: the one failure the fire path itself
// can see, because no completion will ever arrive for it.
type errDispatcher struct{ err error }

func (e errDispatcher) Dispatch(agent, prompt string, opts shell3.DispatchOpts) (string, error) {
	return "", e.err
}

// TestScheduler_OutcomeDescribesTheRun is the whole point of RecordOutcome:
// a job that dispatches cleanly and then FAILS its work must read as a
// failure. Before the outcome arrives the row still holds the previous run's
// verdict — inventing one for work in flight is the thing being fixed.
func TestScheduler_OutcomeDescribesTheRun(t *testing.T) {
	fd := &fakeDispatcher{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "explorer", Prompt: "go"}}
	s, err := New(fd, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if got := s.Jobs()[0]; got.Runs != 1 || got.Failures != 0 || got.LastOK {
		t.Fatalf("dispatch must not claim success: %+v", got)
	}
	s.RecordOutcome(shell3.CronOutcome{
		Job: "j1", SubID: "subX", OK: false, ErrText: "tool exploded", Elapsed: 7 * time.Minute,
	})
	got := s.Jobs()[0]
	if got.LastOK || got.LastErr != "tool exploded" || got.Failures != 1 || got.Runs != 1 {
		t.Fatalf("outcome not recorded: %+v", got)
	}
	if got.LastMillis != (7 * time.Minute).Milliseconds() {
		t.Fatalf("LastMillis = %d, want the run's own elapsed", got.LastMillis)
	}
}

// A clean outcome flips a previously failing job back, and counts no failure.
func TestScheduler_OutcomeClean(t *testing.T) {
	s, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "a", Prompt: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	s.fire(shell3.CronJob{Name: "j1", Agent: "a", Prompt: "go"})
	s.RecordOutcome(shell3.CronOutcome{Job: "j1", SubID: "subX", OK: true, Elapsed: time.Second})
	if got := s.Jobs()[0]; !got.LastOK || got.LastErr != "" || got.Failures != 0 {
		t.Fatalf("clean outcome: %+v", got)
	}
}

// Completion delivery is at-least-once — an outage redelivers the same event
// every few minutes, and a leftover outbox row replays at boot — so the same
// run reports repeatedly. Counting each pass would inflate exactly the number
// RecordOutcome exists to make honest.
func TestScheduler_OutcomeRecordedOnce(t *testing.T) {
	s, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "a", Prompt: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	s.fire(shell3.CronJob{Name: "j1", Agent: "a", Prompt: "go"})
	o := shell3.CronOutcome{Job: "j1", SubID: "subX", OK: false, ErrText: "boom"}
	for i := 0; i < 5; i++ {
		s.RecordOutcome(o)
	}
	if got := s.Jobs()[0]; got.Failures != 1 || got.Runs != 1 {
		t.Fatalf("redelivery double-counted: %+v", got)
	}
}

// An outcome from a run a later fire has superseded is a straggler: the table
// keeps the latest run only, so it must not backdate the row.
func TestScheduler_OutcomeStaleSubIDDropped(t *testing.T) {
	s, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "a", Prompt: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	s.fire(shell3.CronJob{Name: "j1", Agent: "a", Prompt: "go"}) // LastSubID = subX
	s.RecordOutcome(shell3.CronOutcome{Job: "j1", SubID: "subOLD", OK: false, ErrText: "boom"})
	if got := s.Jobs()[0]; got.Failures != 0 || got.LastErr != "" {
		t.Fatalf("stale outcome applied: %+v", got)
	}
}

// A name this scheduler does not declare — one a /reload removed — must never
// grow a phantom row.
func TestScheduler_OutcomeUnknownJobDropped(t *testing.T) {
	s, err := New(&fakeDispatcher{}, []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "a", Prompt: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	s.RecordOutcome(shell3.CronOutcome{Job: "ghost", OK: false, ErrText: "boom"})
	if js := s.Jobs(); len(js) != 1 || js[0].Name != "j1" {
		t.Fatalf("phantom row: %+v", js)
	}
}

// A dispatch rejection is a real failure with no completion coming, so it
// counts at once — and nothing later may overwrite it.
func TestScheduler_DispatchRejectionCountsImmediately(t *testing.T) {
	s, err := New(errDispatcher{err: errors.New("no such agent")},
		[]shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "gone", Prompt: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	s.fire(shell3.CronJob{Name: "j1", Agent: "gone", Prompt: "go"})
	got := s.Jobs()[0]
	if got.LastOK || got.Failures != 1 || got.LastErr != "no such agent" {
		t.Fatalf("rejection not counted: %+v", got)
	}
	s.RecordOutcome(shell3.CronOutcome{Job: "j1", OK: true})
	if got := s.Jobs()[0]; got.LastOK {
		t.Fatalf("a straggler overwrote a dispatch rejection: %+v", got)
	}
}

// The fire-time write must reach the store: the outcome matches on LastSubID,
// so a fire whose row did not survive a restart has nothing to match against.
func TestScheduler_FirePersistsSubID(t *testing.T) {
	rs := &fakeRunStore{}
	jobs := []shell3.CronJob{{Name: "j1", Schedule: "@every 1h", Agent: "a", Prompt: "go"}}
	s, err := NewWithStore(&fakeDispatcher{}, rs, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	rs.mu.Lock()
	saved := rs.status["j1"]
	rs.mu.Unlock()
	if saved.LastSubID != "subX" || saved.Runs != 1 || saved.OutcomeRecorded {
		t.Fatalf("fire not persisted for later matching: %+v", saved)
	}
	s.RecordOutcome(shell3.CronOutcome{Job: "j1", SubID: "subX", OK: true, Elapsed: time.Second})
	rs.mu.Lock()
	saved = rs.status["j1"]
	rs.mu.Unlock()
	if !saved.LastOK || !saved.OutcomeRecorded {
		t.Fatalf("outcome not persisted: %+v", saved)
	}
}
