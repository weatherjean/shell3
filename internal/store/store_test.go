package store_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func boolPtr(b bool) *bool { return &b }

func TestStore_Open(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
}

func TestStore_MemoryUpsert_InsertAndUpdate(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	if err := st.MemoryUpsert("k", "v1", nil); err != nil {
		t.Fatal(err)
	}
	if err := st.MemoryUpsert("k", "v2", nil); err != nil {
		t.Fatal(err)
	}
	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 1 || results[0].Value != "v2" {
		t.Fatalf("expected single row v2, got %+v", results)
	}
}

func TestStore_MemoryUpsert_EmptyValueDeletes(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("k", "v", nil)
	if err := st.MemoryUpsert("k", "", nil); err != nil {
		t.Fatal(err)
	}
	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 0 {
		t.Fatalf("expected 0 rows after empty-value delete, got %d", len(results))
	}
}

func TestStore_MemoryUpsert_CorePreservedOnUpdate(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("k", "v1", boolPtr(true))
	_ = st.MemoryUpsert("k", "v2", nil) // core omitted
	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 1 || !results[0].Core {
		t.Fatalf("expected core preserved on update, got %+v", results)
	}
}

func TestStore_MemoryUpsert_CoreExplicitDemote(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("k", "v1", boolPtr(true))
	_ = st.MemoryUpsert("k", "v2", boolPtr(false))
	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 1 || results[0].Core {
		t.Fatalf("expected core=false after explicit demote, got %+v", results)
	}
}

func TestStore_MemoryQuery_Search(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("auth", "use JWT 1h expiry", nil)
	_ = st.MemoryUpsert("deploy", "run migrations first", nil)

	results, err := st.MemoryQuery("JWT", false, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 || results[0].Key != "auth" {
		t.Fatalf("expected auth result, got %+v", results)
	}
}

func TestStore_MemoryQuery_CoreOnly(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("c1", "core fact", boolPtr(true))
	_ = st.MemoryUpsert("n1", "regular fact", nil)

	results, _ := st.MemoryQuery("", true, 10)
	if len(results) != 1 || results[0].Key != "c1" {
		t.Fatalf("expected only core entry, got %+v", results)
	}
}

func TestStore_MemoryQuery_ListNewestFirst(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	_ = st.MemoryUpsert("a", "1", nil)
	_ = st.MemoryUpsert("b", "2", nil)
	_ = st.MemoryUpsert("c", "3", nil)

	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 3 || results[0].Key != "c" {
		t.Fatalf("expected newest-first c,b,a, got %+v", results)
	}
}

func TestStore_SessionLifecycle(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	id, err := st.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero session id")
	}
	if err := st.EndSession(id); err != nil {
		t.Fatal(err)
	}
}

