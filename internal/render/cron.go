package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// Cron renders the declared cron jobs and, where known, when each last ran.
// lastRuns is keyed by job name; a missing entry reads as "never".
func Cron(jobs []shell3.CronJob, lastRuns map[string]time.Time) string {
	var b strings.Builder
	b.WriteString("# Cron\n\n")
	if len(jobs) == 0 {
		b.WriteString("_No cron jobs._\n")
		return b.String()
	}
	for _, j := range jobs {
		fmt.Fprintf(&b, "## %s\n\n", j.Name)
		field(&b, "schedule", "`"+j.Schedule+"`")
		field(&b, "agent", j.Agent)
		field(&b, "workdir", j.WorkDir)
		delivery := "mail (quiet agent turn)"
		if j.Direct {
			delivery = "direct (raw post, no agent turn)"
		}
		field(&b, "delivery", delivery)
		last := "never"
		if t, ok := lastRuns[j.Name]; ok && !t.IsZero() {
			last = stamp(t)
		}
		field(&b, "last run", last)
		b.WriteString("\n")
		if strings.TrimSpace(j.Prompt) != "" {
			fence(&b, "", j.Prompt)
		}
	}
	return b.String()
}

// CronBrief is the /status line-per-job form. The full Cron view prints each
// job's prompt body, which is right when you asked about cron specifically and
// wrong in a view you check at a glance.
func CronBrief(jobs []shell3.CronJob, lastRuns map[string]time.Time) string {
	var b strings.Builder
	b.WriteString("## Cron\n\n")
	for _, j := range jobs {
		last := "never"
		if t, ok := lastRuns[j.Name]; ok && !t.IsZero() {
			last = stamp(t)
		}
		fmt.Fprintf(&b, "- `%s` %s → %s _(last: %s)_\n", j.Name, j.Schedule, j.Agent, last)
	}
	return b.String()
}
