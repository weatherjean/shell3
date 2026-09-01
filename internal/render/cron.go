//go:build unix

package render

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
)

// CronRollupWindow controls both the cron query window and its label.
const CronRollupWindow = 7 * 24 * time.Hour

// cronCost reports dispatched-run tokens only. Report-turn cost cannot be
// attributed when one wake turn handles several jobs, so the "run" qualifier
// is intentional. Missing rollup data is unknown, not zero.
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
