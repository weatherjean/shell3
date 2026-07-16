package shell3

import (
	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// applyHostReminders sets this session's standing reminders to the host-level
// Environment context, gated by the active agent's toggle (cfg.Environment,
// default off). Standing reminders are injected into every turn's context and
// visible via Snapshot, but are NOT persisted — they are re-assembled fresh at
// every prompt-assembly event (session create, agent switch, config reload,
// /clear).
//
// These are host-injected facts that depend on SESSION-level values the system
// prompt can't carry — the resolved config path, this session's id, and the
// active model — so they live as standing reminders rather than in the
// Lua-authored system prompt. SetStandingReminders replaces the set wholesale,
// so this is naturally idempotent across re-runs (no prompt-splicing / strip
// step is needed).
//
// Delegation needs no reminder: the allowed-subagents list (names +
// descriptions) is baked into the `task` tool's schema (luacfg.TaskToolFor),
// so the tool advertises itself.
func (s *Session) applyHostReminders() {
	var rems []string
	if s.cfg.Environment {
		if env := s.envReminder(); env != "" {
			rems = append(rems, env)
		}
	}
	s.sess.SetStandingReminders(rems)
}

// envReminder renders the host Environment standing reminder from this session's
// config (config path, runs dir, model from the status line) plus the runs
// session id. The fact wording lives in agentsetup.EnvironmentReminder so it
// stays in one place. Returns "" when no runs dir is resolvable.
func (s *Session) envReminder() string {
	_, model := chat.SplitStatus(s.cfg.StatusLine)
	return agentsetup.EnvironmentReminder(s.cfg.ConfigPath, s.cfg.RunsDir, model, s.sess.ID())
}
