package shell3

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
)

func TestJobManagerCommandLifecycle(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo hi", t.TempDir(), []string{"echo", "hi"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	if got := m.list(); len(got) != 1 || got[0].ID != id || got[0].Kind != JobCommand {
		t.Fatalf("list = %+v, want one JobCommand id=%s", got, id)
	}
	m.wg.Wait()
	if !strings.Contains(m.output(id), "hi") {
		t.Fatalf("output never contained 'hi': %q", m.output(id))
	}
}

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
	id, err := m.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "1"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if _, err := m.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "1"}, nil, notify.ReportAuto, ""); err == nil {
		t.Fatal("expected cap error on second start, got nil")
	}
	if err := m.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	m.wg.Wait()
}

func TestJobManagerRejectsNewWorkAfterShutdownStarts(t *testing.T) {
	m := newJobManager(nil, 8)
	m.cancelAll()
	if _, err := m.startCommand(nil, "echo late", t.TempDir(), []string{"echo", "late"}, nil, notify.ReportAuto, ""); err == nil {
		t.Fatal("startCommand admitted work after cancelAll")
	}
	m.wait()
}

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
	if len(rt.jobs.transcript(id)) == 0 {
		t.Fatalf("transcript for job %s is empty after subagent completion", id)
	}
}

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
	waitForWake(t, rt, parent)
	if len(rt.jobs.transcript(id)) == 0 {
		t.Fatalf("transcript empty after job done for %s", id)
	}
}

