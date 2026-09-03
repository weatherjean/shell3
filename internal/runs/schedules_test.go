package runs

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestScheduleRunLedgerAndOverlap(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	first := ScheduleRun{ID: "run-1", Schedule: "daily", Task: "report", Trigger: "cron", RunDir: "/runs/1", OutputPath: "/runs/1/artifacts/report.md"}
	if err := store.StartScheduleRun(first, "skip"); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.RunDir = "run-2", "/runs/2"
	second.OutputPath = filepath.Join(second.RunDir, "artifacts", "report.md")
	if err := store.StartScheduleRun(second, "skip"); !errors.Is(err, ErrScheduleOverlap) {
		t.Fatalf("overlap error = %v", err)
	}
	if err := store.FinishScheduleRun(first.ID, "done", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.StartScheduleRun(second, "skip"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishScheduleRun(second.ID, "failed", "timeout"); err != nil {
		t.Fatal(err)
	}
	runs, err := store.ListScheduleRuns("daily", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].ID != "run-2" || runs[0].Status != "failed" || runs[0].Error != "timeout" || runs[1].Status != "done" {
		t.Fatalf("history = %+v", runs)
	}
}

func TestScheduleRunAllowsExplicitOverlap(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, id := range []string{"one", "two"} {
		run := ScheduleRun{ID: id, Schedule: "parallel", Task: "work", Trigger: "manual", RunDir: "/" + id, OutputPath: "/" + id + "/out"}
		if err := store.StartScheduleRun(run, "allow"); err != nil {
			t.Fatal(err)
		}
	}
	running, err := store.RunningScheduleRuns()
	if err != nil || len(running) != 2 {
		t.Fatalf("running = %+v, err = %v", running, err)
	}
}
