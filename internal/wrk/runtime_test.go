//go:build unix

package wrk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
)

func startCommandRun(t *testing.T, definition string) (string, string) {
	return startCommandRunWithOptions(t, definition, nil)
}

func startCommandRunWithOptions(t *testing.T, definition string, configure func(*StartOptions)) (string, string) {
	t.Helper()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	definitionPath := filepath.Join(dir, "test.wrk.lisp")
	if err := os.WriteFile(configPath, []byte("(shell3 (version 1))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definitionPath, []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := StartOptions{
		StateRoot: filepath.Join(dir, "state"), RunID: "test-run", Shell3Bin: "/bin/false", Request: "test request",
	}
	if configure != nil {
		configure(&opts)
	}
	runDir, err := Start(configPath, definitionPath, opts)
	if err != nil {
		t.Fatal(err)
	}
	return dir, runDir
}

func TestBeatAdvancesParallelReadersThenWriter(t *testing.T) {
	dir, runDir := startCommandRun(t, `(task "waves"
  (parallel 2)
  (command read-a (access read) (timeout "2s")
    (run "touch a.started; while test ! -f b.started; do sleep 0.01; done"))
  (command read-b (access read) (timeout "2s")
    (run "touch b.started; while test ! -f a.started; do sleep 0.01; done"))
  (command write (after read-a read-b) (access write)
    (run "printf done > final.txt")))`)

	first, err := Beat(context.Background(), runDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "ready" || !slices.Equal(first.Ran, []string{"read-a", "read-b"}) {
		t.Fatalf("first beat = %+v", first)
	}
	if _, err := os.Stat(filepath.Join(dir, "final.txt")); !os.IsNotExist(err) {
		t.Fatalf("writer ran in reader beat: %v", err)
	}
	second, err := Beat(context.Background(), runDir)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "completed" || !slices.Equal(second.Ran, []string{"write"}) {
		t.Fatalf("second beat = %+v", second)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "final.txt")); string(got) != "done" {
		t.Fatalf("final = %q", got)
	}
}

func TestBeatRecoversRunningMarkerAtLeastOnce(t *testing.T) {
	dir, runDir := startCommandRun(t, `(task "recover" (command work (run "printf recovered > recovered.txt")))`)
	if err := writeStatus(filepath.Join(runDir, "nodes", "work"), "running"); err != nil {
		t.Fatal(err)
	}
	result, err := Beat(context.Background(), runDir)
	if err != nil || result.Status != "completed" {
		t.Fatalf("beat = %+v, %v", result, err)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "recovered.txt")); string(got) != "recovered" {
		t.Fatalf("recovered output = %q", got)
	}
}

func TestBeatWithProgressMirrorsCommandAndLifecycle(t *testing.T) {
	_, runDir := startCommandRun(t, `(task "progress"
  (command work
    (run "printf 'command output\\n'; printf 'command diagnostic\\n' >&2")
    (accept (sh "printf 'check output\\n'"))))`)
	var progress strings.Builder
	result, err := BeatWithProgress(context.Background(), runDir, &progress)
	if err != nil || result.Status != "completed" {
		t.Fatalf("beat = %+v, %v", result, err)
	}
	got := progress.String()
	for _, want := range []string{"[wrk] work: starting", "command output", "command diagnostic", "[wrk] work: verifying", "check output", "[wrk] work: passed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("progress %q does not contain %q", got, want)
		}
	}
	commandLog, err := os.ReadFile(filepath.Join(runDir, "nodes", "work", "command.log"))
	if err != nil || !strings.Contains(string(commandLog), "command output") || !strings.Contains(string(commandLog), "command diagnostic") {
		t.Fatalf("command log = %q, err = %v", commandLog, err)
	}
}

