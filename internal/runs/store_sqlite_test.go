package runs

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Search finds conversation text across sessions, best match first, and
// stays blind to tool output (which would bury results in noise).
func TestSearchFindsConversationText(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id1, _ := st.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	id2, _ := st.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(st.AppendMessage(id1, llm.Message{Role: llm.RoleUser, Content: "renew the wildcard certificate"}))
	must(st.AppendMessage(id1, llm.Message{Role: llm.RoleAssistant, Content: "certificate renewed via certbot"}))
	must(st.AppendMessage(id2, llm.Message{Role: llm.RoleUser, Content: "unrelated grocery list"}))
	must(st.AppendMessage(id2, llm.Message{Role: llm.RoleTool, Content: "certificate mentioned in tool output"}))

	hits, err := st.Search("certificate", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (tool output must not be indexed): %+v", len(hits), hits)
	}
	for _, h := range hits {
		if h.SessionID != id1 {
			t.Fatalf("hit outside session %s: %+v", id1, h)
		}
		if !strings.Contains(h.Snippet, "certificate") {
			t.Fatalf("snippet misses match: %+v", h)
		}
	}
}

// Current session: record, overwrite, read back, and surface isolation.
func TestCurrentSessionRecordLookup(t *testing.T) {
	st, _ := Open(t.TempDir())
	if err := st.SetCurrentSession("web", "sess-a"); err != nil {
		t.Fatal(err)
	}
	if got, ok := st.CurrentSession("web"); !ok || got != "sess-a" {
		t.Fatalf("CurrentSession = %q, %v", got, ok)
	}
	// Another surface has its own marker.
	if _, ok := st.CurrentSession("other"); ok {
		t.Fatal("surface isolation broken")
	}
	// Re-record overwrites (a /new moves the marker).
	if err := st.SetCurrentSession("web", "sess-b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.CurrentSession("web"); got != "sess-b" {
		t.Fatalf("overwrite failed: %q", got)
	}
}

// The store survives close/reopen with everything intact (persistence).
func TestReopenPersists(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	id, _ := st.NewSession(Meta{Workdir: "/w", ConfigDir: "/c", Model: "m"})
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hello there"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentSession("web", id); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	msgs, err := st2.LoadMessages(id)
	if err != nil || len(msgs) != 1 || msgs[0].Content != "hello there" {
		t.Fatalf("messages lost across reopen: %v %v", msgs, err)
	}
	if got, ok := st2.CurrentSession("web"); !ok || got != id {
		t.Fatalf("current-session marker lost across reopen: %q %v", got, ok)
	}
	if hits, _ := st2.Search("hello", 5); len(hits) != 1 {
		t.Fatalf("fts lost across reopen: %d hits", len(hits))
	}
}

