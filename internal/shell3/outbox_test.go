package shell3

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
)

// outboxRows is a test helper returning the outbox rows of a runtime's store.
func outboxRows(t *testing.T, rt *Runtime) []struct{ Kind, JSON string } {
	t.Helper()
	rows, err := rt.store.OutboxLoadAll()
	if err != nil {
		t.Fatalf("OutboxLoadAll: %v", err)
	}
	out := make([]struct{ Kind, JSON string }, 0, len(rows))
	for _, r := range rows {
		out = append(out, struct{ Kind, JSON string }{r.Kind, r.JSON})
	}
	return out
}

// A completion handed to the host leaves no outbox row behind: delivery
// completed, nothing to redeliver at the next boot.
func TestDispatchDeletesEventRowAfterHandoff(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)

	ev := cleanEvent()
	ev.OwnerID = "sess-1"
	rt.jobs.dispatchCompletion(ev)

	if _, wakes, _ := host.snapshot(); len(wakes) != 1 {
		t.Fatalf("wakes = %v, want the completion delivered", wakes)
	}
	if rows := outboxRows(t, rt); len(rows) != 0 {
		t.Fatalf("outbox = %+v, want empty after successful hand-off", rows)
	}
}

// A completion arriving during shutdown is NOT dropped: the event row stays
// in the outbox so the next boot can redeliver what this process could not.
func TestDispatchDuringShutdownKeepsEventRow(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)

	rt.jobs.mu.Lock()
	rt.jobs.closing = true
	rt.jobs.mu.Unlock()

	ev := cleanEvent()
	ev.OwnerID = "sess-1"
	rt.jobs.dispatchCompletion(ev)

	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("host got %v/%v/%v, want nothing during shutdown", posts, wakes, fresh)
	}
	rows := outboxRows(t, rt)
	if len(rows) != 1 || rows[0].Kind != "event" {
		t.Fatalf("outbox = %+v, want one surviving event row", rows)
	}
	if !strings.Contains(rows[0].JSON, "bg1") {
		t.Fatalf("event row does not carry the job: %s", rows[0].JSON)
	}
}

// A job the shutdown itself cancelled must NOT leave an event row: its
// "failed: context canceled" completion is noise manufactured by the restart,
// and the running marker (not the event) is what reports it at boot.
func TestDispatchDuringShutdownDropsCancelledJobsEvent(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)

	rt.jobs.mu.Lock()
	rt.jobs.closing = true
	rt.jobs.jobs["bg2"] = &bgJob{id: "bg2", shutdownCancel: true}
	rt.jobs.mu.Unlock()

	rt.jobs.dispatchCompletion(failedEvent())

	if rows := outboxRows(t, rt); len(rows) != 0 {
		t.Fatalf("outbox = %+v, want no row for a shutdown-cancelled job", rows)
	}
}

func TestRecoverCompletionsRedeliversEvent(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{}
	rt.SetCompletionHost(host)

	if _, err := rt.store.OutboxPut("event",
		`{"Kind":0,"JobID":"bg7","Title":"make deploy","Tail":"deployed ok"}`); err != nil {
		t.Fatal(err)
	}

	if n := rt.RecoverCompletions(); n != 1 {
		t.Fatalf("recovered = %d, want 1", n)
	}
	_, _, fresh := host.snapshot()
	if len(fresh) != 1 || !strings.Contains(fresh[0], "bg7") {
		t.Fatalf("fresh = %v, want the recovered completion mailed", fresh)
	}
	if !strings.Contains(fresh[0], "recovered after a shell3 restart") {
		t.Fatalf("recovered mail should say so, got %q", fresh[0])
	}
	if rows := outboxRows(t, rt); len(rows) != 0 {
		t.Fatalf("outbox = %+v, want empty after recovery", rows)
	}
}

func TestRecoverCompletionsReportsDeadRunningMarker(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{}
	rt.SetCompletionHost(host)

	if _, err := rt.store.OutboxPut("running",
		`{"pid":2147483646,"kind":"command","job_id":"bg3","title":"rsync backup"}`); err != nil {
		t.Fatal(err)
	}

	if n := rt.RecoverCompletions(); n != 1 {
		t.Fatalf("recovered = %d, want 1", n)
	}
	posts, _, _ := host.snapshot()
	if len(posts) != 1 || !strings.Contains(posts[0], "was still running when shell3 stopped") {
		t.Fatalf("posts = %v, want the lost-job failure floor", posts)
	}
	if !strings.Contains(posts[0], "rsync backup") {
		t.Fatalf("post should name the job, got %q", posts[0])
	}
	if rows := outboxRows(t, rt); len(rows) != 0 {
		t.Fatalf("outbox = %+v, want empty after recovery", rows)
	}
}

