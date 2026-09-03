//go:build unix

package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/wrk"
)

func scheduleFixture(t *testing.T, command, output string, timeout time.Duration) (string, string, *lispconfig.Config, *runs.Store) {
	t.Helper()
	dir := t.TempDir()
	wrkfile := filepath.Join(dir, "daily.wrk.lisp")
	definition := `(task "daily" (command produce (run "` + command + `")))`
	if err := os.WriteFile(wrkfile, []byte(definition), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "shell3.lisp")
	config := `(shell3
  (version 1)
  (schedule report
    (cron "* * * * *")
    (timezone "UTC")
    (run (wrkfile "daily.wrk.lisp"))
    (output "` + output + `")
    (timeout "` + timeout.String() + `")
    (overlap skip)
    (notify "main")))`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := lispconfig.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := runs.Open(filepath.Join(dir, ".shell3_project"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return dir, configPath, cfg, store
}

func TestRunCompletesOnlyWithDeclaredOutput(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t,
		`mkdir -p $TASK_ARTIFACTS; printf ok > $TASK_ARTIFACTS/report.md`, "report.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := executor.Run(context.Background(), "report", "manual", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "done" {
		t.Fatalf("run = %+v", run)
	}
	if body, err := os.ReadFile(run.OutputPath); err != nil || string(body) != "ok" {
		t.Fatalf("output = %q, err = %v", body, err)
	}
	notices, total, err := (inbox.Store{Root: filepath.Join(dir, ".shell3_project")}).List("main", inbox.StatusNew, 0, 10)
	if err != nil || total != 1 || notices[0].Message.Event != "wrk.completed" {
		t.Fatalf("completion notices = %+v, total = %d, err = %v", notices, total, err)
	}
	history, err := store.ListScheduleRuns("report", "done", 10)
	if err != nil || len(history) != 1 || history[0].OutputPath != run.OutputPath {
		t.Fatalf("history = %+v, err = %v", history, err)
	}
}

func TestRunFailsWhenDeclaredOutputIsMissing(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t, `true`, "missing.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := executor.Run(context.Background(), "report", "manual", time.Now())
	if err == nil || !strings.Contains(err.Error(), "required output") {
		t.Fatalf("error = %v", err)
	}
	if run.Status != "failed" || !strings.Contains(run.Error, "required output") {
		t.Fatalf("run = %+v", run)
	}
	history, listErr := store.ListScheduleRuns("report", "failed", 10)
	if listErr != nil || len(history) != 1 {
		t.Fatalf("history = %+v, err = %v", history, listErr)
	}
	notices, total, noticeErr := (inbox.Store{Root: filepath.Join(dir, ".shell3_project")}).List("main", inbox.StatusNew, 0, 10)
	if noticeErr != nil || total != 1 || notices[0].Message.Event != "wrk.failed" {
		t.Fatalf("failure notices = %+v, total = %d, err = %v", notices, total, noticeErr)
	}
}

func TestTerminalLedgerWaitsForDurableNotice(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t,
		`mkdir -p $TASK_ARTIFACTS; printf ok > $TASK_ARTIFACTS/report.md`, "report.md", time.Minute)
	inboxPath := filepath.Join(dir, ".shell3_project", "inbox")
	if err := os.WriteFile(inboxPath, []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	run, err := executor.Run(context.Background(), "report", "manual", time.Now())
	if err == nil || run.Status != "running" {
		t.Fatalf("run = %+v, error = %v", run, err)
	}
	running, err := store.RunningScheduleRuns()
	if err != nil || len(running) != 1 {
		t.Fatalf("running ledger = %+v, err = %v", running, err)
	}
	if err := os.Remove(inboxPath); err != nil {
		t.Fatal(err)
	}
	run, err = executor.Resume(context.Background(), running[0])
	if err != nil || run.Status != "done" {
		t.Fatalf("resumed run = %+v, error = %v", run, err)
	}
	notices, total, err := (inbox.Store{Root: filepath.Join(dir, ".shell3_project")}).List("main", inbox.StatusNew, 0, 10)
	if err != nil || total != 1 || notices[0].Message.Event != "wrk.completed" {
		t.Fatalf("completion notices = %+v, total = %d, err = %v", notices, total, err)
	}
}

func TestTerminalLedgerKeepsAcceptedCompletionAfterOutputRemoval(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t,
		`mkdir -p $TASK_ARTIFACTS; printf ok > $TASK_ARTIFACTS/report.md`, "report.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	job := executor.Jobs["report"]
	runID := "accepted-output"
	runDir := filepath.Join(executor.StateRoot, job.Task, runID)
	record := runs.ScheduleRun{
		ID: runID, Schedule: "report", Task: job.Task, Trigger: "manual", RunDir: runDir,
		OutputPath: filepath.Join(runDir, "artifacts", "report.md"), StartedAt: time.Now(), Status: "running",
	}
	if err := store.StartScheduleRun(record, "skip"); err != nil {
		t.Fatal(err)
	}
	if _, err := wrk.Start(configPath, job.Wrkfile, wrk.StartOptions{
		StateRoot: executor.StateRoot, RunID: runID, Shell3Bin: executor.Shell3Bin,
		NotifyTo: "main", NotifyState: filepath.Join(dir, ".shell3_project"), RequiredOutput: "report.md", Timeout: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := wrk.Beat(context.Background(), runDir)
	if err != nil || result.Status != "completed" {
		t.Fatalf("terminal beat = %+v, %v", result, err)
	}
	if err := os.Remove(record.OutputPath); err != nil {
		t.Fatal(err)
	}
	run, err := executor.Resume(context.Background(), record)
	if err != nil || run.Status != "done" {
		t.Fatalf("resumed run = %+v, %v", run, err)
	}
	notices, total, err := (inbox.Store{Root: filepath.Join(dir, ".shell3_project")}).List("main", inbox.StatusNew, 0, 10)
	if err != nil || total != 1 || notices[0].Message.Event != "wrk.completed" {
		t.Fatalf("notices = %+v, total = %d, err = %v", notices, total, err)
	}
}

func TestRunTimeoutIsPersistedAsFailure(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t, `sleep 5`, "report.md", 100*time.Millisecond)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	run, err := executor.Run(context.Background(), "report", "manual", time.Now())
	if err == nil || !strings.Contains(err.Error(), "timeout exceeded") {
		t.Fatalf("error = %v", err)
	}
	if run.Status != "failed" || !strings.Contains(run.Error, "timeout exceeded") {
		t.Fatalf("run = %+v", run)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("timeout took too long: %s", time.Since(started))
	}
}

func TestRunSkipsOverlapAtomically(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t, `true`, "report.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	job := executor.Jobs["report"]
	blocking := runs.ScheduleRun{
		ID: "already-running", Schedule: "report", Task: job.Task, Trigger: "cron",
		RunDir: "/tmp/already", OutputPath: "/tmp/already/out",
	}
	if err := store.StartScheduleRun(blocking, "skip"); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Run(context.Background(), "report", "manual", time.Now()); !errors.Is(err, runs.ErrScheduleOverlap) {
		t.Fatalf("overlap error = %v", err)
	}
}

func TestRunRejectsChangedScheduledTaskIdentity(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t, `true`, "report.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "daily.wrk.lisp"), []byte(`(task "renamed" (command produce (run "true")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	run, err := executor.Run(context.Background(), "report", "manual", time.Now())
	if err == nil || !strings.Contains(err.Error(), `task changed from "daily" to "renamed"`) {
		t.Fatalf("run = %+v, error = %v", run, err)
	}
	if run.Status != "failed" || run.ID == "" {
		t.Fatalf("failed run = %+v", run)
	}
	if _, err := os.Stat(filepath.Join(executor.StateRoot, "renamed", run.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("changed task created an orphan run: %v", err)
	}
}

func TestManagerOwnsScheduleClockExclusively(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t, `true`, "report.md", time.Minute)
	first, err := Start(context.Background(), configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Start(context.Background(), configPath, dir, cfg, store, applog.Noop{})
	if err == nil {
		second.Close()
		t.Fatal("second schedule owner started")
	}
	if !strings.Contains(err.Error(), "another persistent shell3 process") {
		t.Fatalf("error = %v", err)
	}
	first.Close()
	third, err := Start(context.Background(), configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatalf("schedule lock remained held after close: %v", err)
	}
	third.Close()
}

func TestSameDeclarationsRequiresExactOrderedMatch(t *testing.T) {
	a := []lispconfig.Schedule{{Name: "a", Cron: "0 8 * * *", Timezone: "UTC"}, {Name: "b"}}
	b := append([]lispconfig.Schedule(nil), a...)
	if !SameDeclarations(a, b) {
		t.Fatal("identical schedules reported changed")
	}
	b[0].Cron = "0 9 * * *"
	if SameDeclarations(a, b) {
		t.Fatal("changed schedule reported identical")
	}
	if SameDeclarations(a, []lispconfig.Schedule{a[1], a[0]}) {
		t.Fatal("reordered schedules reported identical")
	}
}

func TestManagerRecoversRunningInvocationAfterScheduleRemoval(t *testing.T) {
	dir, configPath, cfg, store := scheduleFixture(t,
		`mkdir -p $TASK_ARTIFACTS; printf recovered > $TASK_ARTIFACTS/report.md`, "report.md", time.Minute)
	executor, err := NewExecutor(configPath, dir, cfg, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	job := executor.Jobs["report"]
	runID := "recover-me"
	runDir := filepath.Join(executor.StateRoot, job.Task, runID)
	record := runs.ScheduleRun{
		ID: runID, Schedule: "report", Task: job.Task, Trigger: "cron", RunDir: runDir,
		OutputPath: filepath.Join(runDir, "artifacts", "report.md"), StartedAt: time.Now(),
	}
	if err := store.StartScheduleRun(record, "skip"); err != nil {
		t.Fatal(err)
	}
	if _, err := wrk.Start(configPath, job.Wrkfile, wrk.StartOptions{
		StateRoot: executor.StateRoot, RunID: runID, RequiredOutput: "report.md", Timeout: time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	withoutSchedule, err := lispconfig.Parse("empty.lisp", []byte(`(shell3 (version 1))`))
	if err != nil {
		t.Fatal(err)
	}
	manager, err := Start(context.Background(), configPath, dir, withoutSchedule, store, applog.Noop{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		history, err := store.ListScheduleRuns("report", "done", 1)
		if err == nil && len(history) == 1 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running invocation was not recovered")
}

type recordingLogger struct {
	errors []string
}

func (*recordingLogger) Debug(string, ...any) {}
func (*recordingLogger) Info(string, ...any)  {}
func (*recordingLogger) Warn(string, ...any)  {}
func (l *recordingLogger) Error(message string, err error, _ ...any) {
	l.errors = append(l.errors, message+": "+err.Error())
}

func TestManagerDeduplicatesRepeatedRecoveryDiagnostics(t *testing.T) {
	log := &recordingLogger{}
	m := &Manager{executor: &Executor{Log: log}, reported: map[string]string{}}

	m.report("run:one", "schedule recovery returned", errors.New("inbox unavailable"))
	m.report("run:one", "schedule recovery returned", errors.New("inbox unavailable"))
	if len(log.errors) != 1 {
		t.Fatalf("repeated diagnostics = %v", log.errors)
	}

	m.report("run:one", "schedule recovery returned", errors.New("ledger unavailable"))
	m.clearReported("run:one")
	m.report("run:one", "schedule recovery returned", errors.New("ledger unavailable"))
	if len(log.errors) != 3 {
		t.Fatalf("changed or recovered diagnostics = %v", log.errors)
	}
}