// Sweep: expired sessions go (with their FTS entries and current-session markers),
// recent ones stay, and keep<=0 preserves everything except trash.
func TestSweep(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	oldID, _ := st.NewSession(Meta{Workdir: "/w"})
	newID_, _ := st.NewSession(Meta{Workdir: "/w"})
	// One marker per surface: the expired session owns "web", the recent one
	// owns "other", so the sweep's drop and its keep are both observable.
	for surface, id := range map[string]string{"web": oldID, "other": newID_} {
		if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "searchable words"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetCurrentSession(surface, id); err != nil {
			t.Fatal(err)
		}
	}
	// Age the old session by rewriting its recency.
	if _, err := st.db.Exec(`UPDATE sessions SET last_at=? WHERE id=?`,
		encTime(time.Now().Add(-40*24*time.Hour)), oldID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	removed, threadsDropped, err := Sweep(root, 30*24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 1 || removed[0] != oldID {
		t.Fatalf("removed = %v, want [%s]", removed, oldID)
	}
	if threadsDropped != 1 {
		t.Fatalf("threadsDropped = %d, want 1", threadsDropped)
	}

	st2, _ := Open(root)
	defer st2.Close()
	if _, ok := findMeta(t, st2, oldID); ok {
		t.Fatal("expired session still present")
	}
	if _, ok := findMeta(t, st2, newID_); !ok {
		t.Fatal("recent session was swept")
	}
	if _, ok := st2.CurrentSession("web"); ok {
		t.Fatal("stale current-session marker survived")
	}
	if _, ok := st2.CurrentSession("other"); !ok {
		t.Fatal("live current-session marker was dropped")
	}
	// The swept session's text must be gone from the index too.
	hits, _ := st2.Search("searchable", 10)
	if len(hits) != 1 || hits[0].SessionID != newID_ {
		t.Fatalf("fts after sweep: %+v", hits)
	}
}

// keep<=0 means keep forever: only empty trash goes.
func TestSweepKeepForever(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	ancient, _ := st.NewSession(Meta{Workdir: "/w"})
	if err := st.AppendMessage(ancient, llm.Message{Role: llm.RoleUser, Content: "old but precious"}); err != nil {
		t.Fatal(err)
	}
	trash, _ := st.NewSession(Meta{Workdir: "/w"}) // no messages, no job log
	for _, id := range []string{ancient, trash} {
		if _, err := st.db.Exec(`UPDATE sessions SET last_at=? WHERE id=?`,
			encTime(time.Now().Add(-400*24*time.Hour)), id); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	removed, _, err := Sweep(root, 0, time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if len(removed) != 1 || removed[0] != trash {
		t.Fatalf("removed = %v, want only the trash session %s", removed, trash)
	}
}

// The empty-trash rule spares dispatch parents: a session other sessions
// name as parent_id (the pinned cron parent runs no turns, so it is always
// message-less and always "live") must survive the sweep, or its children's
// lineage dangles. A plain aged empty session still goes.
func TestSweepSparesDispatchParents(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	parentID, _ := st.NewSession(Meta{Workdir: "/w"})
	childID, _ := st.NewSession(Meta{Workdir: "/w", ParentID: parentID})
	if err := st.AppendMessage(childID, llm.Message{Role: llm.RoleUser, Content: "tick"}); err != nil {
		t.Fatal(err)
	}
	orphanID, _ := st.NewSession(Meta{Workdir: "/w"})
	old := encTime(time.Now().Add(-2 * time.Hour))
	for _, id := range []string{parentID, orphanID} {
		if _, err := st.db.Exec(`UPDATE sessions SET last_at=? WHERE id=?`, old, id); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()

	removed, _, err := Sweep(root, 30*24*time.Hour, time.Now())
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if slices.Contains(removed, parentID) {
		t.Error("sweep removed a dispatch parent still referenced by a child")
	}
	if !slices.Contains(removed, orphanID) {
		t.Error("sweep kept a plain aged empty session")
	}
}

// Stale "live" rows are unclean-shutdown leftovers: Sweep runs at process
// start, when nothing from a previous run can still be live. Rows past the
// grace window flip to ended; recent ones (a concurrent `shell3 ask`) stay.
func TestSweepEndsStaleLiveSessions(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	staleID, _ := st.NewSession(Meta{Workdir: "/w"})
	if err := st.AppendMessage(staleID, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	freshID, _ := st.NewSession(Meta{Workdir: "/w"})
	if err := st.AppendMessage(freshID, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE sessions SET last_at=? WHERE id=?`,
		encTime(time.Now().Add(-2*time.Hour)), staleID); err != nil {
		t.Fatal(err)
	}
	st.Close()

	if _, _, err := Sweep(root, 30*24*time.Hour, time.Now()); err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	st, _ = Open(root)
	defer st.Close()
	var status string
	if err := st.db.QueryRow(`SELECT status FROM sessions WHERE id=?`, staleID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "ended" {
		t.Errorf("stale live session status = %q, want ended", status)
	}
	if err := st.db.QueryRow(`SELECT status FROM sessions WHERE id=?`, freshID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "live" {
		t.Errorf("recent live session status = %q, want live (may be another process's)", status)
	}
}

// Every appended message carries a timestamp in the same shape sessions use,
// so a question about a window ("how many task reports arrived overnight, how
// many were answered NO_REPLY") is answerable by a plain string compare —
// the gap that made the harness review's wasted-report count session-lifetime
// rather than windowed.
func TestAppendMessageStampsTime(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id, _ := st.NewSession(Meta{Workdir: "/w", ConfigDir: "/c"})
	before := time.Now().UTC().Add(-time.Second)
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	var ts, lastAt string
	if err := st.db.QueryRow(`SELECT ts FROM messages WHERE session_id=? AND seq=0`, id).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	got := decTime(ts)
	if got.IsZero() {
		t.Fatalf("ts %q does not parse as %s", ts, time.RFC3339Nano)
	}
	if got.Before(before) || got.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("ts %s is not around now", got)
	}
	// The row and the session's recency come from ONE clock reading, so a
	// message can never look newer than the session holding it.
	if err := st.db.QueryRow(`SELECT last_at FROM sessions WHERE id=?`, id).Scan(&lastAt); err != nil {
		t.Fatal(err)
	}
	if lastAt != ts {
		t.Fatalf("session last_at %q != message ts %q", lastAt, ts)
	}
}
