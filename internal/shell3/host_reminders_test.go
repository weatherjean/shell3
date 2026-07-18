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
// Environment toggle plus the session-level facts the Environment reminder
// reads (config path, runs dir, status line).
func hostRemindersCfg(env bool) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        fakellm.New(fakellm.Script{}),
			ModeLabel:  "code",
			StatusLine: "openai │ gpt-x",
			ConfigDir:  "/cfg",
			RunsDir:    "/root/.shell3_project/runs",
			AgentKnobs: chat.AgentKnobs{Environment: env},
			AgentNames: []string{"code"},
		}
	}
}

// newHostRemindersRuntime wires the runtime field applyHostReminders depends on
// that newTestRuntime leaves zero: a resolvable config path.
func newHostRemindersRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	rt := newTestRuntime(t, mk)
	rt.configDir = "/cfg"
	return rt
}

// TestHostReminders_Environment: with Environment=true, a fresh session carries
// an Environment standing reminder (mentions the session id) while the system
// prompt carries no host section.
func TestHostReminders_Environment(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(true))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	// Give the runs session an id (the store does this in real runs) and
	// re-assemble so the Environment reminder can advertise the session id.
	s.sess.SetID("sess-42")
	s.applyHostReminders()

	env := findReminder(s, "session id")
	if env == "" {
		t.Fatalf("expected an Environment standing reminder mentioning the session id:\n%s", hostReminderText(s))
	}
	if !strings.HasPrefix(env, "<system-reminder>") {
		t.Errorf("Environment reminder not wrapped in <system-reminder>:\n%s", env)
	}

	prompt := s.cfg.Personality.SystemPrompt
	if strings.Contains(prompt, "## Environment") {
		t.Errorf("system prompt must not contain the host Environment section:\n%s", prompt)
	}

	// The prompt-inspection view (the dashboard Status → Prompt) reads
	// Snapshot().SystemPrompt, which folds in the standing reminders so the user
	// sees the full effective context even though the authored prompt stays clean.
	shown := s.Snapshot().SystemPrompt
	if !strings.Contains(shown, "Host reminders") {
		t.Errorf("Snapshot prompt missing the Host reminders section:\n%s", shown)
	}
	if !strings.Contains(shown, "session id") {
		t.Errorf("Snapshot prompt must surface the Environment standing reminder:\n%s", shown)
	}
}

// TestHostReminders_Off: toggle off → no standing reminders at all.
func TestHostReminders_Off(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(false))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := hostReminderText(s); strings.TrimSpace(got) != "" {
		t.Errorf("expected no standing reminders with the toggle off, got:\n%s", got)
	}
}
