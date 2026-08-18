//go:build unix

package render_test

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestCron(t *testing.T) {
	last := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	statuses := []cron.JobStatus{
		{
			Name: "digest", Schedule: "0 8 * * *", Agent: "explorer",
			Prompt: "summarise yesterday's commits", WorkDir: "/tmp/repo", Direct: true,
			LastRun: last.Format(time.RFC3339), LastOK: true, Runs: 3,
		},
		{Name: "backup", Schedule: "@daily", Agent: "ops", Prompt: "run the backup"},
	}
	out := render.Cron(statuses)
	for _, want := range []string{
		"digest", "0 8 * * *", "explorer", "/tmp/repo",
		"summarise yesterday's commits", "backup", "@daily", "2026-07-28",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Cron missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "direct") {
		t.Errorf("Cron missing the delivery mode:\n%s", out)
	}
}

func TestCronEmpty(t *testing.T) {
	if out := render.Cron(nil); !strings.Contains(strings.ToLower(out), "no cron jobs") {
		t.Errorf("expected an empty-state line:\n%s", out)
	}
}

// A tool: job names no agent, so the view must show the tool it runs instead
// — never a blank "agent:" line — and a delivery mode that doesn't claim an
// agent turn happens (none does). Its outcome is a real ok/FAIL: ToolRunner
// reports the actual result.
func TestCronToolJob(t *testing.T) {
	statuses := []cron.JobStatus{
		{
			Name: "sync", Schedule: "@every 30m", Tool: "sync-notion-recent",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: true, Runs: 812, Failures: 3,
		},
	}
	out := render.Cron(statuses)
	for _, want := range []string{"sync", "@every 30m", "sync-notion-recent", "tool call", "ok", "812 runs, 3 failed"} {
		if !strings.Contains(out, want) {
			t.Errorf("Cron missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "agent turn") && !strings.Contains(out, "no agent turn") {
		t.Errorf("a tool job's delivery must not claim an agent turn happens:\n%s", out)
	}

	brief := render.CronBrief(statuses, nil)
	if !strings.Contains(brief, "tool:sync-notion-recent") {
		t.Errorf("CronBrief missing the tool target:\n%s", brief)
	}
	if !strings.Contains(brief, "812 runs, 3 failed") {
		t.Errorf("CronBrief missing the run tally:\n%s", brief)
	}
}

// A failing tool job must show FAIL and its error, never look identical to a
// healthy one — the whole point of this task.
func TestCronToolJobFailure(t *testing.T) {
	statuses := []cron.JobStatus{
		{
			Name: "broken-job", Schedule: "@every 1h", Tool: "notion-poll",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: false, LastErr: "notion 502",
			Runs: 47, Failures: 47,
		},
	}
	out := render.Cron(statuses)
	for _, want := range []string{"FAIL", "47 runs, 47 failed", "notion 502"} {
		if !strings.Contains(out, want) {
			t.Errorf("Cron missing %q\n---\n%s", want, out)
		}
	}
}

// A never-run job (never fired since this scheduler started, or a brand new
// job) renders "never", not a bogus timestamp or a false "0 failed".
func TestCronNeverRun(t *testing.T) {
	statuses := []cron.JobStatus{{Name: "fresh", Schedule: "@every 1h", Tool: "t"}}
	out := render.Cron(statuses)
	if !strings.Contains(out, "never") {
		t.Errorf("Cron missing 'never' for an unrun job:\n%s", out)
	}
	brief := render.CronBrief(statuses, nil)
	if !strings.Contains(brief, "never") {
		t.Errorf("CronBrief missing 'never' for an unrun job:\n%s", brief)
	}
}

// CONTROLLER SCOPE RULING (task 5 brief): an agent job's LastOK/Failures only
// ever reflect whether Dispatch ACCEPTED the subagent, never whether its run
// actually succeeded. A job dispatching cleanly every night while failing
// its real work every night must never render as "ok" or "0 failed" — that
// would be a dashboard that lies. This pins the negative: the rendered text
// for an agent job must use the weaker, honestly-labeled words, and must not
// contain the tool-job vocabulary that would read as a real outcome.
func TestCronAgentJobOutcomeIsLabeledNotClaimedAsSuccess(t *testing.T) {
	statuses := []cron.JobStatus{
		{
			Name: "ampd-tick", Schedule: "@every 3h", Agent: "ampd-leads",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: true, Runs: 118, Failures: 0,
		},
	}
	out := render.Cron(statuses)
	if !strings.Contains(out, "dispatched") {
		t.Errorf("agent job outcome must say 'dispatched', not an unqualified 'ok':\n%s", out)
	}
	if strings.Contains(out, "118 runs, 0 failed") {
		t.Errorf("agent job must not render the tool-job 'runs/failed' wording — Failures there is a dispatch-rejection count, not a real failure count:\n%s", out)
	}
	if !strings.Contains(out, "118 dispatches, 0 rejected") {
		t.Errorf("agent job must render its count under the honestly-labeled 'dispatches/rejected' wording:\n%s", out)
	}

	brief := render.CronBrief(statuses, nil)
	if strings.Contains(brief, "0 failed") {
		t.Errorf("CronBrief must not claim '0 failed' for an agent job — that count is not observed:\n%s", brief)
	}
}

// The rejected-dispatch case gets its own honestly-labeled failure word too
// ("dispatch FAILED", not "FAIL") so it never reads as a tool job's real
// outcome.
func TestCronAgentJobDispatchRejected(t *testing.T) {
	statuses := []cron.JobStatus{
		{
			Name: "flaky", Schedule: "@every 1h", Agent: "explorer",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: false, LastErr: "job queue full",
			Runs: 10, Failures: 2,
		},
	}
	out := render.Cron(statuses)
	if !strings.Contains(out, "dispatch FAILED") {
		t.Errorf("a rejected dispatch must say 'dispatch FAILED':\n%s", out)
	}
	if strings.Contains(out, "2 failed") {
		t.Errorf("must not use the tool job's 'failed' wording for a dispatch rejection:\n%s", out)
	}
	if !strings.Contains(out, "2 rejected") {
		t.Errorf("must render the rejection count under 'rejected':\n%s", out)
	}
}

// TestCronBrief_ShowsCostForKnownJob is the finding that started this task
// made visible: a job's 7-day token spend must appear right on the /status
// glance view, not only in a hand-written SQL query. Asserts the actual
// rendered text, not the JobCost struct.
func TestCronBrief_ShowsCostForKnownJob(t *testing.T) {
	statuses := []cron.JobStatus{
		{
			Name: "sync", Schedule: "@every 30m", Tool: "sync-notion-recent",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: true, Runs: 812, Failures: 3,
		},
	}
	costs := map[string]runs.JobCost{
		"sync": {CronJob: "sync", Runs: 812, PromptTokens: 14_300_000, CompletionTokens: 400_000},
	}
	brief := render.CronBrief(statuses, costs)
	// "run" qualifies the figure as the dispatched-run spend only, excluding
	// the main-agent report turn that reads the result (see cronCostSuffix's
	// doc comment) — asserted here so the label can't quietly drop back to an
	// unqualified, overclaiming total.
	if !strings.Contains(brief, "14.7M tok/7d run") {
		t.Fatalf("CronBrief missing the rendered, run-qualified cost column:\n%s", brief)
	}
}

// TestCronBrief_OmitsCostWhenUnavailable: an AGENT job with no rollup entry
// (no store wired, the rollup errored, or it hasn't run this window) must not
// render a bogus "0 tok" — that would misreport a genuinely unknown figure as
// literally free. (A tool job is different — see
// TestCronBrief_ToolJobRendersKnownZeroCost — because it never dispatches a
// session at all, so its absence from the rollup IS a known zero.)
func TestCronBrief_OmitsCostWhenUnavailable(t *testing.T) {
	statuses := []cron.JobStatus{
		{Name: "fresh", Schedule: "@every 1h", Agent: "explorer"},
	}
	brief := render.CronBrief(statuses, nil)
	if strings.Contains(brief, "tok/7d") {
		t.Fatalf("CronBrief must omit the cost segment when no rollup data exists:\n%s", brief)
	}
}

// TestCronBrief_ToolJobRendersKnownZeroCost: a tool job dispatches no
// subagent and so never creates a session — it can NEVER have a CronRollup
// row, so its absence is not missing data, it is a knowably-zero cost.
// Rendering it as absent (like the unknown-cost case above) would be the
// inverse of "missing must never look like zero": here, zero must not look
// like missing.
func TestCronBrief_ToolJobRendersKnownZeroCost(t *testing.T) {
	statuses := []cron.JobStatus{
		{Name: "sync", Schedule: "@every 30m", Tool: "sync-notion-recent",
			LastRun: time.Now().UTC().Format(time.RFC3339), LastOK: true, Runs: 812},
	}
	brief := render.CronBrief(statuses, nil)
	if !strings.Contains(brief, "0 tok/7d run") {
		t.Fatalf("CronBrief must render a tool job's knowably-zero cost, not omit it:\n%s", brief)
	}
}
