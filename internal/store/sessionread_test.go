package store_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

// TestStore_ListSessions pins the dashboard read path: sessions come back
// newest-first, each carrying its message count and a preview of its first user
// message, honoring the limit.
func TestStore_ListSessions(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	s1, _ := st.StartSession("", "")
	st.AppendHistory(s1, "user", "first question")
	st.AppendHistory(s1, "assistant", "an answer")

	s2, _ := st.StartSession("", "")
	st.AppendHistory(s2, "user", "second question")

	got, err := st.ListSessions(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d sessions, want 2", len(got))
	}
	if got[0].ID != s2 || got[1].ID != s1 {
		t.Fatalf("order = [%d %d], want newest-first [%d %d]", got[0].ID, got[1].ID, s2, s1)
	}
	if got[1].NumMsgs != 2 {
		t.Errorf("s1 NumMsgs = %d, want 2", got[1].NumMsgs)
	}
	if got[1].Preview != "first question" {
		t.Errorf("s1 Preview = %q, want the first user message", got[1].Preview)
	}

	one, err := st.ListSessions(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(one) != 1 || one[0].ID != s2 {
		t.Fatalf("limit=1 returned %+v, want only the newest session %d", one, s2)
	}
}

// TestStore_SessionTurns pins the per-session transcript read: every turn for
// the session, in insertion order, isolated from other sessions.
func TestStore_SessionTurns(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	a, _ := st.StartSession("", "")
	st.AppendHistory(a, "user", "u1")
	st.AppendHistory(a, "assistant", "a1")
	st.AppendHistory(a, "user", "u2")

	b, _ := st.StartSession("", "")
	st.AppendHistory(b, "user", "other")

	turns, err := st.SessionTurns(a)
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 3 {
		t.Fatalf("got %d turns, want 3 (no bleed from the other session)", len(turns))
	}
	wantRole := []string{"user", "assistant", "user"}
	wantContent := []string{"u1", "a1", "u2"}
	for i, tn := range turns {
		if tn.Role != wantRole[i] || tn.Content != wantContent[i] {
			t.Errorf("turn %d = (%s,%q), want (%s,%q)", i, tn.Role, tn.Content, wantRole[i], wantContent[i])
		}
		if tn.SessionID != a {
			t.Errorf("turn %d SessionID = %d, want %d", i, tn.SessionID, a)
		}
	}
}
