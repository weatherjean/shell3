package runs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

func TestLatestSession(t *testing.T) {
	s, _ := Open(t.TempDir() + "/.shell3_project")
	_, _ = s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	id2, _ := s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	got, found, err := s.LatestSession("/w", "/c")
	if err != nil || !found || got != id2 {
		t.Fatalf("LatestSession got=%q found=%v err=%v want=%q", got, found, err, id2)
	}
	if _, found, _ := s.LatestSession("/other", "/c"); found {
		t.Fatal("expected no match for /other")
	}
}

// A subagent's child session (ParentID set) is a newer run than the main
// session but shares its workdir+config. resume-latest must skip it — otherwise
// a front-end restart reattaches to the subagent's transcript and silently
// replaces the user's conversation.
func TestLatestSessionSkipsChildSessions(t *testing.T) {
	s, _ := Open(t.TempDir() + "/.shell3_project")
	mainID, _ := s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	time.Sleep(2 * time.Millisecond) // guarantee the child sorts newer by id
	childID, _ := s.NewSession(Meta{Workdir: "/w", ConfigDir: "/c", ParentID: mainID})
	got, found, err := s.LatestSession("/w", "/c")
	if err != nil || !found {
		t.Fatalf("LatestSession found=%v err=%v", found, err)
	}
	if got == childID {
		t.Fatal("resume-latest returned the subagent child session — it must be skipped")
	}
	if got != mainID {
		t.Fatalf("LatestSession got=%q, want main %q", got, mainID)
	}
}

// Session IDs arrive from user-controlled surfaces (the dashboard,
// shell3 dev --resume); a path-traversal id must never escape the store.
func TestSessionIDPathTraversalRejected(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// Plant a readable messages.jsonl one level above runs/ so an id of ".."
	// (filepath.Base("..") == "..") would actually find something to leak.
	if err := os.WriteFile(filepath.Join(root, "messages.jsonl"),
		[]byte(`{"role":"user","content":"secret"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../escape", "..", ".", "a/b", "/abs"} {
		if msgs, err := st.LoadMessages(id); err != nil || msgs != nil {
			t.Errorf("LoadMessages(%q) = %v, %v; want nil, nil (mapped to an impossible dir)", id, msgs, err)
		}
		if err := st.TouchSession(id); err == nil {
			t.Errorf("TouchSession(%q): want error, got nil", id)
		}
	}
}
