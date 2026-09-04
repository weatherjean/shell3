package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func hostReminderText(s *Session) string {
	var b strings.Builder
	for _, r := range s.sess.StandingReminders() {
		b.WriteString(r)
		b.WriteString("\n")
	}
	return b.String()
}

func findReminder(s *Session, sub string) string {
	for _, r := range s.sess.StandingReminders() {
		if strings.Contains(r, sub) {
			return r
		}
	}
	return ""
}

func hostRemindersCfg() func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:     fakellm.New(fakellm.Script{}),
			ModelID: "gpt-x",
			RenderEnvironment: func(sessionID string) string {
				return "<system-reminder>\nEnvironment:\n- session: " + sessionID + "\n</system-reminder>"
			},
		}
	}
}

func newHostRemindersRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	return newTestRuntime(t, mk)
}

func TestHostReminders_Environment(t *testing.T) {
	rt := newHostRemindersRuntime(t, hostRemindersCfg())
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.applyHostReminders()

	env := findReminder(s, "Environment")
	if env == "" {
		t.Fatalf("expected an Environment standing reminder mentioning the session id:\n%s", hostReminderText(s))
	}
	if !strings.HasPrefix(env, "<system-reminder>") {
		t.Errorf("Environment reminder not wrapped in <system-reminder>:\n%s", env)
	}

	prompt := s.cfg.Profile.SystemPrompt
	if strings.Contains(prompt, "## Environment") {
		t.Errorf("system prompt must not contain the host Environment section:\n%s", prompt)
	}

}
