//go:build unix

package render_test

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
)

// cronRow renders one JobStatus through the dash index and returns the page.
func cronRow(statuses []cron.JobStatus, costs map[string]runs.JobCost) string {
	return render.DashIndexHTML(nil, nil, "", nil, statuses, costs, "")
}

// An agent job's outcome is labeled "dispatched", never "ok": Dispatch only
// reports whether the subagent was ACCEPTED, not whether its run succeeded,
// so claiming success would be a dashboard that lies.
func TestCronAgentJobOutcomeIsLabeledNotClaimedAsSuccess(t *testing.T) {
	out := cronRow([]cron.JobStatus{{
		Name: "digest", Schedule: "0 8 * * *", Agent: "analyst",
		LastRun: "2026-08-01T08:00:00Z", LastOK: true, Runs: 4, Failures: 0,
	}}, nil)
	if !strings.Contains(out, "dispatched") || !strings.Contains(out, "4 dispatches") {
		t.Errorf("agent job should speak in dispatch language:\n%s", out)
	}
	if strings.Contains(out, "ok —") {
		t.Errorf("agent job must not claim \"ok\":\n%s", out)
	}
}

func TestCronAgentJobDispatchRejected(t *testing.T) {
	out := cronRow([]cron.JobStatus{{
		Name: "digest", Schedule: "0 8 * * *", Agent: "analyst",
		LastRun: "2026-08-01T08:00:00Z", LastOK: false, LastErr: "no such agent", Runs: 4, Failures: 2,
	}}, nil)
	for _, want := range []string{"dispatch FAILED", "2 rejected", "no such agent"} {
		if !strings.Contains(out, want) {
			t.Errorf("rejected dispatch missing %q:\n%s", want, out)
		}
	}
}

// A tool job's outcome IS the real run result, so it gets honest ok/FAIL.
func TestCronToolJobOutcome(t *testing.T) {
	out := cronRow([]cron.JobStatus{{
		Name: "sync", Schedule: "*/30 * * * *", Tool: "pull",
		LastRun: "2026-08-01T08:00:00Z", LastOK: false, LastErr: "exit 3", Runs: 9, Failures: 1,
	}}, nil)
	for _, want := range []string{"tool:pull", "FAIL", "9 runs", "1 failed", "exit 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("tool job missing %q:\n%s", want, out)
		}
	}
}

// Cost column: a known figure renders; an agent job with no rollup row shows
// NOTHING (missing must never look like zero); a tool job's absence is a
// knowable zero and renders as one.
func TestCronCostColumn(t *testing.T) {
	agent := cron.JobStatus{Name: "digest", Schedule: "@daily", Agent: "a", LastRun: "2026-08-01T08:00:00Z", LastOK: true, Runs: 1}
	tool := cron.JobStatus{Name: "sync", Schedule: "@hourly", Tool: "pull", LastRun: "2026-08-01T08:00:00Z", LastOK: true, Runs: 1}

	out := cronRow([]cron.JobStatus{agent}, map[string]runs.JobCost{
		"digest": {CronJob: "digest", Runs: 3, PromptTokens: 2_000_000, CompletionTokens: 100_000},
	})
	if !strings.Contains(out, "2.1M tok/7d run") {
		t.Errorf("known agent cost missing:\n%s", out)
	}

	out = cronRow([]cron.JobStatus{agent}, nil)
	if strings.Contains(out, "0 tok") {
		t.Errorf("unknown agent cost rendered as zero:\n%s", out)
	}

	out = cronRow([]cron.JobStatus{tool}, nil)
	if !strings.Contains(out, "0 tok/7d run") {
		t.Errorf("tool job's knowable zero missing:\n%s", out)
	}
}
