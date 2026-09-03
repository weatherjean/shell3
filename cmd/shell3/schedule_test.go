//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

func writeScheduleCLIConfig(t *testing.T, dir string) string {
	t.Helper()
	wrkfile := filepath.Join(dir, "scheduled.wrk.lisp")
	if err := os.WriteFile(wrkfile, []byte(`(task "scheduled" (command produce (run "mkdir -p $TASK_ARTIFACTS; printf ready > $TASK_ARTIFACTS/result.txt")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, "shell3.lisp")
	body := `(shell3
  (version 1)
  (schedule probe
    (cron "0 8 * * *")
    (timezone "UTC")
    (run (wrkfile "scheduled.wrk.lisp"))
    (request "Create the probe output.")
    (output "result.txt")
    (timeout "1m")
    (overlap skip)
    (notify "main")))`
	if err := os.WriteFile(config, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return config
}

func TestScheduleRunAndHistoryCommands(t *testing.T) {
	dir := t.TempDir()
	config := writeScheduleCLIConfig(t, dir)
	run := newScheduleRunCommand()
	var runOut bytes.Buffer
	run.SetOut(&runOut)
	run.SetArgs([]string{"--config", config, "--workdir", dir, "probe"})
	if err := run.Execute(); err != nil {
		t.Fatal(err)
	}
	var record runs.ScheduleRun
	if err := json.Unmarshal(runOut.Bytes(), &record); err != nil {
		t.Fatalf("run output = %q: %v", runOut.String(), err)
	}
	if record.Status != "done" {
		t.Fatalf("record = %+v", record)
	}
	logBody, err := os.ReadFile(filepath.Join(dir, ".shell3_project", "errors.jsonl"))
	if err != nil || !strings.Contains(string(logBody), `"event":"schedule.started"`) || !strings.Contains(string(logBody), `"event":"schedule.done"`) {
		t.Fatalf("schedule lifecycle log = %q, err = %v", logBody, err)
	}

	history := newScheduleHistoryCommand()
	var historyOut bytes.Buffer
	history.SetOut(&historyOut)
	history.SetArgs([]string{"--workdir", dir, "--status", "done", "probe"})
	if err := history.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(historyOut.String(), `"schedule":"probe"`) || !strings.Contains(historyOut.String(), `"status":"done"`) {
		t.Fatalf("history = %q", historyOut.String())
	}
}

func TestServiceRunsUntilContextCancellation(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "shell3-service-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	config := writeScheduleCLIConfig(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	command := newServiceCommand()
	command.SetContext(ctx)
	var out bytes.Buffer
	command.SetOut(&out)
	command.SetArgs([]string{"--config", config, "--workdir", dir})
	done := make(chan error, 1)
	go func() { done <- command.Execute() }()
	deadline := time.Now().Add(3 * time.Second)
	lockPath := filepath.Join(dir, ".shell3_project", "schedule.lock")
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(lockPath); err != nil {
		cancel()
		t.Fatalf("service did not acquire its schedule lock: %v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("service did not stop after cancellation")
	}
	if !strings.Contains(out.String(), "1 schedule(s) active") {
		t.Fatalf("service output = %q", out.String())
	}
}

func TestConfigCheckValidatesScheduledWrkfile(t *testing.T) {
	dir := t.TempDir()
	config := writeScheduleCLIConfig(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "scheduled.wrk.lisp"), []byte(`(task "broken")`), 0o600); err != nil {
		t.Fatal(err)
	}
	command := newLispConfigCheckCommand()
	command.SetArgs([]string{config})
	if err := command.Execute(); err == nil || !strings.Contains(err.Error(), "task must declare at least one node") {
		t.Fatalf("error = %v", err)
	}
}
