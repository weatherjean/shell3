package shell3

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/paths"
)

// applyHostReminders sets this session's standing reminders to the host-level
// Environment and Delegation context, gated by the active agent's toggles
// (cfg.Environment / cfg.Delegation, default off). Standing reminders are
// injected into every turn's context and shown on the dashboard, but are NOT
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
// current active agent: the allowed-subagents list + the exact templated spawn
// command (with absolute --out/--inbox/--parent-session already substituted),
// wrapped in <system-reminder>…</system-reminder>. Returns "" when there is
// nothing to delegate with: no runtime, no allowed subagents, or no resolvable
// config path.
func (s *Session) delegationReminder(rt *Runtime) string {
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
	section := renderDelegation(delegationParams{
		Binary:        shell3Binary(),
		ConfigPath:    cfgPath,
		WorkDir:       s.cfg.WorkDir,
		RunsDir:       s.cfg.RunsDir,
		ParentSession: s.sess.ID(),
		Subagents:     s.subagentList(rt, allowed),
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

// delegationParams carries the concrete, session-resolved values the delegation
// section templates into its spawn command.
type delegationParams struct {
	Binary        string
	ConfigPath    string
	WorkDir       string
	RunsDir       string // parent's <root>/.shell3_project/runs — derives the absolute --out + --inbox paths
	ParentSession string
	Subagents     []subagentItem
}

// renderDelegation builds the Delegation reminder body: the allowed subagents
// and the EXACT bash_bg command to spawn one, with every runtime value already
// substituted. The caller (delegationReminder) wraps the result in a
// <system-reminder> envelope. The command passes --parent-session <this session> (the
// child records that report pointer and reports back by appending a pointer line
// to the project inbox when it finishes). There is no depth gate — a spawned
// child may itself delegate.
// Returns "" when there are no subagents to list.
func renderDelegation(p delegationParams) string {
	if len(p.Subagents) == 0 {
		return ""
	}
	// Resolve the transcript and the report inbox to ABSOLUTE paths under the
	// parent's runtime root (<root>/.shell3_project), so a spawned subagent —
	// which runs from its own working directory — writes its transcript and its
	// completion pointer where THIS host actually watches, not relative to the
	// child's cwd. RunsDir is <root>/.shell3_project/runs; its grandparent is the
	// root. Falls back to a relative transcript (and no --inbox) when unknown.
	transcript := paths.AgentTranscript("", "<id>")
	inbox := ""
	if p.RunsDir != "" {
		root := filepath.Dir(filepath.Dir(p.RunsDir))
		transcript = paths.AgentTranscript(root, "<id>")
		inbox = paths.NewLocal(root).Inbox
	}
	var b strings.Builder
	b.WriteString("Delegation:\n")
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
	if inbox != "" {
		fmt.Fprintf(&b, "  %s run --config %s --agent <name> --out %s --inbox %s --parent-session %s --id <id> --prompt \"<task>\"\n\n",
			p.Binary, p.ConfigPath, transcript, inbox, p.ParentSession)
	} else {
		fmt.Fprintf(&b, "  %s run --config %s --agent <name> --out %s --parent-session %s --id <id> --prompt \"<task>\"\n\n",
			p.Binary, p.ConfigPath, transcript, p.ParentSession)
	}
	b.WriteString("When it finishes you'll get a notification with the subagent's result summary (act on it directly) plus the transcript path at `" + transcript + "`. The transcript is a JSONL audit log — read it only if the summary isn't enough, and extract the subagent's full final answer cleanly with:\n\n")
	fmt.Fprintf(&b, "    jq -rs 'map(select(.kind==\"assistant_message\"))[-1].text' %s\n", transcript)
	return b.String()
}
