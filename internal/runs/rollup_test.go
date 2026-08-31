package runs_test

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

func TestCronRollup_SumsByJob(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		id, err := st.NewSession(runs.Meta{Agent: "bookmarks", CronJob: "sync"})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.AddUsage(id, 17000, 40); err != nil {
			t.Fatal(err)
		}
	}
	id, err := st.NewSession(runs.Meta{Agent: "bookmarks", CronJob: "bookmarks-tick"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(id, 46000, 3000); err != nil {
		t.Fatal(err)
	}

	got, err := st.CronRollup(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	byJob := map[string]runs.JobCost{}
	for _, c := range got {
		byJob[c.CronJob] = c
	}
	if byJob["sync"].PromptTokens != 51000 || byJob["sync"].Runs != 3 {
		t.Fatalf("sync rollup = %+v", byJob["sync"])
	}
	if byJob["bookmarks-tick"].CompletionTokens != 3000 {
		t.Fatalf("tick rollup = %+v", byJob["bookmarks-tick"])
	}
}

func TestCronRollup_SinceFiltersOldSessions(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(runs.Meta{CronJob: "old-job"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(id, 1000, 100); err != nil {
		t.Fatal(err)
	}

	got, err := st.CronRollup(time.Now().Add(24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rows past a future cutoff, got %+v", got)
	}
}

func TestCronRollup_ReportTurnExcluded(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	childID, err := st.NewSession(runs.Meta{Agent: "syncer", CronJob: "sync"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(childID, 10000, 500); err != nil {
		t.Fatal(err)
	}
	mainID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(mainID, 90000, 4500); err != nil {
		t.Fatal(err)
	}

	got, err := st.CronRollup(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("want exactly 1 job in the rollup (main session must not appear), got %+v", got)
	}
	if got[0].CronJob != "sync" || got[0].PromptTokens != 10000 || got[0].CompletionTokens != 500 {
		t.Fatalf("rollup = %+v, want only the dispatched child's 10000/500 — report-turn cost must not leak in", got[0])
	}
}

func TestAddUsage_AccumulatesAndSurvivesRoundTrip(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := st.AddUsage(id, 100, 10); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.SessionMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalPromptTokens != 300 || got.TotalCompletionTokens != 30 {
		t.Fatalf("usage = %d/%d, want 300/30", got.TotalPromptTokens, got.TotalCompletionTokens)
	}
}

func TestAddUsage_UnknownSessionErrors(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage("nonexistent", 1, 1); err == nil {
		t.Fatal("expected an error for an unknown session id, got nil")
	}
}
