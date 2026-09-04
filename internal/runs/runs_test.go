package runs

import (
	"os"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestSessionRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, Content: "yo"},
	} {
		if err := st.AppendMessage(id, msg); err != nil {
			t.Fatal(err)
		}
	}
	msgs, err := st.LoadMessages(id)
	if err != nil || len(msgs) != 2 || msgs[0].Content != "hi" || msgs[1].Role != llm.RoleAssistant {
		t.Fatalf("LoadMessages = %+v, %v", msgs, err)
	}
}

func TestRemindersSidecar(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession()
	if err := st.AppendReminder(id, 1, "a"); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendReminder(id, 3, "b"); err != nil {
		t.Fatal(err)
	}
	got, err := st.LoadReminders(id)
	if err != nil || len(got) != 2 || got[0].Seq != 1 || got[1].Text != "b" {
		t.Fatalf("LoadReminders = %+v, %v", got, err)
	}
	if err := st.TruncateReminders(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.LoadReminders(id); len(got) != 0 {
		t.Fatalf("reminders after truncate = %+v", got)
	}
}

func sessionExists(t *testing.T, st *Store, id string) bool {
	t.Helper()
	var one int
	return st.db.QueryRow(`SELECT 1 FROM sessions WHERE id=?`, id).Scan(&one) == nil
}

func TestSessionIDPathTraversalRejected(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", "..", ".", "a/b", "/abs"} {
		if p := st.JobLogPath(id, "bg1"); p != "" {
			t.Errorf("JobLogPath(%q) = %q; want empty", id, p)
		}
	}
}

func TestEndSessionRemovesEmptySession(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession()
	if err := st.EndSession(id); err != nil {
		t.Fatal(err)
	}
	if sessionExists(t, st, id) {
		t.Fatal("empty session should be removed")
	}
}

func TestEndSessionKeepsStoredConversation(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession()
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(id); err != nil {
		t.Fatal(err)
	}
	if !sessionExists(t, st, id) {
		t.Fatal("session with messages should survive EndSession")
	}
}

func TestEndSessionKeepsJobLogs(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession()
	logPath := st.JobLogPath(id, "bg1")
	if err := os.WriteFile(logPath, []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(id); err != nil {
		t.Fatal(err)
	}
	if !sessionExists(t, st, id) {
		t.Fatal("session with a job log should survive EndSession")
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("job log should survive: %v", err)
	}
}

func TestHasMessages(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession()
	if st.hasMessages(id) {
		t.Fatal("fresh session must report no messages")
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if !st.hasMessages(id) {
		t.Fatal("stored message was not found")
	}
}
