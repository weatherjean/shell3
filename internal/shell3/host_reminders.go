package shell3

// applyHostReminders refreshes non-persisted, session-specific environment facts.
func (s *Session) applyHostReminders() {
	var rems []string
	if env := s.envReminder(); env != "" {
		rems = append(rems, env)
	}
	s.sess.SetStandingReminders(rems)
}

// envReminder renders the host Environment standing reminder supplied by the
// configured runtime. A runtime that has no environment facts may omit it.
func (s *Session) envReminder() string {
	if s.cfg.RenderEnvironment != nil {
		return s.cfg.RenderEnvironment(s.sess.ID())
	}
	return ""
}
