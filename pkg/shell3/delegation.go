package shell3

import (
	"fmt"
	"path/filepath"
	"strings"
)

// applyDelegationContext appends a "## Delegation" section to this session's
// system prompt when the active agent has ≥1 allowed subagent and a resolvable
// config path. It is the per-session counterpart to agentsetup's "##
// Environment" section: agentsetup renders config-derived facts, but the
// delegation command needs SESSION-level values agentsetup can't see — the
// resolved config path, the running shell3 binary, and this session's id (the
// child's --parent-session report pointer) — so it is injected here, once, at
// session construction.
//
// The result survives across turns (it lives in the system prompt, not a
// per-turn reminder, so it never bloats later turns). There is no depth gate:
// multi-level delegation is supported. A spawned child records its
// --parent-session pointer and reports its result back automatically over the
// socket/inbox transport when it finishes, so a child that itself has subagents
// gets a delegation context too.
//
// delegationMarker opens the appended section. stripDelegation removes a
// previously-appended section so applyDelegationContext is idempotent and safe
// to re-run after an agent switch or a config reload (both rebuild the system
// prompt from the active agent and would otherwise drop, or duplicate, the
// section).
const delegationMarker = "\n## Delegation\n"

// rt is the owning runtime, captured by the caller (Runtime.Session) before any
// concurrent Close can nil s.runtime; it supplies the config path and the
// subagent description lookup. Re-runnable: it first strips any prior Delegation
// section, then re-appends one for the CURRENT active agent (whose allowed
// subagents may differ after a /agent switch).
func (s *Session) applyDelegationContext(rt *Runtime) {
	// Compute the new prompt unlocked (no concurrent cfg writer — callers are
	// between-turns on the single bot goroutine), then publish it under s.mu so
	// the dashboard's Snapshot reader never observes a torn assignment.
	prompt := stripDelegation(s.cfg.Personality.SystemPrompt) + s.delegationSection(rt)
	s.mu.Lock()
	s.cfg.Personality.SystemPrompt = prompt
	s.mu.Unlock()
}

// delegationSection renders the "## Delegation" section to append for the
// current active agent, or "" when there is nothing to delegate with: no
// runtime, no allowed subagents, or no resolvable config path. See
// applyDelegationContext for why this lives per-session.
func (s *Session) delegationSection(rt *Runtime) string {
	if rt == nil {
		return ""
	}
	allowed := s.cfg.Subagents // the active agent's tools.subagents allowlist
	if len(allowed) == 0 {
		return ""
	}
	cfgPath, err := rt.ConfigPath()
	if err != nil || cfgPath == "" {
		return "" // can't template a spawn command without a concrete config path
	}
	return renderDelegation(delegationParams{
		Binary:        shell3Binary(),
		ConfigPath:    cfgPath,
		WorkDir:       s.cfg.WorkDir,
		ParentSession: s.sess.ID(),
		Subagents:     s.subagentList(rt, allowed),
	})
}

// stripDelegation removes a previously-appended "## Delegation" section (from
// its marker to end-of-prompt), leaving the rest of the prompt intact. The
// section is always appended last, so everything from the marker on is ours.
func stripDelegation(prompt string) string {
	if i := strings.Index(prompt, delegationMarker); i >= 0 {
		return prompt[:i]
	}
	return prompt
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

// delegationParams carries the concrete, session-resolved values the delegation
// section templates into its spawn command.
type delegationParams struct {
	Binary        string
	ConfigPath    string
	WorkDir       string
	ParentSession int64
	Subagents     []subagentItem
}

// renderDelegation builds the "## Delegation" system-prompt section: the allowed
// subagents and the EXACT bash_bg command to spawn one, with every runtime value
// already substituted. The command sets notify_on_exit=false (the child
// self-reports its result, so a duplicate bg_done is suppressed) and
// --parent-session <this session> (the child records that report pointer and
// reports back to THIS session over the socket/inbox transport when it
// finishes). There is no depth gate — a spawned child may itself delegate.
// Returns "" when there are no subagents to list.
func renderDelegation(p delegationParams) string {
	if len(p.Subagents) == 0 {
		return ""
	}
	transcript := filepath.Join(".shell3", "agents", "<id>.jsonl")
	var b strings.Builder
	b.WriteString(delegationMarker)
	b.WriteString("You can delegate a focused, self-contained subtask to a subagent — a background `shell3` process that runs the chosen agent on the task and reports back to you automatically when it finishes. You do NOT poll; a notification arrives on its own, and it already carries the subagent's result summary — act on that directly. A transcript path comes with it for the rare case you need more.\n\n")
	b.WriteString("Subagents you may spawn:\n")
	for _, sa := range p.Subagents {
		if sa.Description != "" {
			fmt.Fprintf(&b, "- %s: %s\n", sa.Name, sa.Description)
		} else {
			fmt.Fprintf(&b, "- %s\n", sa.Name)
		}
	}
	b.WriteString("\nTo spawn one, call the `bash_bg` tool with this exact command, substituting `<name>` (a subagent from the list), `<id>` (a short unique id you choose, e.g. `explore1`), and `<task>` (the full self-contained prompt — the subagent does not see this conversation):\n\n")
	fmt.Fprintf(&b, "  %s run --config %s --agent <name> --out %s --parent-session %d --id <id> --prompt \"<task>\"\n\n",
		p.Binary, p.ConfigPath, transcript, p.ParentSession)
	b.WriteString("Pass notify_on_exit=false to bash_bg (the subagent reports its own completion, so the generic background-job notice is redundant). When it finishes you'll get a notification with the subagent's result summary (act on it directly) plus the transcript path at `.shell3/agents/<id>.jsonl`. The transcript is a JSONL audit log — read it only if the summary isn't enough, and extract the subagent's full final answer cleanly with:\n\n")
	fmt.Fprintf(&b, "    jq -rs 'map(select(.kind==\"assistant_message\"))[-1].text' %s\n", transcript)
	return b.String()
}