func TestStore_HistoryGet_DefaultsToLatestCompleted(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	s1, _ := st.StartSession()
	_ = st.AppendHistory(s1, "user", "old")
	_ = st.EndSession(s1)

	s2, _ := st.StartSession()
	_ = st.AppendHistory(s2, "user", "current") // not ended

	res, err := st.HistoryGet(0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID != s1 {
		t.Fatalf("expected default to latest completed s1=%d, got %d", s1, res.SessionID)
	}
	if len(res.Turns) != 1 || res.Turns[0].Content != "old" {
		t.Fatalf("unexpected turns: %+v", res.Turns)
	}
	if res.NextSessionID != s2 {
		t.Errorf("expected NextSessionID=%d, got %d", s2, res.NextSessionID)
	}
}

func TestStore_HistoryGet_Chunking(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	id, _ := st.StartSession()
	for i := 0; i < 60; i++ {
		_ = st.AppendHistory(id, "user", fmt.Sprintf("turn-%d", i))
	}
	_ = st.EndSession(id)

	r0, _ := st.HistoryGet(id, 0)
	r1, _ := st.HistoryGet(id, 1)
	r2, _ := st.HistoryGet(id, 2)

	if r0.TotalChunks != 3 {
		t.Errorf("TotalChunks: want 3 got %d", r0.TotalChunks)
	}
	if len(r0.Turns) != store.ChunkSize || len(r1.Turns) != store.ChunkSize || len(r2.Turns) != 10 {
		t.Errorf("chunk sizes: %d %d %d", len(r0.Turns), len(r1.Turns), len(r2.Turns))
	}
	if r0.Turns[0].Content != "turn-0" || r2.Turns[len(r2.Turns)-1].Content != "turn-59" {
		t.Errorf("ordering wrong: %s ... %s", r0.Turns[0].Content, r2.Turns[len(r2.Turns)-1].Content)
	}
}

func TestStore_HistoryGet_PrevNext(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	a, _ := st.StartSession()
	_ = st.AppendHistory(a, "u", "a")
	_ = st.EndSession(a)
	b, _ := st.StartSession()
	_ = st.AppendHistory(b, "u", "b")
	_ = st.EndSession(b)
	c, _ := st.StartSession()
	_ = st.AppendHistory(c, "u", "c")
	_ = st.EndSession(c)

	res, _ := st.HistoryGet(b, 0)
	if res.PrevSessionID != a || res.NextSessionID != c {
		t.Errorf("prev/next: got %d/%d want %d/%d",
			res.PrevSessionID, res.NextSessionID, a, c)
	}
}

func TestStore_HistorySearch_ReturnsLocator(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer func() { _ = st.Close() }()

	id, _ := st.StartSession()
	for i := 0; i < 30; i++ {
		_ = st.AppendHistory(id, "user", fmt.Sprintf("plain turn %d", i))
	}
	_ = st.AppendHistory(id, "assistant", "JWT_EXPIRY=3600")
	_ = st.EndSession(id)

	res, err := st.HistorySearch("JWT_EXPIRY", 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalHits == 0 {
		t.Fatal("expected hit")
	}
	hit := res.Hits[0]
	if hit.SessionID != id {
		t.Errorf("session_id: got %d want %d", hit.SessionID, id)
	}
	if hit.Chunk != 1 {
		t.Errorf("chunk: got %d want 1 (turn at index 30, ChunkSize=25)", hit.Chunk)
	}
}


func TestStore_HistorySearch_PunctuationSafe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sid, _ := st.StartSession()
	_ = st.AppendHistory(sid, "user", "make cobra cli colorful")
	_ = st.AppendHistory(sid, "assistant", "use lipgloss for styling")
	_ = st.EndSession(sid)

	res, err := st.HistorySearch("cobra colorful cli ?", 10)
	if err != nil {
		t.Fatalf("search with `?` should not error: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected at least one hit for sanitized query")
	}
}

func TestStore_MemoryQuery_PunctuationSafe(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.MemoryUpsert("k", "JWT auth token spec", boolPtr(false))

	res, err := st.MemoryQuery("JWT?", false, 5)
	if err != nil {
		t.Fatalf("query with `?` should not error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least one hit for sanitized memory query")
	}
}

func TestStore_HistorySearchExpr_OrAnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	sid, _ := st.StartSession()
	_ = st.AppendHistory(sid, "user", "cobra cli colorful")
	_ = st.AppendHistory(sid, "assistant", "termenv handles ansi colors")
	_ = st.EndSession(sid)

	exprAny := store.BuildFTSExpr([]string{"cobra", "termenv"}, false)
	r, err := st.HistorySearchExpr(exprAny, 10)
	if err != nil {
		t.Fatalf("OR search: %v", err)
	}
	if r.TotalHits < 2 {
		t.Fatalf("OR should match both turns, got %d", r.TotalHits)
	}

	exprAll := store.BuildFTSExpr([]string{"cobra", "termenv"}, true)
	r2, err := st.HistorySearchExpr(exprAll, 10)
	if err != nil {
		t.Fatalf("AND search: %v", err)
	}
	if r2.TotalHits != 0 {
		t.Fatalf("AND should match zero turns, got %d", r2.TotalHits)
	}
}

func TestStore_MemorySearchExpr_Or(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	_ = st.MemoryUpsert("a", "JWT auth", boolPtr(false))
	_ = st.MemoryUpsert("b", "kafka pipeline", boolPtr(false))

	expr := store.BuildFTSExpr([]string{"JWT", "kafka"}, false)
	res, err := st.MemorySearchExpr(expr, false, 5)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 2 {
		t.Fatalf("OR should return 2 memories, got %d", len(res))
	}
}
