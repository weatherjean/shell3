//go:build unix

package render

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
)

// CronRollupWindow is how far back the dash Cron table's cost column looks —
// the window Store.CronRollup is called with at the render call site, and the
// "7d" label cronCost prints. Kept as one named constant, not two
// numbers in two packages, so the call site and the label can never drift apart.
const CronRollupWindow = 7 * 24 * time.Hour

// cronCost renders "<tok> tok/<N>d run" for a job with cost data, or
// "" when none is available and unknowable (no store wired, the rollup
// errored, or an AGENT job that hasn't run in the window) — a missing cost
// must never look like a zero cost. The day count is derived from
// CronRollupWindow, not hardcoded, so the label can never drift out of sync
// with the window the caller actually queried.
//
// A TOOL job is the one exception: it dispatches no subagent and so creates
// no session, meaning it NEVER has a CronRollup row — that absence is not
// missing data, it is a knowably-zero cost, so it renders "0 tok/Nd run"
// rather than vanishing like a genuinely unknown figure would.
//
// For a job with cost data, the figure is the job's DISPATCHED-RUN spend
// only — Store.CronRollup groups on sessions.cron_job, which is set on the
// dispatched child session but NOT on the main-agent session that later
// reads the task report and answers it (a wake turn can drain reports from
// several jobs plus user backlog in one turn, so there is no honest way to
// split that turn's cost back across the jobs that fed it). That report turn
// is commonly the majority of a job's real cost. Do not drop the "run"
// qualifier or fold report-turn cost into this number without also giving
// the rollup a real per-job split — see TestCronRollup_ReportTurnExcluded in
// internal/runs.
func cronCost(st cron.JobStatus, costs map[string]runs.JobCost) string {
	days := int(CronRollupWindow.Hours() / 24)
	c, ok := costs[st.Name]
	if !ok || c.Runs == 0 {
		return "" // absence is genuinely unknown, not zero
	}
	return fmt.Sprintf("%s tok/%dd run", formatTokCount(c.PromptTokens+c.CompletionTokens), days)
}

// formatTokCount renders a token count compactly (2100000 -> "2.1M") so a
// cron row fits on one phone-width line instead of a bare seven-digit number.
func formatTokCount(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// cronLastRun renders when a job last fired, "never" if it hasn't.
func cronLastRun(st cron.JobStatus) string {
	if st.LastRun == "" {
		return "never"
	}
	t, err := time.Parse(time.RFC3339, st.LastRun)
	if err != nil {
		return "never"
	}
	return stamp(t)
}

// cronOutcome renders one job's outcome plus its running counts. A tool
// job's LastOK/Runs/Failures describe the actual run (ToolRunner's real
// error), so it gets truthful "ok"/"FAIL" wording. An agent job's fields
// describe only whether Dispatch ACCEPTED the subagent — never whether its
// run succeeded — so it is labeled "dispatched"/"dispatch FAILED" with
// "dispatches"/"rejected" counts: distinct words for a distinct, weaker
// guarantee, so a job failing every night can never read as "0 failed".
func cronOutcome(st cron.JobStatus) string {
	if st.LastRun == "" {
		return "never run"
	}
	word := "dispatched"
	if !st.LastOK {
		word = "dispatch FAILED"
	}
	out := fmt.Sprintf("%s — %d dispatches, %d rejected", word, st.Runs, st.Failures)
	if !st.LastOK && st.LastErr != "" {
		out += ": " + st.LastErr
	}
	return out
}
