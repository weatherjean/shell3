//go:build unix

package wrk

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestInspectDistinguishesLiveInterruptedAndExpired(t *testing.T) {
	_, dir := startCommandRun(t, `(task "inspect" (timeout "1h") (command work (run "true")))`)
	s, err := Inspect(dir)
	if err != nil || s.Status != "ready" || s.ExecutionActive || s.RecoveryRequired {
		t.Fatalf("fresh: %+v %v", s, err)
	}
	// Reproduce a legacy crash: root still ready, node claims running, with
	// unrelated artifacts present. Neither file is proof of a live execution.
	if err := writeStatus(filepath.Join(dir, "nodes", "work"), "running"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "artifacts", "manual.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = Inspect(dir)
	if err != nil || s.Status != "interrupted" || s.ExecutionActive || !s.RecoveryRequired || s.Nodes[0].Status != "interrupted" || s.Nodes[0].PersistedStatus != "running" {
		t.Fatalf("abandoned: %+v %v", s, err)
	}
	if raw, _ := readStatus(dir); raw != "ready" {
		t.Fatal("inspection mutated state")
	}
	lease, err := lockRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	s, err = Inspect(dir)
	unlockRun(lease)
	if err != nil || s.Status != "running" || !s.ExecutionActive || s.RecoveryRequired {
		t.Fatalf("live: %+v %v", s, err)
	}
	// Inspectors use shared locks, so one reader cannot impersonate a beat.
	reader, active, err := inspectLease(dir)
	if err != nil || active || reader == nil {
		t.Fatalf("lease: %v %v", active, err)
	}
	s, err = Inspect(dir)
	_ = reader.Close()
	if err != nil || s.ExecutionActive {
		t.Fatalf("reader appeared live: %+v %v", s, err)
	}
	var m Manifest
	if err := readJSON(filepath.Join(dir, "run.json"), &m); err != nil {
		t.Fatal(err)
	}
	m.Deadline = time.Now().Add(-time.Second)
	if err := writeJSON(filepath.Join(dir, "run.json"), m); err != nil {
		t.Fatal(err)
	}
	s, err = Inspect(dir)
	if err != nil || s.Status != "expired" || !s.DeadlineExceeded || !s.RecoveryRequired || s.ExecutionActive {
		t.Fatalf("expired: %+v %v", s, err)
	}
	if _, err := Beat(t.Context(), dir); err == nil {
		t.Fatal("expired beat did not fail")
	}
	s, err = Inspect(dir)
	if err != nil || s.Status != "failed" || s.RecoveryRequired || s.ExecutionActive {
		t.Fatalf("final: %+v %v", s, err)
	}
}

func TestCancelledDriverRecordsInterruptionAndCanResume(t *testing.T) {
	root, dir := startCommandRun(t, `(task "interrupt" (command work (run "if test ! -f started; then touch started; sleep 30; fi; printf recovered > done")))`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Beat(ctx, dir); done <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not start")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, err := Inspect(dir)
	if err != nil || s.Status != "running" || !s.ExecutionActive {
		t.Fatalf("active: %+v %v", s, err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("cancel did not return")
	}
	s, err = Inspect(dir)
	if err != nil || s.Status != "interrupted" || s.PersistedStatus != "interrupted" || s.Nodes[0].PersistedStatus != "interrupted" || !s.RecoveryRequired {
		t.Fatalf("interrupted: %+v %v", s, err)
	}
	if b, err := Beat(t.Context(), dir); err != nil || b.Status != "completed" {
		t.Fatalf("resume: %+v %v", b, err)
	}
	if body, _ := os.ReadFile(filepath.Join(root, "done")); string(body) != "recovered" {
		t.Fatal("resume did not execute")
	}
}

func TestOwnDeadlineRecordsTerminalFailureImmediately(t *testing.T) {
	_, dir := startCommandRun(t, `(task "expire" (timeout "100ms") (command work (run "sleep 30")))`)
	b, err := Beat(t.Context(), dir)
	if err == nil || b.Status != "failed" {
		t.Fatalf("deadline: %+v %v", b, err)
	}
	s, err := Inspect(dir)
	if err != nil || s.Status != "failed" || s.ExecutionActive {
		t.Fatalf("deadline snapshot: %+v %v", s, err)
	}
}

type brokenProgress struct{ calls int }

func (w *brokenProgress) Write([]byte) (int, error) { w.calls++; return 0, syscall.EPIPE }

func TestClosedProgressConsumerDoesNotInterruptWork(t *testing.T) {
	_, dir := startCommandRun(t, `(task "pipe" (command work (run "printf useful-output") (accept (sh "true"))))`)
	w := &brokenProgress{}
	b, err := BeatWithProgress(t.Context(), dir, w)
	if err != nil || b.Status != "completed" {
		t.Fatalf("closed consumer: %+v %v", b, err)
	}
	if w.calls != 1 {
		t.Fatalf("repeated writes to closed consumer: %d", w.calls)
	}
	body, err := os.ReadFile(filepath.Join(dir, "nodes", "work", "command.log"))
	if err != nil || !strings.Contains(string(body), "useful-output") {
		t.Fatalf("durable output lost: %s %v", body, err)
	}
}

func TestInspectRejectsLockIOFailure(t *testing.T) {
	_, dir := startCommandRun(t, `(task "badlock" (command work (run "true")))`)
	path := filepath.Join(dir, "beat.lock")
	// A missing parent component is an actual I/O error, never evidence of an owner.
	if err := os.Symlink(filepath.Join(path, "missing"), path); err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(dir); err == nil {
		t.Fatal("invalid lock presented as live or idle")
	}
}