func TestBeatWithProgressMirrorsAgentStreamWithoutDuplicatingResult(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	definitionPath := filepath.Join(dir, "agent.wrk.lisp")
	shell3Bin := filepath.Join(dir, "fake-shell3")
	if err := os.WriteFile(configPath, []byte(`(shell3 (version 1)
  (runner fake (command "/bin/true") (result stdout))
  (agent worker (using fake)))`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definitionPath, []byte(`(task "agent-progress" (agent work (using worker) (prompt "work")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shell3Bin, []byte("#!/bin/sh\nprintf 'live runner output\\n' >&2\nprintf 'final result\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runDir, err := Start(configPath, definitionPath, StartOptions{
		StateRoot: filepath.Join(dir, "state"), RunID: "test-run", Shell3Bin: shell3Bin,
	})
	if err != nil {
		t.Fatal(err)
	}
	var progress strings.Builder
	result, err := BeatWithProgress(context.Background(), runDir, &progress)
	if err != nil || result.Status != "completed" {
		t.Fatalf("beat = %+v, %v", result, err)
	}
	if got := progress.String(); !strings.Contains(got, "live runner output") || strings.Contains(got, "final result") {
		t.Fatalf("progress = %q", got)
	}
	resultFile, err := os.ReadFile(filepath.Join(runDir, "nodes", "work", "result-1.md"))
	if err != nil || string(resultFile) != "final result\n" {
		t.Fatalf("result file = %q, err = %v", resultFile, err)
	}
}

func TestBeatFailsClosedOnChangedSnapshotAndCorruptStatus(t *testing.T) {
	_, runDir := startCommandRun(t, `(task "strict" (command work (run "true")))`)
	if err := os.WriteFile(filepath.Join(runDir, "task.wrk.lisp"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Beat(context.Background(), runDir); err == nil || !strings.Contains(err.Error(), "snapshot hash mismatch") {
		t.Fatalf("snapshot error = %v", err)
	}

	_, runDir = startCommandRun(t, `(task "strict" (command work (run "true")))`)
	if err := writeStatus(filepath.Join(runDir, "nodes", "work"), "mystery"); err != nil {
		t.Fatal(err)
	}
	if _, err := Beat(context.Background(), runDir); err == nil || !strings.Contains(err.Error(), "invalid status") {
		t.Fatalf("status error = %v", err)
	}
}

func TestBeatRecordsFailureAndQuiescentWait(t *testing.T) {
	_, failedDir := startCommandRun(t, `(task "failure" (command nope (run "exit 7")))`)
	failed, err := Beat(context.Background(), failedDir)
	if err != nil || failed.Status != "failed" {
		t.Fatalf("failed beat = %+v, %v", failed, err)
	}
	status, _ := os.ReadFile(filepath.Join(failedDir, "nodes", "nope", "status"))
	if strings.TrimSpace(string(status)) != "failed" {
		t.Fatalf("node status = %q", status)
	}

	_, waitDir := startCommandRun(t, `(task "waiting" (wait approval (for (event "approved")) (message "Need approval.")))`)
	waiting, err := Beat(context.Background(), waitDir)
	if err != nil || waiting.Status != "waiting" || !slices.Equal(waiting.Ran, []string{"approval"}) {
		t.Fatalf("waiting beat = %+v, %v", waiting, err)
	}
	again, err := Beat(context.Background(), waitDir)
	if err != nil || again.Status != "waiting" || len(again.Ran) != 0 {
		t.Fatalf("quiescent beat = %+v, %v", again, err)
	}
}

func TestBeatUsesSnapshotsAndNotifiesTerminalOnce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	definitionPath := filepath.Join(dir, "notify.wrk.lisp")
	if err := os.WriteFile(configPath, []byte("(shell3 (version 1))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definitionPath, []byte(`(task "notify" (command finish (run "printf snapshot > snapshot.txt")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	notifyRoot := filepath.Join(dir, "messages")
	runDir, err := Start(configPath, definitionPath, StartOptions{
		StateRoot: filepath.Join(dir, "state"), RunID: "notify-run", Shell3Bin: "/bin/false",
		NotifyTo: "main", NotifyState: notifyRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definitionPath, []byte(`(task "notify" (command finish (run "exit 9")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result, err := Beat(context.Background(), runDir)
		if err != nil || result.Status != "completed" {
			t.Fatalf("beat %d = %+v, %v", i, result, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "snapshot.txt")); string(got) != "snapshot" {
		t.Fatalf("run followed edited source: %q", got)
	}
	var messages int
	err = filepath.Walk(filepath.Join(notifyRoot, "inbox"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".json") {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			var message map[string]any
			if err := json.Unmarshal(data, &message); err != nil {
				return err
			}
			messages++
		}
		return err
	})
	if err != nil || messages != 1 {
		t.Fatalf("notifications = %d, err = %v", messages, err)
	}
}

func TestTerminalStatusIsNotReinterpretedAfterOutputRemoval(t *testing.T) {
	dir, runDir := startCommandRunWithOptions(t, `(task "terminal-output"
  (command finish (run "printf accepted > $TASK_ARTIFACTS/result.txt")))`, func(opts *StartOptions) {
		opts.RequiredOutput = "result.txt"
		opts.NotifyTo = "main"
		opts.NotifyState = filepath.Join(filepath.Dir(opts.StateRoot), "messages")
	})

	result, err := Beat(context.Background(), runDir)
	if err != nil || result.Status != "completed" {
		t.Fatalf("first beat = %+v, %v", result, err)
	}
	if err := os.Remove(filepath.Join(runDir, "artifacts", "result.txt")); err != nil {
		t.Fatal(err)
	}
	result, err = Beat(context.Background(), runDir)
	if err != nil || result.Status != "completed" || len(result.Ran) != 0 {
		t.Fatalf("terminal beat = %+v, %v", result, err)
	}
	status, err := os.ReadFile(filepath.Join(runDir, "status"))
	if err != nil || strings.TrimSpace(string(status)) != "completed" {
		t.Fatalf("status = %q, %v", status, err)
	}
	notices, total, err := (inbox.Store{Root: filepath.Join(dir, "messages")}).List("main", inbox.StatusNew, 0, 10)
	if err != nil || total != 1 || notices[0].Message.Event != "wrk.completed" {
		t.Fatalf("notices = %+v, total = %d, err = %v", notices, total, err)
	}
}

func TestRequiredOutputRejectsDirectoriesAndSymlinks(t *testing.T) {
	tests := []struct {
		name    string
		command string
		output  string
		want    string
	}{
		{name: "directory", command: `mkdir -p $TASK_ARTIFACTS/result`, output: "result", want: "not a regular file"},
		{name: "final symlink", command: `printf ok > target; ln -s $PWD/target $TASK_ARTIFACTS/result`, output: "result", want: "contains a symlink"},
		{name: "symlink directory", command: `mkdir outside; ln -s $PWD/outside $TASK_ARTIFACTS/nested; printf ok > outside/result`, output: "nested/result", want: "contains a symlink"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, runDir := startCommandRunWithOptions(t, `(task "required-output" (command produce (run "`+tt.command+`")))`, func(opts *StartOptions) {
				opts.RequiredOutput = tt.output
			})
			result, err := Beat(context.Background(), runDir)
			if err == nil || result.Status != "failed" || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("beat = %+v, error = %v", result, err)
			}
		})
	}
}

func TestSignalIsDurableBeforeAndAfterWait(t *testing.T) {
	_, preSignalled := startCommandRun(t, `(task "pre" (wait approval (for (event "approved"))))`)
	event, err := Signal(preSignalled, "approved", "operator approved")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Beat(context.Background(), preSignalled)
	if err != nil || result.Status != "completed" {
		t.Fatalf("pre-signalled beat = %+v, %v", result, err)
	}
	var consumed ExternalEvent
	if err := readJSON(filepath.Join(preSignalled, "nodes", "approval", "event.json"), &consumed); err != nil || consumed.ID != event.ID {
		t.Fatalf("consumed event = %+v, %v", consumed, err)
	}

	_, waitingRun := startCommandRun(t, `(task "post" (wait approval (for (event "approved"))))`)
	result, err = Beat(context.Background(), waitingRun)
	if err != nil || result.Status != "waiting" {
		t.Fatalf("initial wait = %+v, %v", result, err)
	}
	if _, err := Signal(waitingRun, "approved", "later"); err != nil {
		t.Fatal(err)
	}
	result, err = Beat(context.Background(), waitingRun)
	if err != nil || result.Status != "completed" {
		t.Fatalf("released wait = %+v, %v", result, err)
	}
	snapshot, err := Inspect(waitingRun)
	if err != nil || snapshot.Status != "completed" || len(snapshot.Nodes) != 1 || snapshot.Nodes[0].Status != "passed" {
		t.Fatalf("snapshot = %+v, %v", snapshot, err)
	}
}

func TestCancelInterruptsActiveBeat(t *testing.T) {
	dir, runDir := startCommandRun(t, `(task "cancel-live"
  (command slow (run "touch started; sleep 30; touch should-not-exist")))`)
	type outcome struct {
		result BeatResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := Beat(context.Background(), runDir)
		done <- outcome{result: result, err: err}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := Cancel(runDir); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-done:
		if got.err != nil || got.result.Status != "cancelled" {
			t.Fatalf("cancelled beat = %+v, %v", got.result, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not interrupt the active child")
	}
	if _, err := os.Stat(filepath.Join(dir, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("cancelled command reached its tail: %v", err)
	}
	snapshot, err := Inspect(runDir)
	if err != nil || snapshot.Status != "cancelled" {
		t.Fatalf("cancelled snapshot = %+v, %v", snapshot, err)
	}
	again, err := Beat(context.Background(), runDir)
	if err != nil || again.Status != "cancelled" || len(again.Ran) != 0 {
		t.Fatalf("post-cancel beat = %+v, %v", again, err)
	}
}

func TestTerminalRunRejectsSignalAndCancel(t *testing.T) {
	_, runDir := startCommandRun(t, `(task "terminal" (command finish (run "true")))`)
	result, err := Beat(context.Background(), runDir)
	if err != nil || result.Status != "completed" {
		t.Fatalf("beat = %+v, %v", result, err)
	}
	if _, err := Signal(runDir, "late", "too late"); err == nil || !strings.Contains(err.Error(), "cannot signal terminal") {
		t.Fatalf("signal error = %v", err)
	}
	if err := Cancel(runDir); err == nil || !strings.Contains(err.Error(), "cannot cancel terminal") {
		t.Fatalf("cancel error = %v", err)
	}
}

func TestCancelNotifiesOnce(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	definitionPath := filepath.Join(dir, "cancel.wrk.lisp")
	if err := os.WriteFile(configPath, []byte("(shell3 (version 1))\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(definitionPath, []byte(`(task "cancel-notify" (wait hold (for (event "release"))))`), 0o600); err != nil {
		t.Fatal(err)
	}
	notifyRoot := filepath.Join(dir, "messages")
	runDir, err := Start(configPath, definitionPath, StartOptions{
		StateRoot: filepath.Join(dir, "state"), RunID: "cancel-run", Shell3Bin: "/bin/false",
		NotifyTo: "main", NotifyState: notifyRoot,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := Cancel(runDir); err != nil {
		t.Fatal(err)
	}
	if err := Cancel(runDir); err != nil {
		t.Fatal(err)
	}
	var records [][]byte
	err = filepath.Walk(filepath.Join(notifyRoot, "inbox"), func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.HasSuffix(path, ".json") {
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			records = append(records, data)
		}
		return err
	})
	if err != nil || len(records) != 1 || !strings.Contains(string(records[0]), `"event": "wrk.cancelled"`) {
		t.Fatalf("cancel notifications = %q, err = %v", records, err)
	}
}
