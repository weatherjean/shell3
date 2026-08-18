package runs

import (
	"fmt"
	"time"
)

// JobCost is one cron job's spend over a window — the answer to "what did
// this job cost me this week", which sessions.last_prompt_tokens alone
// cannot give (it is a point-in-time context gauge, not a running total).
type JobCost struct {
	CronJob          string `json:"cron_job"`
	Runs             int    `json:"runs"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
}

// CronRollup totals token spend per cron job for sessions started at or after
// since (the zero time matches every session — encTime(zero) is "", and every
// stored started_at string is >= "" — so a caller wanting all-time totals
// passes time.Time{} rather than a separate "no filter" path).
func (s *Store) CronRollup(since time.Time) ([]JobCost, error) {
	rows, err := s.db.Query(`SELECT cron_job, COUNT(*),
		COALESCE(SUM(total_prompt_tokens),0), COALESCE(SUM(total_completion_tokens),0)
		FROM sessions WHERE cron_job <> '' AND started_at >= ?
		GROUP BY cron_job ORDER BY SUM(total_prompt_tokens) DESC`, encTime(since.UTC()))
	if err != nil {
		return nil, fmt.Errorf("runs: cron rollup: %w", err)
	}
	defer rows.Close()
	var out []JobCost
	for rows.Next() {
		var c JobCost
		if err := rows.Scan(&c.CronJob, &c.Runs, &c.PromptTokens, &c.CompletionTokens); err != nil {
			return nil, fmt.Errorf("runs: cron rollup scan: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
