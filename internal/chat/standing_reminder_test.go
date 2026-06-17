package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/runs"
)

func TestStandingRemindersNotPersisted(t *testing.T) {
	st, _ := runs.Open(t.TempDir() + "/shell3_project")
	id, _ := st.NewSession(runs.Meta{Workdir: "/w", ConfigPath: "/c"})
	s := NewSession(SessionOpts{Store: st, ID: id})

	s.SetStandingReminders([]string{"<system-reminder>env</system-reminder>"})
	// Shown to the dashboard…
	if got := s.Reminders(); len(got) != 1 || !strings.Contains(got[0].Text, "env") {
		t.Fatalf("standing reminder not in reminderLog: %+v", got)
	}
	// …but NOT persisted (it regenerates on resume).
	if lines, _ := st.LoadReminders(id); len(lines) != 0 {
		t.Fatalf("standing reminder must not be persisted, sidecar has %d", len(lines))
	}
	// Re-setting replaces, does not accumulate.
	s.SetStandingReminders([]string{"<system-reminder>env2</system-reminder>"})
	if got := s.Reminders(); len(got) != 1 || !strings.Contains(got[0].Text, "env2") {
		t.Fatalf("re-set should replace standing reminders: %+v", got)
	}
}
