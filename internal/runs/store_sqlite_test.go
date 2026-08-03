package runs

import (
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

// Multimodal user messages index their text parts.
func TestSearchIndexesTextParts(t *testing.T) {
	st, _ := Open(t.TempDir())
	id, _ := st.NewSession(Meta{})
	if err := st.AppendMessage(id, llm.Message{
		Role: llm.RoleUser,
		ContentParts: []llm.ContentPart{
			{Type: llm.ContentPartTypeText, Text: "what is on this whiteboard photo"},
			{Type: llm.ContentPartTypeImageURL, ImageURL: "data:image/png;base64,xxxx"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	hits, err := st.Search("whiteboard", 10)
	if err != nil || len(hits) != 1 {
		t.Fatalf("Search: hits=%d err=%v", len(hits), err)
	}
}

// Threads: record, overwrite, lookup, and surface isolation.
func TestThreadRecordLookup(t *testing.T) {
	st, _ := Open(t.TempDir())
	if err := st.ThreadRecord("telegram", "42", "sess-a"); err != nil {
		t.Fatal(err)
	}
	if got, ok := st.ThreadLookup("telegram", "42"); !ok || got != "sess-a" {
		t.Fatalf("Lookup = %q, %v", got, ok)
	}
	// Same msg id on another surface stays invisible.
	if _, ok := st.ThreadLookup("serve", "42"); ok {
		t.Fatal("surface isolation broken")
	}
	// Re-record overwrites (the anchor advances as a thread continues).
	if err := st.ThreadRecord("telegram", "42", "sess-b"); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ThreadLookup("telegram", "42"); got != "sess-b" {
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
	if err := st.ThreadRecord("telegram", "7", id); err != nil {
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
	if got, ok := st2.ThreadLookup("telegram", "7"); !ok || got != id {
		t.Fatalf("thread lost across reopen: %q %v", got, ok)
	}
	if hits, _ := st2.Search("hello", 5); len(hits) != 1 {
		t.Fatalf("fts lost across reopen: %d hits", len(hits))
	}
}

// Sweep: expired sessions go (with their FTS entries and thread entries),
// recent ones stay, and keep<=0 preserves everything except trash.
func TestSweep(t *testing.T) {
	root := t.TempDir()
	st, _ := Open(root)
	oldID, _ := st.NewSession(Meta{Workdir: "/w"})
	newID_, _ := st.NewSession(Meta{Workdir: "/w"})
	for _, id := range []string{oldID, newID_} {
		if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: "searchable words"}); err != nil {
			t.Fatal(err)
		}
		if err := st.ThreadRecord("telegram", "m-"+id, id); err != nil {
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
	if _, ok := st2.ThreadLookup("telegram", "m-"+oldID); ok {
		t.Fatal("stale thread entry survived")
	}
	if _, ok := st2.ThreadLookup("telegram", "m-"+newID_); !ok {
		t.Fatal("live thread entry was dropped")
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
