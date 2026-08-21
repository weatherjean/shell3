package runs_test

import (
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

// TestCronRollup_SumsByJob pins the grouping this task exists for: "what did
// this cron job cost this week" needs per-job sums across every session that
// job started, not a single session's ledger.
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

// TestCronRollup_SinceFiltersOldSessions: a window narrower than "all time"
// must exclude sessions started before it — otherwise "cost this week" would
// silently report "cost ever".
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

	got, err := st.CronRollup(time.Now().Add(24 * time.Hour)) // future cutoff: nothing qualifies
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no rows past a future cutoff, got %+v", got)
	}
}

// TestCronRollup_ReportTurnExcluded pins the deliberate, documented
// undercount described on cronCostSuffix (internal/render/cron.go): the
// main-agent session that later reads a cron job's task report and answers
// it runs with cron_job="" (it is the main conversation, not the dispatched
// child), so its usage must NOT be attributed to the job that triggered it —
// a wake turn can drain reports from several jobs plus user backlog in one
// turn, so there is no honest per-job split of that turn's cost. This is the
// negative half of TestCronRollup_SumsByJob: the dispatched child's spend
// counts, the report-reading turn's spend must not.
func TestCronRollup_ReportTurnExcluded(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The dispatched child session: this IS the job's run, cron_job set.
	childID, err := st.NewSession(runs.Meta{Agent: "syncer", CronJob: "sync"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(childID, 10000, 500); err != nil {
		t.Fatal(err)
	}
	// The main conversation session that later reads the task report and
	// replies — cron_job is '' because this is not a dispatched run.
	mainID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage(mainID, 90000, 4500); err != nil { // the "62% cost" report turn
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

// TestAddUsage_AccumulatesAndSurvivesRoundTrip proves AddUsage sums across
// multiple calls (not last-write-wins) and that the total is read back
// correctly through SessionMeta — the store-level half of the accumulation
// guarantee chat.saveHistory depends on.
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

// TestAddUsage_UnknownSessionErrors: a cost that silently lands nowhere is
// worse than a loud failure naming the id — see AddUsage's doc comment.
func TestAddUsage_UnknownSessionErrors(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddUsage("nonexistent", 1, 1); err == nil {
		t.Fatal("expected an error for an unknown session id, got nil")
	}
}
