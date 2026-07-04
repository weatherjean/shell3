package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// hostReminderText concatenates a session's standing+logged reminder texts so a
// test can assert over the whole set the dashboard/turn loop would inject.
func hostReminderText(s *Session) string {
	var b strings.Builder
	for _, r := range s.sess.Reminders() {
		b.WriteString(r.Text)
		b.WriteString("\n")
	}
	return b.String()
}

// findReminder returns the first standing reminder containing sub, or "".
func findReminder(s *Session, sub string) string {
	for _, r := range s.sess.Reminders() {
		if strings.Contains(r.Text, sub) {
			return r.Text
		}
	}
	return ""
}

// hostRemindersCfg builds a fake chat.Config for an agent with the given
// host-reminder toggles and (optionally) a subagent allowlist, plus the
// session-level facts the Environment reminder reads (config path, runs dir,
// status line). SwitchAgent flips to a Delegation=false agent named "plain".
func hostRemindersCfg(env, deleg bool, subagents []string) func() chat.Config {
	return func() chat.Config {
		cfg := chat.Config{
			LLM:        fakellm.New(fakellm.Script{}),
			ModeLabel:  "code",
			StatusLine: "openai │ gpt-x",
			ConfigPath: "/cfg/shell3.lua",
			RunsDir:    "/root/.shell3_project/runs",
			AgentKnobs: chat.AgentKnobs{Environment: env, Delegation: deleg, Subagents: subagents},
			AgentNames: []string{"code", "plain"},
		}
		cfg.SwitchAgent = func(name string) (chat.ActiveAgent, error) {
			// "plain" has delegation off and no subagents.
			return chat.ActiveAgent{
				ModeLabel: name, LLM: fakellm.New(fakellm.Script{}),
				ModelID:    "gpt-x",
				AgentKnobs: chat.AgentKnobs{Environment: false, Delegation: false},
			}, nil
		}
		return cfg
	}
}

// newHostRemindersRuntime wires the runtime fields applyHostReminders depends on
// that newTestRuntime leaves zero: a resolvable config path and a subagent
// description lookup.
func newHostRemindersRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	rt := newTestRuntime(t, mk)
	rt.configPath = "/cfg/shell3.lua" // ExpandConfigName returns *.lua verbatim
	rt.subagentDesc = func(name string) (string, bool) { return "does " + name, true }
	return rt
}

// TestHostReminders_BothToggles: with Environment=true, Delegation=true and a
// subagent, a fresh session carries an Environment standing reminder (mentions
// the session id) AND a Delegation standing reminder (the absolute --inbox /
// --parent-session spawn command), while the system prompt carries NEITHER
// host section.
func TestHostReminders_BothToggles(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(true, true, []string{"explore"}))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	// Give the runs session an id (the store does this in real runs) and
	// re-assemble so the Environment reminder can advertise the session id.
	s.sess.SetID("sess-42")
	s.applyHostReminders(rt)

	env := findReminder(s, "session id")
	if env == "" {
		t.Fatalf("expected an Environment standing reminder mentioning the session id:\n%s", hostReminderText(s))
	}
	if !strings.HasPrefix(env, "<system-reminder>") {
		t.Errorf("Environment reminder not wrapped in <system-reminder>:\n%s", env)
	}

	deleg := findReminder(s, "Subagents you may spawn")
	if deleg == "" {
		t.Fatalf("expected a Delegation standing reminder:\n%s", hostReminderText(s))
	}
	if !strings.Contains(deleg, "`task`") {
		t.Errorf("Delegation reminder missing task tool guidance:\n%s", deleg)
	}
	if !strings.HasPrefix(deleg, "<system-reminder>") {
		t.Errorf("Delegation reminder not wrapped in <system-reminder>:\n%s", deleg)
	}

	prompt := s.cfg.Personality.SystemPrompt
	if strings.Contains(prompt, "## Environment") || strings.Contains(prompt, "## Delegation") {
		t.Errorf("system prompt must contain NEITHER host section:\n%s", prompt)
	}

	// The prompt-inspection view (TUI /prompt + dashboard Status → Prompt) reads
	// Snapshot().SystemPrompt, which folds in the standing reminders so the user
	// sees the full effective context even though the authored prompt stays clean.
	shown := s.Snapshot().SystemPrompt
	if !strings.Contains(shown, "Host reminders") {
		t.Errorf("Snapshot prompt missing the Host reminders section:\n%s", shown)
	}
	if !strings.Contains(shown, "session id") || !strings.Contains(shown, "Subagents you may spawn") {
		t.Errorf("Snapshot prompt must surface both standing reminders:\n%s", shown)
	}
}

// TestHostReminders_BothFalse: toggles off → no standing reminders at all.
func TestHostReminders_BothFalse(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(false, false, []string{"explore"}))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := hostReminderText(s); strings.TrimSpace(got) != "" {
		t.Errorf("expected no standing reminders with toggles off, got:\n%s", got)
	}
}

// TestHostReminders_DelegationTrueNoSubagents: delegation on but the agent lists
// no subagents → the Delegation reminder is omitted.
func TestHostReminders_DelegationTrueNoSubagents(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(false, true, nil))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if got := findReminder(s, "Subagents you may spawn"); got != "" {
		t.Errorf("expected no Delegation reminder without subagents, got:\n%s", got)
	}
}

// TestHostReminders_SwitchDropsDelegation: switching to a Delegation=false agent
// removes the Delegation standing reminder.
func TestHostReminders_SwitchDropsDelegation(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(true, true, []string{"explore"}))
	s, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if findReminder(s, "Subagents you may spawn") == "" {
		t.Fatal("precondition: expected a Delegation reminder before the switch")
	}
	if err := s.SwitchAgent("plain"); err != nil {
		t.Fatal(err)
	}
	if got := findReminder(s, "Subagents you may spawn"); got != "" {
		t.Errorf("Delegation reminder should be gone after switching to a Delegation=false agent, got:\n%s", got)
	}
}
