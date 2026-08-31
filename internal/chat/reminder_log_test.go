package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestReminderLog_AnchorsToMessageIndex(t *testing.T) {
	s := NewSession(SessionOpts{})
	s.append(llm.Message{Role: llm.RoleUser, Content: "hi"})

	emitSystemReminder(s, "<system-reminder>context: 10%</system-reminder>")
	s.append(llm.Message{Role: llm.RoleAssistant, Content: "hello"})

	rems := s.Reminders()
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

func TestReminderLog_ClearedOnSetMessages(t *testing.T) {
	s := NewSession(SessionOpts{})
	s.append(llm.Message{Role: llm.RoleUser, Content: "hi"})
	emitSystemReminder(s, "<system-reminder>model changed</system-reminder>")
	if len(s.Reminders()) != 1 {
		t.Fatal("setup: reminder not recorded")
	}
	s.SetMessages(nil)
	if got := len(s.Reminders()); got != 0 {
		t.Fatalf("reminders should be cleared on SetMessages, got %d", got)
	}
}
