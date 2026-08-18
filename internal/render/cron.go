//go:build unix

package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/runs"
)

// CronRollupWindow is how far back CronBrief's cost column looks — the
// window Store.CronRollup is called with at the render call site, and the
// "7d" label CronBrief prints. Kept as one named constant, not two numbers in
// two packages, so the call site and the label can never drift apart.
const CronRollupWindow = 7 * 24 * time.Hour

// Cron renders the declared cron jobs together with their run history.
// statuses carries everything a row needs (schedule/agent/tool/prompt from
// config, run counts and last outcome from the scheduler) — see
// cron.JobStatus's own doc comment for why an agent job's outcome is labeled
// "dispatched"/"rejected" rather than "ok"/"failed": Dispatch only reports
// whether the subagent was ACCEPTED, never whether its actual run succeeded,
// so claiming "ok" or a trustworthy failure count for an agent job would be a
// dashboard that lies. A tool job's outcome IS the real result (ToolRunner
// reports it directly), so it gets the honest ok/FAIL/runs/failed language.
func Cron(statuses []cron.JobStatus) string {
	var b strings.Builder
	b.WriteString("# Cron\n\n")
	if len(statuses) == 0 {
		b.WriteString("_No cron jobs._\n")
		return b.String()
	}
	for _, st := range statuses {
		fmt.Fprintf(&b, "## %s\n\n", st.Name)
		field(&b, "schedule", "`"+st.Schedule+"`")
		if st.Tool != "" {
			field(&b, "tool", st.Tool)
		} else {
			field(&b, "agent", st.Agent)
		}
		field(&b, "workdir", st.WorkDir)
		delivery := "mail (quiet agent turn)"
		switch {
		case st.Tool != "":
			// No agent, no dispatch — the job posts its own result (or
			// stays silent), so neither "mail" nor "direct" describes it.
			delivery = "tool call (posts its own result, no agent turn)"
		case st.Direct:
			delivery = "direct (raw post, no agent turn)"
		}
		field(&b, "delivery", delivery)
		field(&b, "last run", cronLastRun(st))
		field(&b, "outcome", cronOutcome(st))
		b.WriteString("\n")
		if strings.TrimSpace(st.Prompt) != "" {
			fence(&b, "", st.Prompt)
		}
	}
	return b.String()
}

// CronBrief is the /status line-per-job form. The full Cron view prints each
// job's prompt body, which is right when you asked about cron specifically and
// wrong in a view you check at a glance. costs is keyed by cron job name (nil
// or a missing entry simply omits that job's cost segment — a store error or
// a job that hasn't run in the window is not a reason to hide the rest of the
// row); this is the surface that made a silently idempotent job's spend
// findable, so a phone-glance dashboard shows it, not just the full /cron view
// nothing links to in production.
func CronBrief(statuses []cron.JobStatus, costs map[string]runs.JobCost) string {
	var b strings.Builder
	b.WriteString("## Cron\n\n")
	for _, st := range statuses {
		target := st.Agent
		if st.Tool != "" {
			target = "tool:" + st.Tool
		}
		fmt.Fprintf(&b, "- `%s` %s → %s _(last: %s — %s)_%s\n",
			st.Name, st.Schedule, target, cronLastRun(st), cronOutcome(st), cronCostSuffix(st, costs))
	}
	return b.String()
}

// cronCostSuffix renders " · <tok> tok/<N>d run" for a job with cost data, or
// "" when none is available and unknowable (no store wired, the rollup
// errored, or an AGENT job that hasn't run in the window) — a missing cost
// must never look like a zero cost. The day count is derived from
// CronRollupWindow, not hardcoded, so the label can never drift out of sync
// with the window the caller actually queried.
//
// A TOOL job is the one exception: it dispatches no subagent and so creates
// no session, meaning it NEVER has a CronRollup row — that absence is not
// missing data, it is a knowably-zero cost, so it renders " · 0 tok/Nd run"
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
func cronCostSuffix(st cron.JobStatus, costs map[string]runs.JobCost) string {
	days := int(CronRollupWindow.Hours() / 24)
	c, ok := costs[st.Name]
	if !ok || c.Runs == 0 {
		if st.Tool == "" {
			return "" // agent job: absence is genuinely unknown, not zero
		}
		return fmt.Sprintf(" · 0 tok/%dd run", days) // tool job: knowably zero
	}
	return fmt.Sprintf(" · %s tok/%dd run", formatTokCount(c.PromptTokens+c.CompletionTokens), days)
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
	if st.Tool != "" {
		word := "ok"
		if !st.LastOK {
			word = "FAIL"
		}
		out := fmt.Sprintf("%s — %d runs, %d failed", word, st.Runs, st.Failures)
		if !st.LastOK && st.LastErr != "" {
			out += ": " + st.LastErr
		}
		return out
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