func TestJobManagerRetainsDoneCommandJob(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo retained", t.TempDir(), []string{"echo", "retained"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	m.wg.Wait()
	if !strings.Contains(m.output(id), "retained") {
		t.Fatalf("output never contained 'retained': %q", m.output(id))
	}

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

	waitForWake(t, rt, parent)
	var found JobInfo
	deadline := time.Now().Add(3 * time.Second)
	for {
		found = JobInfo{}
		for _, j := range rt.jobs.list() {
			if j.ID == id {
				found = j
				break
			}
		}
		if found.Done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if found.ID == "" {
		t.Fatalf("finished subagent job %s not found in list()", id)
	}
	if !found.Done {
		t.Fatalf("finished subagent job should have Done=true, got %+v", found)
	}
	if len(rt.jobs.transcript(id)) == 0 {
		t.Fatalf("transcript empty after subagent done for job %s", id)
	}
}

func TestJobManagerDoneCap(t *testing.T) {
	m := newJobManager(nil, maxDoneJobs+10)

	for i := 0; i < maxDoneJobs+1; i++ {
		_, err := m.startCommand(nil, "echo x", t.TempDir(), []string{"echo", "x"}, nil, notify.ReportAuto, "")
		if err != nil {
			t.Fatalf("startCommand %d: %v", i, err)
		}
	}

	m.wg.Wait()

	jobs := m.list()
	if len(jobs) > maxDoneJobs {
		t.Fatalf("done-job cap: got %d jobs, want at most %d", len(jobs), maxDoneJobs)
	}
	for _, j := range jobs {
		if !j.Done {
			t.Fatalf("non-done job found after wg.Wait(): %+v", j)
		}
	}
}

func TestJobManagerCancelDoneJobIsNoOp(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo done", t.TempDir(), []string{"echo", "done"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	m.wg.Wait() // wait for the job goroutine to finish
	if err := m.cancel(id, false); err != nil {
		t.Fatalf("cancel on done job should return nil, got %v", err)
	}
}

func TestFormatJobList_Empty(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobList()
	if !strings.Contains(got, "no background") {
		t.Errorf("formatJobList empty = %q, want 'no background'", got)
	}
}

func TestFormatJobList_ShowsRunning(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "sleep 60", t.TempDir(), []string{"sleep", "60"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	defer func() {
		_ = m.cancel(id, false)
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

func TestFormatJobStatus_UnknownID(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobStatus("ghost")
	if !strings.Contains(got, "no such task") {
		t.Errorf("formatJobStatus unknown = %q, want 'no such task'", got)
	}
}

func TestFormatJobStatus_RepeatPollsGetToldToStop(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "sleep 30", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	defer func() { _ = m.cancel(id, false) }()

	first := m.formatJobStatus(id)
	if strings.Contains(first, "end your turn") {
		t.Errorf("first status check must stay plain, got %q", first)
	}
	second := m.formatJobStatus(id)
	if !strings.Contains(second, "end your turn") {
		t.Errorf("repeat poll of a running job must instruct to stop, got %q", second)
	}
}

func TestFormatJobStatus_Truncates(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "echo x", t.TempDir(), []string{"echo", "x"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	big := strings.Repeat("x", jobStatusCap*2)
	_, _ = j.out.Write([]byte(big))

	got := m.formatJobStatus(id)
	if len(got) > jobStatusCap+200 {
		t.Errorf("formatJobStatus result too large: %d bytes (cap ~%d)", len(got), jobStatusCap)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("formatJobStatus %q missing truncation marker", got)
	}
	_ = m.cancel(id, false)
}

func TestAppendCappedTail_NearCapNoPanic(t *testing.T) {
	for _, pre := range []int{jobStatusCap - 1, jobStatusCap - 10, jobStatusCap - 19, jobStatusCap - 21, jobStatusCap, jobStatusCap + 5} {
		var b strings.Builder
		b.WriteString(strings.Repeat("h", pre))
		appendCappedTail(&b, "output", strings.Repeat("x", 100))
		if b.Len() > jobStatusCap+20 {
			t.Errorf("pre=%d: appendCappedTail blew the cap: %d bytes", pre, b.Len())
		}
	}
}

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

func TestCommandRealExitCode(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "exit 7", t.TempDir(), []string{"sh", "-c", "exit 7"}, nil, notify.ReportAuto, "")
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

func TestCommandCancelWithLingeringGrandchild(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "orphan", t.TempDir(), []string{"bash", "-c", "sleep 60 & echo started"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(m.output(id), "started") {
		time.Sleep(10 * time.Millisecond)
	}
	if err := m.cancel(id, false); err != nil {
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

func TestSubagentErrorSurfaced(t *testing.T) {
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{
					Events: []llm.StreamEvent{{TextDelta: "partial"}},
					Err:    errors.New("provider exploded"),
				},
			),
			Agent: "code",
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
	var found JobInfo
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, j := range rt.jobs.list() {
			if j.ID == id {
				found = j
			}
		}
		if found.Error != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if found.Error == "" || !strings.Contains(found.Error, "provider exploded") {
		t.Errorf("JobInfo.Error = %q, want the turn error", found.Error)
	}
	if got := rt.jobs.formatJobList(); !strings.Contains(got, "error") {
		t.Errorf("formatJobList = %q, want 'error'", got)
	}
	got := rt.jobs.formatJobStatus(id)
	if !strings.Contains(got, "error") || !strings.Contains(got, "provider exploded") {
		t.Errorf("formatJobStatus = %q, want error status + message", got)
	}
	notice := renderNotification(notifyAgentDone(id, "", "provider exploded"))
	if !strings.Contains(notice, "error") {
		t.Errorf("completion notice = %q, want error status", notice)
	}
}

func TestFormatJobCancel_UnknownID(t *testing.T) {
	m := newJobManager(nil, 8)
	got := m.formatJobCancel("ghost")
	if !strings.Contains(got, "no such task") {
		t.Errorf("formatJobCancel unknown = %q, want 'no such task'", got)
	}
}

func TestFormatJobCancel_KnownJob(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "sleep 60", t.TempDir(), []string{"sleep", "60"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	got := m.formatJobCancel(id)
	if !strings.Contains(got, "cancelled") || !strings.Contains(got, id) {
		t.Errorf("formatJobCancel = %q, want 'cancelled task %s'", got, id)
	}
}

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
	s.mu.Lock()
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	if _, err := tc.StartSubagent("privileged", "p", "d", notify.ReportAuto, ""); err == nil ||
		!strings.Contains(err.Error(), "not allowed") || !strings.Contains(err.Error(), "explorer") {
		t.Fatalf("StartSubagent off-list = %v, want not-allowed error naming the allowlist", err)
	}
	if _, err := tc.StartSubagent("explorer", "p", "d", notify.ReportAuto, ""); err != nil {
		t.Fatalf("StartSubagent allowed name rejected: %v", err)
	}
}

func TestStartSubagentEmptyAllowlist(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	s.mu.Lock()
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	if _, err := tc.StartSubagent("anything", "p", "d", notify.ReportAuto, ""); err == nil ||
		!strings.Contains(err.Error(), "no subagents") {
		t.Fatalf("StartSubagent with empty allowlist = %v, want no-subagents error", err)
	}
}
