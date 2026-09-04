package shell3

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
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
	if j != nil && suppressCompletion {
		j.suppress = true
	}
	var cancelFn context.CancelFunc
	if j != nil {
		cancelFn = j.cancel
	}
	m.mu.Unlock()
	if j == nil {
		return fmt.Errorf("no such task %q", id)
	}
	if cancelFn != nil {
		cancelFn()
	}
	return nil
}

func TestJobManagerCommandLifecycle(t *testing.T) {
	m := newJobManager(nil, 8)
	_, err := m.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "0.1"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	m.mu.Lock()
	running := m.runningCount()
	m.mu.Unlock()
	if running != 1 {
		t.Fatalf("running count = %d, want 1", running)
	}
	m.wg.Wait()
	m.mu.Lock()
	running = m.runningCount()
	m.mu.Unlock()
	if running != 0 {
		t.Fatalf("running count after completion = %d, want 0", running)
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
	if err := m.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	m.wg.Wait()
}

func TestJobManagerRejectsNewWorkAfterShutdownStarts(t *testing.T) {
	m := newJobManager(nil, 8)
	m.cancelAll()
	if _, err := m.startCommand(nil, "echo late", t.TempDir(), []string{"echo", "late"}, nil); err == nil {
		t.Fatal("startCommand admitted work after cancelAll")
	}
	m.wait()
}

func TestCommandCancelWithLingeringGrandchild(t *testing.T) {
	m := newJobManager(nil, 8)
	id, err := m.startCommand(nil, "orphan", t.TempDir(), []string{"bash", "-c", "sleep 60 & echo started"}, nil)
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
