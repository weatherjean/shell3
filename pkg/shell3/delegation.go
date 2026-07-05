package shell3

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// applyHostReminders sets this session's standing reminders to the host-level
// Environment and Delegation context, gated by the active agent's toggles
// (cfg.Environment / cfg.Delegation, default off). Standing reminders are
// injected into every turn's context and visible via Snapshot, but are NOT
// persisted — they are re-assembled fresh at every prompt-assembly event
// (session create, agent switch, config reload, /clear).
//
// These are host-injected facts that depend on SESSION-level values the system
// prompt can't carry — the resolved config path, the running shell3 binary,
// this session's id, and the active model — so they live as standing reminders
// rather than in the Lua-authored system prompt. SetStandingReminders replaces
// the set wholesale, so this is naturally idempotent across re-runs (no
// prompt-splicing / strip step is needed).
//
// rt is the owning runtime, captured by the caller (Runtime.Session) before any
// concurrent Close can nil s.runtime; it supplies the config path and the
// subagent description lookup.
func (s *Session) applyHostReminders(rt *Runtime) {
	var rems []string
	if s.cfg.Environment {
		if env := s.envReminder(); env != "" {
			rems = append(rems, env)
		}
	}
	if s.cfg.Delegation {
		if d := s.delegationReminder(rt); d != "" {
			rems = append(rems, d)
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

// delegationReminder renders the host Delegation standing reminder for the
// current active agent: the allowed-subagents list + task-tool guidance,
// wrapped in <system-reminder>…</system-reminder>. Returns "" when there is
// nothing to delegate with: no runtime or no allowed subagents.
func (s *Session) delegationReminder(rt *Runtime) string {
	if rt == nil {
		return ""
	}
	allowed := s.cfg.Subagents // the active agent's tools.subagents allowlist
	if len(allowed) == 0 {
		return ""
	}
	section := renderDelegation(delegationParams{
		Subagents: s.subagentList(rt, allowed),
	})
	if section == "" {
		return ""
	}
	return "<system-reminder>\n" + section + "\n</system-reminder>"
}

// subagentItem is one allowed subagent, name + model-facing description.
type subagentItem struct{ Name, Description string }

// subagentList resolves the active agent's allowed subagent names to
// name/description pairs via the runtime's subagentDesc lookup. A name with no
// resolvable description still appears (description "") so the agent at least
// knows it may delegate to it; load-time validation guarantees the names exist.
func (s *Session) subagentList(rt *Runtime, names []string) []subagentItem {
	out := make([]subagentItem, 0, len(names))
	for _, n := range names {
		desc := ""
		if rt.subagentDesc != nil {
			if d, ok := rt.subagentDesc(n); ok {
				desc = d
			}
		}
		out = append(out, subagentItem{Name: n, Description: desc})
	}
	return out
}

// delegationParams carries the concrete, session-resolved values the
// delegation section renders.
type delegationParams struct {
	Subagents []subagentItem
}

// renderDelegation builds the Delegation reminder body: the allowed subagents
// and guidance to spawn one via the `task` tool. The caller (delegationReminder)
// wraps the result in a <system-reminder> envelope.
// Returns "" when there are no subagents to list.
func renderDelegation(p delegationParams) string {
	if len(p.Subagents) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Delegation: to run work in the background, call the `task` tool with\n")
	b.WriteString("{subagent_type, prompt, description}. It returns immediately and you'll be\n")
	b.WriteString("notified when the subagent finishes — keep working, don't poll.\n\n")
	b.WriteString("Subagents you may spawn:\n")
	for _, sa := range p.Subagents {
		if sa.Description != "" {
			fmt.Fprintf(&b, "- %s: %s\n", sa.Name, sa.Description)
		} else {
			fmt.Fprintf(&b, "- %s\n", sa.Name)
		}
	}
	b.WriteString("\nTask management tools (use after spawning):\n")
	b.WriteString("- task_list: see all running/finished tasks (ids look like sub1, bg1)\n")
	b.WriteString("- task_status {id}: check one task's status and result\n")
	b.WriteString("- task_cancel {id}: stop a running task\n")
	return b.String()
}
