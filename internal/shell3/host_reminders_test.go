package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func hostReminderText(s *Session) string {
	var b strings.Builder
	for _, r := range s.sess.Reminders() {
		b.WriteString(r.Text)
		b.WriteString("\n")
	}
	return b.String()
}

func findReminder(s *Session, sub string) string {
	for _, r := range s.sess.Reminders() {
		if strings.Contains(r.Text, sub) {
			return r.Text
		}
	}
	return ""
}

func hostRemindersCfg(env bool) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        fakellm.New(fakellm.Script{}),
			ModeLabel:  "code",
			StatusLine: "openai │ gpt-x",
			ConfigDir:  "/cfg",
			RunsDir:    "/root/.shell3_project/runs",
			AgentKnobs: chat.AgentKnobs{Environment: env},
		}
	}
}

func newHostRemindersRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	rt := newTestRuntime(t, mk)
	rt.configDir = "/cfg"
	return rt
}

func TestHostReminders_Environment(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg(true))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
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

	shown := s.Snapshot().SystemPrompt
	if !strings.Contains(shown, "Host reminders") {
		t.Errorf("Snapshot prompt missing the Host reminders section:\n%s", shown)
	}
	if !strings.Contains(shown, "session id") {
		t.Errorf("Snapshot prompt must surface the Environment standing reminder:\n%s", shown)
	}
}

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
