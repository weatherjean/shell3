package shell3

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestJobManagerCommandLifecycle(t *testing.T) {
	m := newJobManager(nil, 8)
	// echo writes to the in-memory buffer and exits; no parent notice in this unit test.
	id, err := m.startCommand(nil, "echo hi", t.TempDir(), []string{"echo", "hi"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	// Job is listed while/after running.
	if got := m.list(); len(got) != 1 || got[0].ID != id || got[0].Kind != JobCommand {
		t.Fatalf("list = %+v, want one JobCommand id=%s", got, id)
	}
	// Join the job goroutine (a real sync point), then read the output once.
	m.wg.Wait()
	if !strings.Contains(m.output(id), "hi") {
		t.Fatalf("output never contained 'hi': %q", m.output(id))
	}
}

// waitForWake drains rt.Events() until a Wake for the given session arrives
// (or fails the test after 3s), tolerating spurious Wakes for other sessions.
func waitForWake(t *testing.T, rt *Runtime, s *Session) {
	t.Helper()
	id := s.ID()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Wake && ev.Session == id {
				return
			}
		case <-deadline:
			t.Fatalf("no Wake for session %s (timeout 3s)", id)
		}
	}
}

func TestJobManagerConcurrencyCap(t *testing.T) {
	m := newJobManager(nil, 1)
	id, err := m.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "1"}, nil)
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := m.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "1"}, nil); err == nil {
		t.Fatal("expected cap error on second start, got nil")
	}
	// Don't leak the sleeping job's goroutine past the test.
	if err := m.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	m.wg.Wait()
}

// TestSubagentCompletionWakesParent verifies that startSubagent runs a child
// session in-process, and that when it finishes the parent session receives a
// Wake event on the runtime bus and transcript returns non-empty content.
func TestSubagentCompletionWakesParent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("subagent done"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent session: %v", err)
	}

	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "test task", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}

	waitForWake(t, rt, parent)
	if strings.TrimSpace(rt.jobs.transcript(id)) == "" {
		t.Fatalf("transcript for job %s is empty after subagent completion", id)
	}
}

