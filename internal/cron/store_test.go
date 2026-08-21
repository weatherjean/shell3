//go:build unix

package cron

import (
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// fakeRunStore is an in-memory RunStore for testing the scheduler's use of
// the seam, without a real database.
type fakeRunStore struct {
	mu      sync.Mutex
	status  map[string]JobStatus
	saves   int
	saveErr error
}

func (f *fakeRunStore) LoadStatus() (map[string]JobStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]JobStatus, len(f.status))
	for k, v := range f.status {
		out[k] = v
	}
	return out, nil
}

func (f *fakeRunStore) SaveStatus(st JobStatus) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.saves++
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.status == nil {
		f.status = map[string]JobStatus{}
	}
	f.status[st.Name] = st
	return nil
}

func TestScheduler_RestoresStatusOnStart(t *testing.T) {
	rs := &fakeRunStore{status: map[string]JobStatus{
		"bookmarks-tick": {Name: "bookmarks-tick", Runs: 12, Failures: 2, LastRun: "2026-08-16T21:29:55Z"},
	}}
	jobs := []shell3.CronJob{{Name: "bookmarks-tick", Schedule: "@every 3h", Agent: "bookmarks", Prompt: "go"}}
	s, err := NewWithStore(&fakeDispatcher{}, &fakeToolRunner{}, rs, jobs)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Jobs()[0]
	if got.Runs != 12 || got.Failures != 2 {
		t.Fatalf("history not restored: %+v", got)
	}
	// The restored status must not clobber config-derived fields — the
	// schedule/agent the freshly loaded config declares, not whatever was
	// serialized alongside the old counts.
	if got.Schedule != "@every 3h" || got.Agent != "bookmarks" {
		t.Fatalf("config fields overwritten by restored status: %+v", got)
	}
}

func TestScheduler_PersistsEveryRun(t *testing.T) {
	rs := &fakeRunStore{status: map[string]JobStatus{}}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "t"}}
	s, err := NewWithStore(&fakeDispatcher{}, &fakeToolRunner{}, rs, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	if rs.saves != 1 {
		t.Fatalf("saves = %d, want 1 — a run that is not persisted is invisible after restart", rs.saves)
	}
}

// A job with no restored entry (never run before, or a brand-new job in the
// config) must still arm cleanly with a zero-value history, not a nil-map
// panic.
func TestScheduler_NewJobNoRestoredHistory(t *testing.T) {
	rs := &fakeRunStore{status: map[string]JobStatus{}}
	jobs := []shell3.CronJob{{Name: "brand-new", Schedule: "@every 1h", Tool: "t"}}
	s, err := NewWithStore(&fakeDispatcher{}, &fakeToolRunner{}, rs, jobs)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Jobs()[0]
	if got.Runs != 0 || got.LastRun != "" {
		t.Fatalf("a never-run job must start at zero, got %+v", got)
	}
}

// New (the Task-3 signature) must still work with no store at all — the
// nil-store path callers other than the host use (tests, library use).
func TestNew_NilStoreNoPanic(t *testing.T) {
	jobs := []shell3.CronJob{{Name: "j", Schedule: "@every 1h", Tool: "t"}}
	s, err := New(&fakeDispatcher{}, &fakeToolRunner{}, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0]) // must not panic reaching for a nil store
	if got := s.Jobs()[0]; got.Runs != 1 {
		t.Fatalf("in-memory recording still works without a store: %+v", got)
	}
}

// A failed save must not fail the run: the fire already happened, and a
// bookkeeping fault must never look like a job fault.
func TestScheduler_SaveFailureIsNotFatal(t *testing.T) {
	rs := &fakeRunStore{status: map[string]JobStatus{}, saveErr: errPersist}
	jobs := []shell3.CronJob{{Name: "sync", Schedule: "@every 1s", Tool: "t"}}
	s, err := NewWithStore(&fakeDispatcher{}, &fakeToolRunner{}, rs, jobs)
	if err != nil {
		t.Fatal(err)
	}
	s.fire(jobs[0])
	got := s.Jobs()[0]
	if got.Runs != 1 {
		t.Fatalf("a store failure must not roll back the in-memory record: %+v", got)
	}
}

