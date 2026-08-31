package runs

import (
	"os"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestSessionRoundTrip(t *testing.T) {
	root := t.TempDir() + "/.shell3_project"
	s, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	id, err := s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c", Model: "m"})
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
	id, _ := s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
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

// TestNewSession_RecordsCronJob proves CronJob survives a round trip through
// SQLite: NewSession persists it, SessionMeta reads it back.
func TestNewSession_RecordsCronJob(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(Meta{Agent: "bookmarks", CronJob: "bookmarks-tick"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.SessionMeta(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.CronJob != "bookmarks-tick" {
		t.Fatalf("CronJob = %q, want bookmarks-tick", got.CronJob)
	}
}

func findMeta(t *testing.T, st *Store, id string) (Meta, bool) {
	t.Helper()
	metas, err := st.ListSessions(0)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, m := range metas {
		if m.ID == id {
			return m, true
		}
	}
	return Meta{}, false
}

// Session IDs arrive from user-controlled surfaces (shell3 ask --resume, the
// bot's views); a path-traversal id must never leak anything or escape onto
// the filesystem via job-log paths.
func TestSessionIDPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", "..", ".", "a/b", "/abs"} {
		if msgs, err := st.LoadMessages(id); err != nil || msgs != nil {
			t.Errorf("LoadMessages(%q) = %v, %v; want nil, nil", id, msgs, err)
		}
		if err := st.AddUsage(id, 1, 1); err == nil {
			t.Errorf("AddUsage(%q): want error, got nil", id)
		}
		if p := st.JobLogPath(id, "bg1"); p != "" {
			t.Errorf("JobLogPath(%q) = %q; want empty", id, p)
		}
	}
}

// A session that never stored a message leaves no trace: ending it deletes
// its row instead of writing an "ended" record for an empty shell — the
// pinned cron dispatch parent and console startups would otherwise litter
// the store with one row per process start.
func TestEndSessionRemovesEmptySession(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(Meta{Workdir: "/w", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(id); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, ok := findMeta(t, st, id); ok {
		t.Fatal("empty session should be removed")
	}
}

func TestEndSessionKeepsStoredConversation(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(Meta{Workdir: "/w", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(id); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	m, ok := findMeta(t, st, id)
	if !ok {
		t.Fatal("session with messages should survive EndSession")
	}
	if m.Status != "ended" {
		t.Fatalf("status = %q, want ended", m.Status)
	}
}

func TestEndSessionKeepsJobLogs(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(Meta{Workdir: "/w", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	logPath := st.JobLogPath(id, "bg1")
	if logPath == "" {
		t.Fatal("JobLogPath empty")
	}
	if err := os.WriteFile(logPath, []byte("out"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.EndSession(id); err != nil {
		t.Fatalf("EndSession: %v", err)
	}
	if _, ok := findMeta(t, st, id); !ok {
		t.Fatal("session with a job log should survive EndSession")
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("job log should survive: %v", err)
	}
}

func TestHasMessages(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := st.NewSession(Meta{Workdir: "/w", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if st.HasMessages(id) {
		t.Fatal("fresh session must report no messages")
	}
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if !st.HasMessages(id) {
		t.Fatal("session with a stored message must report true")
	}
}