// TestSubagentLiveOutputBuffer verifies startSubagent mirrors the child's
// streamed events into j.out, so the jobs view (via output()) can show
// live progress before the run's messages.jsonl transcript exists on disk.
func TestSubagentLiveOutputBuffer(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("streamed answer"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent session: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "test task", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	waitForWake(t, rt, parent)
	if got := rt.jobs.output(id); !strings.Contains(got, "streamed answer") {
		t.Fatalf("subagent live output buffer = %q, want it to contain the streamed text", got)
	}
}

// TestSubagentTranscriptAfterClose verifies transcript() returns a non-empty
// result even after the child session is closed. The finished job is retained
// in m.jobs with its childID intact, so transcript() resolves without any
// separate map.
func TestSubagentTranscriptAfterClose(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("result text"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "task", "desc", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	// Wait for the Wake (child is done; job is retained with Done=true).
	waitForWake(t, rt, parent)
	// Job is retained in m.jobs with Done=true; transcript must still work.
	if strings.TrimSpace(rt.jobs.transcript(id)) == "" {
		t.Fatalf("transcript empty after job done for %s", id)
	}
}

// TestJobManagerRetainsDoneCommandJob verifies that a finished command job stays
// in list() with Done=true, and that output() returns the captured output.
func TestJobManagerRetainsDoneCommandJob(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo retained", t.TempDir(), []string{"echo", "retained"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	// Join the job goroutine, then read once.
	m.wg.Wait()
	if !strings.Contains(m.output(id), "retained") {
		t.Fatalf("output never contained 'retained': %q", m.output(id))
	}

	// Job must still be in list() with Done=true.
	jobs := m.list()
	if len(jobs) != 1 {
		t.Fatalf("list() should retain 1 done job, got %d", len(jobs))
	}
	if !jobs[0].Done {
		t.Fatalf("finished command job should have Done=true, got %+v", jobs[0])
	}
	if jobs[0].Exit == nil {
		t.Fatal("finished command job should have non-nil Exit")
	}
}

// TestJobManagerRetainsDoneSubagentJob verifies that a finished subagent job
// stays in list() with Done=true, and that transcript() still resolves.
func TestJobManagerRetainsDoneSubagentJob(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("subagent output"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent session: %v", err)
	}

	id, err := rt.jobs.startSubagent(parent, "", "task", "desc", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}

	// Wait for the Wake (child is done).
	waitForWake(t, rt, parent)
	// Job must still appear in list() with Done=true.
	var found JobInfo
	for _, j := range rt.jobs.list() {
		if j.ID == id {
			found = j
			break
		}
	}
	if found.ID == "" {
		t.Fatalf("finished subagent job %s not found in list()", id)
	}
	if !found.Done {
		t.Fatalf("finished subagent job should have Done=true, got %+v", found)
	}
	// transcript() must resolve via the retained job's childID.
	if strings.TrimSpace(rt.jobs.transcript(id)) == "" {
		t.Fatalf("transcript empty after subagent done for job %s", id)
	}
}

// TestJobManagerDoneCap verifies that done jobs are capped at maxDoneJobs
// and that the oldest done job is evicted (never a running job).
func TestJobManagerDoneCap(t *testing.T) {
	m := newJobManager(nil, maxDoneJobs+10)

	// Start and let maxDoneJobs+1 command jobs finish.
	for i := 0; i < maxDoneJobs+1; i++ {
		_, err := m.startCommand(nil, "echo x", t.TempDir(), []string{"echo", "x"}, nil)
		if err != nil {
			t.Fatalf("startCommand %d: %v", i, err)
		}
	}

	// Wait until all goroutines have finished.
	m.wg.Wait()

	jobs := m.list()
	if len(jobs) > maxDoneJobs {
		t.Fatalf("done-job cap: got %d jobs, want at most %d", len(jobs), maxDoneJobs)
	}
	// Every retained job must be Done; none should be running.
	for _, j := range jobs {
		if !j.Done {
			t.Fatalf("non-done job found after wg.Wait(): %+v", j)
		}
	}
}

// TestJobManagerCancelDoneJobIsNoOp verifies that cancel() on a finished job
// returns nil and does not panic.
func TestJobManagerCancelDoneJobIsNoOp(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo done", t.TempDir(), []string{"echo", "done"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	m.wg.Wait() // wait for the job goroutine to finish
	if err := m.cancel(id); err != nil {
		t.Fatalf("cancel on done job should return nil, got %v", err)
	}
}

// TestFormatJobList_Empty returns the no-tasks message when there are no jobs.
func TestFormatJobList_Empty(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobList()
	if !strings.Contains(got, "no background") {
		t.Errorf("formatJobList empty = %q, want 'no background'", got)
	}
}

// TestFormatJobList_ShowsRunning verifies a running command job appears with status "running".
func TestFormatJobList_ShowsRunning(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "sleep 60", t.TempDir(), []string{"sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	defer func() {
		_ = m.cancel(id)
		m.wg.Wait() // join the job goroutine so it doesn't outlive the test
	}()
	got := m.formatJobList()
	if !strings.Contains(got, id) {
		t.Errorf("formatJobList %q missing job id %s", got, id)
	}
	if !strings.Contains(got, "running") {
		t.Errorf("formatJobList %q missing 'running'", got)
	}
}

// TestFormatJobStatus_UnknownID returns the "no such task" message.
func TestFormatJobStatus_UnknownID(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobStatus("ghost")
	if !strings.Contains(got, "no such task") {
		t.Errorf("formatJobStatus unknown = %q, want 'no such task'", got)
	}
}

// TestFormatJobStatus_Truncates verifies that a large output is capped.
func TestFormatJobStatus_Truncates(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo x", t.TempDir(), []string{"echo", "x"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	// Manually stuff a large output into the ring buffer (bypass the process).
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	big := strings.Repeat("x", jobStatusCap*2)
	_, _ = j.out.Write([]byte(big))

	got := m.formatJobStatus(id)
	if len(got) > jobStatusCap+200 { // allow some slack for the header lines
		t.Errorf("formatJobStatus result too large: %d bytes (cap ~%d)", len(got), jobStatusCap)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("formatJobStatus %q missing truncation marker", got)
	}
	_ = m.cancel(id)
}

// TestAppendCappedTail_NearCapNoPanic is a regression test for the tail-budget
// panic: with the header already within 20 bytes of jobStatusCap, the old code
// called tail(body, negative) and panicked on the slice bounds.
func TestAppendCappedTail_NearCapNoPanic(t *testing.T) {
	for _, pre := range []int{jobStatusCap - 1, jobStatusCap - 10, jobStatusCap - 19, jobStatusCap - 21, jobStatusCap, jobStatusCap + 5} {
		var b strings.Builder
		b.WriteString(strings.Repeat("h", pre))
		appendCappedTail(&b, "output", strings.Repeat("x", 100)) // must not panic
		if b.Len() > jobStatusCap+20 {
			t.Errorf("pre=%d: appendCappedTail blew the cap: %d bytes", pre, b.Len())
		}
	}
}

// TestAppendCappedTail_Truncates keeps a tail and adds the marker when the
// body exceeds the remaining budget.
func TestAppendCappedTail_Truncates(t *testing.T) {
	var b strings.Builder
	b.WriteString("header\n")
	appendCappedTail(&b, "output", strings.Repeat("x", jobStatusCap*2))
	got := b.String()
	if !strings.Contains(got, "output tail:") || !strings.Contains(got, "…(truncated)") {
		t.Errorf("appendCappedTail = %q, want tail + truncation marker", got)
	}
	if len(got) > jobStatusCap+40 {
		t.Errorf("appendCappedTail result too large: %d bytes", len(got))
	}
}

// TestCommandRealExitCode verifies a command's actual exit code is surfaced
// (not collapsed to 1) in list() and the task_list rendering.
func TestCommandRealExitCode(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "exit 7", t.TempDir(), []string{"sh", "-c", "exit 7"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	m.wg.Wait()
	jobs := m.list()
	if len(jobs) != 1 || jobs[0].ID != id {
		t.Fatalf("list = %+v, want one job %s", jobs, id)
	}
	if jobs[0].Exit == nil || *jobs[0].Exit != 7 {
		t.Fatalf("Exit = %v, want 7", jobs[0].Exit)
	}
	if got := m.formatJobList(); !strings.Contains(got, "error(exit 7)") {
		t.Errorf("formatJobList = %q, want 'error(exit 7)'", got)
	}
}

// TestCommandCancelWithLingeringGrandchild verifies that cancelling a bash_bg
// job whose grandchild still holds the stdout pipe does not wedge the wait
// goroutine: the process-group kill takes the tree down and WaitDelay bounds
// the pipe wait, so wg.Wait returns promptly (this used to hang forever).
func TestCommandCancelWithLingeringGrandchild(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "orphan", t.TempDir(), []string{"bash", "-c", "sleep 60 & echo started"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	// Let the shell start, then cancel the job.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(m.output(id), "started") {
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	done := make(chan struct{})
	go func() { m.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(bgWaitDelay + 5*time.Second):
		t.Fatal("job goroutine still blocked in Wait after cancel (pipe held by grandchild)")
	}
}

// TestSubagentErrorSurfaced verifies a subagent whose turn fails is reported as
// an error — in the parent's completion notice, task_list, and task_status —
// instead of a clean "done".
func TestSubagentErrorSurfaced(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{
					Events: []llm.StreamEvent{{TextDelta: "partial"}},
					Err:    errors.New("provider exploded"),
				},
			),
			ModeLabel: "code",
		}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent session: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "failing task", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	waitForWake(t, rt, parent)
	// JobInfo carries the error.
	var found JobInfo
	for _, j := range rt.jobs.list() {
		if j.ID == id {
			found = j
		}
	}
	if found.Error == "" || !strings.Contains(found.Error, "provider exploded") {
		t.Errorf("JobInfo.Error = %q, want the turn error", found.Error)
	}
	// task_list shows "error", not "done".
	if got := rt.jobs.formatJobList(); !strings.Contains(got, "error") {
		t.Errorf("formatJobList = %q, want 'error'", got)
	}
	// task_status names the error.
	got := rt.jobs.formatJobStatus(id)
	if !strings.Contains(got, "error") || !strings.Contains(got, "provider exploded") {
		t.Errorf("formatJobStatus = %q, want error status + message", got)
	}
	// The parent notice reports the failure (finishSubagent builds it via
	// notifyAgentDone; assert the same rendering path).
	notice := renderNotification(notifyAgentDone(id, "", "provider exploded"))
	if !strings.Contains(notice, "error") {
		t.Errorf("completion notice = %q, want error status", notice)
	}
}

// TestFormatJobCancel_UnknownID returns an error string.
func TestFormatJobCancel_UnknownID(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobCancel("ghost")
	if !strings.Contains(got, "no such task") {
		t.Errorf("formatJobCancel unknown = %q, want 'no such task'", got)
	}
}

// TestFormatJobCancel_KnownJob returns "cancelled task <id>".
func TestFormatJobCancel_KnownJob(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "sleep 60", t.TempDir(), []string{"sleep", "60"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	got := m.formatJobCancel(id)
	if !strings.Contains(got, "cancelled") || !strings.Contains(got, id) {
		t.Errorf("formatJobCancel = %q, want 'cancelled task %s'", got, id)
	}
}

// TestStartSubagentEnforcesAllowlist verifies the task tool's StartSubagent
// path rejects any subagent_type not in the active agent's tools.subagents
// allowlist — the list the task tool's schema advertises.
func TestStartSubagentEnforcesAllowlist(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config {
		cfg := fakeCfg("ok")()
		cfg.Subagents = []string{"explorer"}
		return cfg
	})
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	tc := s.turnConfig()
	if _, err := tc.StartSubagent("privileged", "p", "d"); err == nil ||
		!strings.Contains(err.Error(), "not allowed") || !strings.Contains(err.Error(), "explorer") {
		t.Fatalf("StartSubagent off-list = %v, want not-allowed error naming the allowlist", err)
	}
	if _, err := tc.StartSubagent("explorer", "p", "d"); err != nil {
		t.Fatalf("StartSubagent allowed name rejected: %v", err)
	}
}

// TestStartSubagentEmptyAllowlist verifies an agent with no tools.subagents
// cannot spawn anything (the task tool is not even in its schema).
func TestStartSubagentEmptyAllowlist(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := s.turnConfig().StartSubagent("anything", "p", "d"); err == nil ||
		!strings.Contains(err.Error(), "no subagents") {
		t.Fatalf("StartSubagent with empty allowlist = %v, want no-subagents error", err)
	}
}