// TestStoreRunStore_RoundTrip exercises the real runs-store-backed RunStore:
// save a job's status, reopen (a fresh Store handle, standing in for a
// restart), and confirm LoadStatus returns it unchanged.
func TestStoreRunStore_RoundTrip(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	rs := StoreRunStore{Store: st}
	want := JobStatus{
		Name: "nightly", Schedule: "@daily", Agent: "explorer",
		LastRun: "2026-08-16T21:29:55Z", LastOK: false, LastErr: "boom",
		Runs: 5, Failures: 1, LastMillis: 1234,
	}
	if err := rs.SaveStatus(want); err != nil {
		t.Fatal(err)
	}

	// A fresh handle onto the same database — the shape of an actual restart.
	st2, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	rs2 := StoreRunStore{Store: st2}

	got, err := rs2.LoadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if got["nightly"] != want {
		t.Fatalf("round trip = %+v, want %+v", got["nightly"], want)
	}
}

// TestCronStatus_SurvivesStartupJanitor is the regression test for the
// reviewed defect: cron_status was originally a row in the shared threads
// table, keyed by job name with the JobStatus JSON stuffed into
// threads.session_id. runs.Sweep unconditionally deletes any threads row
// whose session_id doesn't name a live session
// (`DELETE FROM threads WHERE session_id NOT IN (SELECT id FROM sessions)`),
// and a JSON blob is never a real session id — so every cron-status row was
// deleted on the very first startup, before cron.NewWithStore ever got to
// read it back. cmd/shell3/telegram.go calls openThreads (which runs the
// startup janitors, including Sweep) BEFORE wireHost (which arms cron), so
// this ordering is not a corner case: it is what every real process does.
//
// This test drives exactly that sequence — save, Sweep (the real janitor,
// not a stand-in), then load through a fresh scheduler — so a future change
// that reintroduces the same footgun (whether in cron_status itself or in
// wherever cron history ends up living next) fails here instead of only in
// production telemetry.
func TestCronStatus_SurvivesStartupJanitor(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	want := JobStatus{
		Name: "bookmarks-tick", Schedule: "@every 3h", Agent: "bookmarks",
		LastRun: "2026-08-16T21:29:55Z", LastOK: true, Runs: 12, Failures: 2,
	}
	if err := (StoreRunStore{Store: st}).SaveStatus(want); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	// The actual startup janitor — runs.Sweep — opens its own handle on the
	// same database, exactly like runStartupJanitors does at process start,
	// runs_keep_days=0 ("keep forever") included: the defect this pins was
	// present regardless of that setting.
	if _, _, err := runs.Sweep(root, 0, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Simulate the restart: a fresh Store handle, then a fresh scheduler
	// built the way armCron builds one, restoring from RunStore.
	st2, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	jobs := []shell3.CronJob{{Name: "bookmarks-tick", Schedule: "@every 3h", Agent: "bookmarks", Prompt: "go"}}
	s, err := NewWithStore(&fakeDispatcher{}, &fakeToolRunner{}, StoreRunStore{Store: st2}, jobs)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Jobs()[0]
	if got.Runs != want.Runs || got.Failures != want.Failures || got.LastRun != want.LastRun {
		t.Fatalf("history did not survive the startup janitor: got %+v, want Runs=%d Failures=%d LastRun=%s",
			got, want.Runs, want.Failures, want.LastRun)
	}
}

// A job store has never seen (LoadStatus returns no entry for it) must not
// appear at all — the scheduler's zip-by-name join must treat "absent" as
// "never run", not as some other default.
func TestStoreRunStore_LoadStatusEmpty(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	got, err := (StoreRunStore{Store: st}).LoadStatus()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no entries in a fresh store, got %v", got)
	}
}

var errPersist = &persistError{"disk full"}

type persistError struct{ msg string }

func (e *persistError) Error() string { return e.msg }
