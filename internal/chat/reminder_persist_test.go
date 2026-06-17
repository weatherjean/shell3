package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestReminderPersistAndRestore(t *testing.T) {
	st, _ := runs.Open(t.TempDir() + "/shell3_project")
	id, _ := st.NewSession(runs.Meta{Workdir: "/w", ConfigPath: "/c"})

	s := NewSession(SessionOpts{Store: st, ID: id})
	s.append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	emitSystemReminder(s, "<system-reminder>subagent x finished</system-reminder>")

	// A fresh session for the same id restores the reminder from the sidecar.
	s2 := NewSession(SessionOpts{Store: st, ID: id})
	if err := s2.RestoreReminders(); err != nil {
		t.Fatalf("RestoreReminders: %v", err)
	}
	rems := s2.Reminders()
	if len(rems) != 1 || rems[0].Text != "<system-reminder>subagent x finished</system-reminder>" {
		t.Fatalf("want 1 restored reminder, got %+v", rems)
	}
}
