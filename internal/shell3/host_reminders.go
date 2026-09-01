package shell3

import (
	"github.com/weatherjean/shell3/internal/agentsetup"
)

// applyHostReminders refreshes non-persisted, session-specific environment facts.
func (s *Session) applyHostReminders() {
	var rems []string
	if env := s.envReminder(); env != "" {
		rems = append(rems, env)
	}
	s.sess.SetStandingReminders(rems)
}

// envReminder renders the host Environment standing reminder from this session's
// config (config path, runs dir, model) plus the runs
// session id. The fact wording lives in agentsetup.EnvironmentReminder so it
// stays in one place. Returns "" when no runs dir is resolvable.
func (s *Session) envReminder() string {
	return agentsetup.EnvironmentReminder(s.cfg.ConfigDir, s.cfg.RunsDir, s.cfg.ModelID, s.sess.ID())
}