// A running marker whose PID is alive belongs to a concurrent process (an
// ask alongside the bot): left untouched, nothing delivered.
func TestRecoverCompletionsSkipsLiveRunningMarker(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{}
	rt.SetCompletionHost(host)

	if _, err := rt.store.OutboxPut("running",
		fmt.Sprintf(`{"pid":%d,"kind":"command","job_id":"bg9","title":"live elsewhere"}`, os.Getpid())); err != nil {
		t.Fatal(err)
	}

	if n := rt.RecoverCompletions(); n != 0 {
		t.Fatalf("recovered = %d, want 0", n)
	}
	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("host got %v/%v/%v, want nothing for a live marker", posts, wakes, fresh)
	}
	if rows := outboxRows(t, rt); len(rows) != 1 {
		t.Fatalf("outbox = %+v, want the live marker left in place", rows)
	}
}

func TestFailedPostKeepsEventRow(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{postErr: fmt.Errorf("telegram is down")}
	rt.SetCompletionHost(host)

	rt.jobs.dispatchCompletion(failedEvent())

	rows := outboxRows(t, rt)
	if len(rows) != 1 || rows[0].Kind != "event" {
		t.Fatalf("outbox = %+v, want the undelivered event kept", rows)
	}
}

func TestRedeliverUndelivered(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{postErr: fmt.Errorf("telegram is down")}
	rt.SetCompletionHost(host)

	rt.jobs.dispatchCompletion(failedEvent())
	if rows := outboxRows(t, rt); len(rows) != 1 {
		t.Fatalf("outbox = %+v, want one kept row", rows)
	}

	if n := rt.RedeliverUndelivered(); n != 1 {
		t.Fatalf("redelivered = %d, want 1 (attempted)", n)
	}
	if rows := outboxRows(t, rt); len(rows) != 1 {
		t.Fatalf("outbox = %+v, want the row still kept while down", rows)
	}

	// Transport heals: the retry posts and drains the outbox.
	host.mu.Lock()
	host.postErr = nil
	host.mu.Unlock()
	if n := rt.RedeliverUndelivered(); n != 1 {
		t.Fatalf("redelivered = %d, want 1", n)
	}
	posts, _, _ := host.snapshot()
	if len(posts) < 2 {
		t.Fatalf("posts = %v, want the retries to have posted", posts)
	}
	if rows := outboxRows(t, rt); len(rows) != 0 {
		t.Fatalf("outbox = %+v, want empty after successful redelivery", rows)
	}
	if n := rt.RedeliverUndelivered(); n != 0 {
		t.Fatalf("redelivered = %d, want 0 when nothing is pending", n)
	}
}

// waitOutboxEmpty polls until the runtime's outbox drains (job finished and
// its rows cleaned up) or the deadline hits.
func waitOutboxEmpty(t *testing.T, rt *Runtime) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(outboxRows(t, rt)) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("outbox never drained: %+v", outboxRows(t, rt))
}

func TestCommandJobMarkerLifecycle(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rows := outboxRows(t, rt)
	if len(rows) != 1 || rows[0].Kind != "running" {
		t.Fatalf("outbox after start = %+v, want one running marker", rows)
	}
	if !strings.Contains(rows[0].JSON, `"job_id":"bg1"`) {
		t.Fatalf("marker does not name the job: %s", rows[0].JSON)
	}

	rt.jobs.killAllForStop() // a user kill, not a shutdown: marker must clear
	rt.jobs.wait()
	waitOutboxEmpty(t, rt)
}

// cancelAll (runtime shutdown) leaves the running marker in place: it is the
// record the next boot turns into "was still running when shell3 stopped".
func TestShutdownLeavesRunningMarker(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.cancelAll()
	rt.jobs.wait()

	rows := outboxRows(t, rt)
	if len(rows) != 1 || rows[0].Kind != "running" {
		t.Fatalf("outbox after shutdown = %+v, want the running marker kept", rows)
	}
}
