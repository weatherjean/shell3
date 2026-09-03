package shell3

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
)

func (m *jobManager) output(id string) string {
	m.mu.Lock()
	j := m.jobs[id]
	m.mu.Unlock()
	if j != nil && j.out != nil {
		return j.out.String()
	}
	return ""
}

func (m *jobManager) cancel(id string, suppressCompletion bool) error {
	m.mu.Lock()
	j := m.jobs[id]
	if j != nil && suppressCompletion && !j.finished {
		j.suppress = true
	}
	finished := j != nil && j.finished
	var cancelFn context.CancelFunc
	if j != nil {
		cancelFn = j.cancel
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Errorf("no such task %q", id)
	}
	if !finished && cancelFn != nil {
		cancelFn()
	}
	return nil
}

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
