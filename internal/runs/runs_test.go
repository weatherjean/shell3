package runs

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestSessionRoundTrip(t *testing.T) {
	root := t.TempDir() + "/.shell3_project"
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := s.NewSession(Meta{Workdir: "/w", ConfigPath: "/c", Model: "m"})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if err := s.AppendMessage(id, llm.Message{Role: "user", Content: "hi"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	if err := s.AppendMessage(id, llm.Message{Role: "assistant", Content: "yo"}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}
	msgs, err := s.LoadMessages(id)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Content != "hi" || msgs[1].Role != "assistant" {
		t.Fatalf("got %+v", msgs)
	}
}

func TestRemindersSidecar(t *testing.T) {
	s, _ := Open(t.TempDir() + "/shell3_project")
	id, _ := s.NewSession(Meta{Workdir: "/w", ConfigPath: "/c"})
	if err := s.AppendReminder(id, 1, "<system-reminder>a</system-reminder>"); err != nil {
		t.Fatalf("AppendReminder: %v", err)
	}
	if err := s.AppendReminder(id, 3, "<system-reminder>b</system-reminder>"); err != nil {
		t.Fatalf("AppendReminder: %v", err)
	}
	got, err := s.LoadReminders(id)
	if err != nil || len(got) != 2 {
		t.Fatalf("LoadReminders: got %d err %v", len(got), err)
	}
	if got[0].Seq != 1 || got[1].Seq != 3 || got[0].Text != "<system-reminder>a</system-reminder>" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if err := s.TruncateReminders(id); err != nil {
		t.Fatalf("TruncateReminders: %v", err)
	}
	if r, _ := s.LoadReminders(id); len(r) != 0 {
		t.Fatalf("want empty after truncate, got %d", len(r))
	}
}

func TestLatestSession(t *testing.T) {
	s, _ := Open(t.TempDir() + "/.shell3_project")
	_, _ = s.NewSession(Meta{Workdir: "/w", ConfigPath: "/c"})
	id2, _ := s.NewSession(Meta{Workdir: "/w", ConfigPath: "/c"})
	got, found, err := s.LatestSession("/w", "/c")
	if err != nil || !found || got != id2 {
		t.Fatalf("LatestSession got=%q found=%v err=%v want=%q", got, found, err, id2)
	}
	if _, found, _ := s.LatestSession("/other", "/c"); found {
		t.Fatal("expected no match for /other")
	}
}

// Session IDs arrive from user-controlled surfaces (read-session <id>,
// --resume); a path-traversal id must never escape the store.
func TestSessionIDPathTraversalRejected(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", "..", "a/b", "/abs"} {
		if msgs, err := st.LoadMessages(id); err != nil || msgs != nil {
			t.Errorf("LoadMessages(%q) = %v, %v; want nil, nil (mapped to an impossible dir)", id, msgs, err)
		}
		if err := st.TouchSession(id); err == nil {
			t.Errorf("TouchSession(%q): want error, got nil", id)
		}
	}
}
