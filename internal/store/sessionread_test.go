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

	s1, _ := st.StartSession("", "", "")
	st.AppendHistory(s1, "user", "first question")
	st.AppendHistory(s1, "assistant", "an answer")

	s2, _ := st.StartSession("", "", "")
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

// TestStore_ListSessionsPage pins the `shell3 list-sessions` read path: project
// scoping (""=all), newest-first paging via limit/offset, and status/parent.
func TestStore_ListSessionsPage(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	a1, _ := st.StartSession("projA", "/a", "")
	st.AppendHistory(a1, "user", "alpha one")
	a2, _ := st.StartSessionWithParent(a1, "projA", "/a", "") // a subagent of a1
	b1, _ := st.StartSession("projB", "/b", "")

	// All projects, newest-first.
	all, err := st.ListSessionsPage("", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID != b1 {
		t.Fatalf("all = %d sessions, newest %d; want 3 newest %d", len(all), all[0].ID, b1)
	}

	// Project-scoped.
	onlyA, err := st.ListSessionsPage("projA", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(onlyA) != 2 {
		t.Fatalf("projA = %d sessions, want 2", len(onlyA))
	}
	// a2 is newest in projA and records its parent.
	if onlyA[0].ID != a2 || onlyA[0].ParentID != a1 {
		t.Fatalf("projA newest = id %d parent %d; want id %d parent %d", onlyA[0].ID, onlyA[0].ParentID, a2, a1)
	}
	if onlyA[1].Preview != "alpha one" {
		t.Errorf("a1 preview = %q, want %q", onlyA[1].Preview, "alpha one")
	}

	// Paging: limit 1 offset 1 returns the second-newest overall.
	page, err := st.ListSessionsPage("", 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 || page[0].ID != a2 {
		t.Fatalf("limit=1 offset=1 = %+v, want only session %d", page, a2)
	}
}

// TestStore_SessionTurns pins the per-session transcript read: every turn for
// the session, in insertion order, isolated from other sessions.
func TestStore_SessionTurns(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	a, _ := st.StartSession("", "", "")
	st.AppendHistory(a, "user", "u1")
	st.AppendHistory(a, "assistant", "a1")
	st.AppendHistory(a, "user", "u2")

	b, _ := st.StartSession("", "", "")
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
