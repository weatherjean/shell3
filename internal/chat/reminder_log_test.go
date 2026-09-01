package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func reminderSnapshot(s *Session) []runs.ReminderLine {
	s.msgMu.RLock()
	defer s.msgMu.RUnlock()
	out := make([]runs.ReminderLine, 0, len(s.standingReminders)+len(s.reminderLog))
	for _, text := range s.standingReminders {
		out = append(out, runs.ReminderLine{Text: text})
	}
	return append(out, s.reminderLog...)
}

func TestReminderLog_AnchorsToMessageIndex(t *testing.T) {
	s := NewSession(SessionOpts{})
	s.append(llm.Message{Role: llm.RoleUser, Content: "hi"})

	emitSystemReminder(s, "<system-reminder>context: 10%</system-reminder>")
	s.append(llm.Message{Role: llm.RoleAssistant, Content: "hello"})

	rems := reminderSnapshot(s)
	if len(rems) != 1 {
		t.Fatalf("want 1 reminder, got %d", len(rems))
	}
	if rems[0].Seq != 1 {
		t.Fatalf("want reminder anchored at seq 1 (before the assistant reply), got %d", rems[0].Seq)
	}
	if rems[0].Text == "" {
		t.Fatal("reminder text not recorded")
	}
}
