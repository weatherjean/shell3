package shell3

import (
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// outcomeSink collects what the completion router reports back to cron.
type outcomeSink struct {
	mu  sync.Mutex
	got []CronOutcome
}

func (s *outcomeSink) record(o CronOutcome) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, o)
}

func (s *outcomeSink) snapshot() []CronOutcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]CronOutcome{}, s.got...)
}

func cronEvent() CompletionEvent {
	return CompletionEvent{
		Kind: EvCron, JobID: "sub7", Title: "cron:nightly", Agent: "auditor",
		CronJob: "nightly", Elapsed: 7 * time.Minute,
		notice: notifyAgentDone("sub7", "done", ""),
	}
}

func newOutcomeRuntime(t *testing.T) (*Runtime, *fakeHost, *outcomeSink) {
	t.Helper()
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: fakellm.New()} })
	host := &fakeHost{wakeOK: false}
	rt.SetCompletionHost(host)
	sink := &outcomeSink{}
	rt.SetCronOutcomeHook(sink.record)
	return rt, host, sink
}

// A failed cron run must reach the scheduler as a failure: this is the whole
// point — Dispatch only ever said the subagent was accepted.
func TestCronOutcomeReportsFailure(t *testing.T) {
	rt, _, sink := newOutcomeRuntime(t)
	ev := cronEvent()
	ev.ErrText = "agent errored"
	rt.jobs.dispatchCompletion(ev)
	got := sink.snapshot()
	if len(got) != 1 {
		t.Fatalf("outcomes = %v, want 1", got)
	}
	if got[0].Job != "nightly" || got[0].SubID != "sub7" || got[0].OK || got[0].ErrText != "agent errored" {
		t.Fatalf("bad outcome: %+v", got[0])
	}
	if got[0].Elapsed != 7*time.Minute {
		t.Fatalf("elapsed = %v, want the run's own", got[0].Elapsed)
	}
}

// The NO_REPLY drop is a DELIVERY decision — an idempotent tick with nothing
// to say — and returns before the owner branch. The run still happened, so
// its outcome must not be swallowed with the report.
func TestCronOutcomeReportedForOwnerlessNoReply(t *testing.T) {
	rt, host, sink := newOutcomeRuntime(t)
	ev := cronEvent()
	ev.Tail = "NO_REPLY"
	rt.jobs.dispatchCompletion(ev)
	if posts, wakes, fresh := host.snapshot(); len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("NO_REPLY must deliver nothing: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
	got := sink.snapshot()
	if len(got) != 1 || !got[0].OK || got[0].Job != "nightly" {
		t.Fatalf("clean ownerless outcome not reported: %+v", got)
	}
}

// /superstop replaces N floor posts with one summary. That is a decision about
// the CHAT; a suppressed job's run still belongs in its job's history.
func TestCronOutcomeReportedForSuppressedJob(t *testing.T) {
	rt, host, sink := newOutcomeRuntime(t)
	ev := cronEvent()
	ev.ErrText = "killed"
	rt.jobs.mu.Lock()
	rt.jobs.jobs[ev.JobID] = &bgJob{id: ev.JobID, suppress: true}
	rt.jobs.mu.Unlock()
	rt.jobs.dispatchCompletion(ev)
	if posts, _, _ := host.snapshot(); len(posts) != 0 {
		t.Fatalf("suppressed job posted: %v", posts)
	}
	if got := sink.snapshot(); len(got) != 1 || got[0].OK {
		t.Fatalf("suppressed run missing from history: %+v", got)
	}
}

// A non-cron completion has no job history to update.
func TestNonCronCompletionReportsNoOutcome(t *testing.T) {
	rt, _, sink := newOutcomeRuntime(t)
	ev := cleanEvent()
	ev.OwnerID = "sess-1"
	rt.jobs.dispatchCompletion(ev)
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("non-cron event reported an outcome: %+v", got)
	}
}

// A lingering cron subagent's follow-up turn is the SAME run continuing. Its
// outcome is the main turn's, already recorded; counting it again would make
// one run look like two.
func TestCronFollowUpReportsNoOutcome(t *testing.T) {
	rt, _, sink := newOutcomeRuntime(t)
	ev := cronEvent()
	ev.Kind = EvFollowUp
	rt.jobs.dispatchCompletion(ev)
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("follow-up counted as a run: %+v", got)
	}
}

// A run cancelAll killed at shutdown reports a manufactured "context
// canceled" failure. That failure belongs to the restart, not the job: its
// outbox row is dropped for the same reason, and the boot-time dead-PID
// marker is the honest report.
func TestCronOutcomeSkipsShutdownCancelled(t *testing.T) {
	rt, _, sink := newOutcomeRuntime(t)
	ev := cronEvent()
	ev.ErrText = "context canceled"
	rt.jobs.mu.Lock()
	rt.jobs.closing = true
	rt.jobs.jobs[ev.JobID] = &bgJob{id: ev.JobID, shutdownCancel: true}
	rt.jobs.mu.Unlock()
	rt.jobs.dispatchCompletion(ev)
	if got := sink.snapshot(); len(got) != 0 {
		t.Fatalf("restart-manufactured failure counted against the job: %+v", got)
	}
}
