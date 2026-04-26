# Memory Features Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce store tools to 3 (`memory_upsert`, `memory_query`, `history_query`), add `core` flag to memories, paginate history by session+chunk, inject core memories into persona prompts.

**Architecture:** Schema migration adds `core` UNINDEXED column to FTS5 `memories` table via copy-rebuild. Tool surface unifies list/search and adds session-walk metadata to history. Core memories surface in persona templates via a new `CoreMemories` field on `TemplateData`.

**Tech Stack:** Go 1.x, `modernc.org/sqlite` (FTS5), `text/template`, internal packages: `internal/store`, `internal/persona`, `internal/chat`, `internal/scaffold`, `cmd/shell3`.

**Spec:** `docs/superpowers/specs/2026-04-26-memory-features-design.md`

---

## File Structure

| File | Responsibility | Action |
|---|---|---|
| `internal/store/store.go` | SQLite-backed store; schema, memory + history APIs | Modify (replace memory + history APIs, add migration) |
| `internal/store/store_test.go` | Store unit tests | Replace tests for renamed APIs; add new coverage |
| `internal/persona/persona.go` | Persona loader; tool defs; `TemplateData` | Modify — add `CoreMemories`, replace 6 store tools with 3 |
| `internal/persona/persona_test.go` | Persona unit tests | Add test for `CoreMemories` rendering |
| `internal/chat/tools.go` | Tool dispatcher | Replace `dispatchStore` cases with 3 tools |
| `cmd/shell3/run.go` | Session bootstrap | Load core memories before persona; print 2KB warning |
| `internal/scaffold/scaffold.go` | `shell3 init` template | Update `codePersonaTemplate` for new tool names + `CoreMemories` |
| `.shell3/personas/base.md` | Local default persona | Update tool docs + add `CoreMemories` block |

---

## Task 1: Add `core` column migration to memories table

**Files:**
- Modify: `internal/store/store.go:28-54`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write failing test for core column presence**

Append to `internal/store/store_test.go`:

```go
func TestStore_MemoriesHasCoreColumn(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	// Reach into raw DB via a no-arg query that fails if `core` is absent.
	// We use the public API: store a core memory, retrieve it.
	if err := st.MemoryUpsert("k1", "v1", boolPtr(true)); err != nil {
		t.Fatal(err)
	}
	results, err := st.MemoryQuery("", true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !results[0].Core {
		t.Fatalf("expected 1 core memory, got %+v", results)
	}
}

func boolPtr(b bool) *bool { return &b }
```

- [ ] **Step 2: Run — verify it fails (compile error: methods don't exist)**

Run: `go test ./internal/store/... -run TestStore_MemoriesHasCoreColumn`
Expected: build fails — `MemoryUpsert`, `MemoryQuery`, `Core` field undefined.

- [ ] **Step 3: Implement migration (idempotent)**

Replace `migrate` in `internal/store/store.go`:

```go
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TEXT NOT NULL,
			ended_at   TEXT,
			summary    TEXT
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS history USING fts5(
			content,
			session_id UNINDEXED,
			role       UNINDEXED,
			created_at UNINDEXED
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories USING fts5(
			key,
			value,
			core       UNINDEXED,
			updated_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return migrateMemoriesAddCore(db)
}

// migrateMemoriesAddCore adds the `core` column to legacy memories tables
// that predate the column. FTS5 has no ALTER ADD COLUMN, so we copy-rebuild.
func migrateMemoriesAddCore(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(memories)`)
	if err != nil {
		return fmt.Errorf("store: inspect memories: %w", err)
	}
	defer rows.Close()
	hasCore := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return fmt.Errorf("store: scan table_info: %w", err)
		}
		if name == "core" {
			hasCore = true
		}
	}
	if hasCore {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("store: migrate memories: begin: %w", err)
	}
	defer tx.Rollback()

	migration := []string{
		`CREATE VIRTUAL TABLE memories_new USING fts5(
			key,
			value,
			core       UNINDEXED,
			updated_at UNINDEXED
		)`,
		`INSERT INTO memories_new(rowid, key, value, core, updated_at)
			SELECT rowid, key, value, 0, updated_at FROM memories`,
		`DROP TABLE memories`,
		`ALTER TABLE memories_new RENAME TO memories`,
	}
	for _, s := range migration {
		if _, err := tx.Exec(s); err != nil {
			return fmt.Errorf("store: migrate memories: %w", err)
		}
	}
	return tx.Commit()
}
```

- [ ] **Step 4: Commit (test still fails — that's expected; migration alone doesn't add APIs yet)**

```bash
git add internal/store/store.go
git commit -m "feat(store): add core column to memories table with migration"
```

---

## Task 2: Rewrite `MemoryEntry` and replace memory APIs

**Files:**
- Modify: `internal/store/store.go:60-136`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Replace `MemoryEntry` struct**

In `internal/store/store.go`, replace the existing `MemoryEntry`:

```go
// MemoryEntry is one memory record.
type MemoryEntry struct {
	Key       string
	Value     string
	Core      bool
	UpdatedAt time.Time
}
```

- [ ] **Step 2: Replace memory CRUD methods with `MemoryUpsert` and `MemoryQuery`**

In `internal/store/store.go`, delete `MemoryStore`, `MemorySearch`, `MemoryDelete`, `MemoryList`. Add:

```go
// MemoryUpsert inserts or updates a memory entry.
//
//   - Empty value deletes any existing entry for key.
//   - On insert, core defaults to false unless explicitly set.
//   - On update, core is preserved unless explicitly set (pass *bool).
func (s *Store) MemoryUpsert(key, value string, core *bool) error {
	if key == "" {
		return fmt.Errorf("store: memory upsert: empty key")
	}
	if value == "" {
		if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
			return fmt.Errorf("store: memory delete %q: %w", key, err)
		}
		return nil
	}

	var existingCore int
	var existed bool
	err := s.db.QueryRow(`SELECT core FROM memories WHERE key = ?`, key).Scan(&existingCore)
	switch {
	case err == sql.ErrNoRows:
		existed = false
	case err != nil:
		return fmt.Errorf("store: memory probe %q: %w", key, err)
	default:
		existed = true
	}

	finalCore := 0
	switch {
	case core != nil && *core:
		finalCore = 1
	case core != nil && !*core:
		finalCore = 0
	case existed:
		finalCore = existingCore
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO memories(key, value, core, updated_at) VALUES(?, ?, ?, ?)`,
		key, value, finalCore, now,
	); err != nil {
		return fmt.Errorf("store: memory insert: %w", err)
	}
	return nil
}

// MemoryQuery returns memory entries.
//
//   - query == "" → list newest-first.
//   - query != "" → FTS5 search ranked by BM25.
//   - coreOnly filters to core entries.
//   - limit caps results; pass <=0 for default 50.
func (s *Store) MemoryQuery(query string, coreOnly bool, limit int) ([]MemoryEntry, error) {
	if limit <= 0 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	switch {
	case query == "" && !coreOnly:
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			ORDER BY updated_at DESC
			LIMIT ?
		`, limit)
	case query == "" && coreOnly:
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			WHERE core = 1
			ORDER BY updated_at DESC
			LIMIT ?
		`, limit)
	case query != "" && !coreOnly:
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			WHERE memories MATCH ?
			ORDER BY rank
			LIMIT ?
		`, query, limit)
	default: // query != "" && coreOnly
		rows, err = s.db.Query(`
			SELECT key, value, core, updated_at FROM memories
			WHERE memories MATCH ? AND core = 1
			ORDER BY rank
			LIMIT ?
		`, query, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("store: memory query: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var coreInt int
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &coreInt, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: memory scan: %w", err)
		}
		e.Core = coreInt != 0
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}
```

- [ ] **Step 3: Replace memory tests in `internal/store/store_test.go`**

Replace `TestStore_MemoryStoreAndSearch`, `TestStore_MemoryUpsert`, `TestStore_MemoryDelete`, `TestStore_MemoryList`, leaving `TestStore_MemoriesHasCoreColumn` from Task 1:

```go
func TestStore_MemoryUpsert_InsertAndUpdate(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

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
	defer st.Close()

	st.MemoryUpsert("k", "v", nil)
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
	defer st.Close()

	tr := true
	st.MemoryUpsert("k", "v1", &tr)
	st.MemoryUpsert("k", "v2", nil) // core omitted
	results, _ := st.MemoryQuery("", false, 10)
	if len(results) != 1 || !results[0].Core {
		t.Fatalf("expected core preserved on update, got %+v", results)
	}
}

func TestStore_MemoryUpsert_CoreExplicitDemote(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	tr, fa := true, false
	st.MemoryUpsert("k", "v1", &tr)
	st.MemoryUpsert("k", "v2", &fa)
	results, _ := st.MemoryQuery("", false, 10)
	if results[0].Core {
		t.Fatal("expected core=false after explicit demote")
	}
}

func TestStore_MemoryQuery_Search(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryUpsert("auth", "use JWT 1h expiry", nil)
	st.MemoryUpsert("deploy", "run migrations first", nil)

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
	defer st.Close()

	tr := true
	st.MemoryUpsert("c1", "core fact", &tr)
	st.MemoryUpsert("n1", "regular fact", nil)

	results, _ := st.MemoryQuery("", true, 10)
	if len(results) != 1 || results[0].Key != "c1" {
		t.Fatalf("expected only core entry, got %+v", results)
	}
}
```

- [ ] **Step 4: Run all store tests**

Run: `go test ./internal/store/...`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): replace memory CRUD with MemoryUpsert + MemoryQuery"
```

---

## Task 3: Replace history APIs with unified `HistoryQuery`

**Files:**
- Modify: `internal/store/store.go:170-241`
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Add types and method skeleton**

Replace `HistoryResult` and `HistoryLatest` and `SearchHistory` with:

```go
// HistoryTurn is one stored conversation turn.
type HistoryTurn struct {
	SessionID int64
	Role      string
	Content   string
	CreatedAt time.Time
	// Chunk is the chunk index this turn belongs to within its session.
	// Populated for search results so the model can fetch surrounding context.
	Chunk int
}

// HistoryGetResult is returned when fetching a chunk of a session.
type HistoryGetResult struct {
	SessionID        int64
	Chunk            int
	TotalChunks      int
	PrevSessionID    int64 // 0 if none
	NextSessionID    int64 // 0 if none
	SessionStartedAt time.Time
	SessionEndedAt   time.Time // zero if session still in progress
	Turns            []HistoryTurn
}

// HistorySearchResult is returned for search queries.
type HistorySearchResult struct {
	TotalHits int
	Hits      []HistoryTurn
}

// ChunkSize is the number of turns per history chunk.
const ChunkSize = 25
```

- [ ] **Step 2: Implement `HistoryGet`**

```go
// HistoryGet returns a chunk of one session.
//
//   - sessionID == 0 → use the latest completed session (most recent
//     row in sessions where ended_at IS NOT NULL).
//   - chunk indexes oldest→newest within the session, ChunkSize turns each.
//   - PrevSessionID/NextSessionID walk completed sessions by id.
func (s *Store) HistoryGet(sessionID int64, chunk int) (HistoryGetResult, error) {
	if chunk < 0 {
		return HistoryGetResult{}, fmt.Errorf("store: history get: chunk must be >= 0")
	}

	if sessionID == 0 {
		err := s.db.QueryRow(`
			SELECT id FROM sessions
			WHERE ended_at IS NOT NULL
			ORDER BY id DESC LIMIT 1
		`).Scan(&sessionID)
		if err == sql.ErrNoRows {
			return HistoryGetResult{}, nil
		}
		if err != nil {
			return HistoryGetResult{}, fmt.Errorf("store: history get: latest session: %w", err)
		}
	}

	var startedAt string
	var endedAt sql.NullString
	err := s.db.QueryRow(`SELECT started_at, ended_at FROM sessions WHERE id = ?`, sessionID).
		Scan(&startedAt, &endedAt)
	if err == sql.ErrNoRows {
		return HistoryGetResult{}, fmt.Errorf("store: history get: session %d not found", sessionID)
	}
	if err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: session row: %w", err)
	}

	var totalTurns int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM history WHERE CAST(session_id AS INTEGER) = ?`, sessionID,
	).Scan(&totalTurns); err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: count: %w", err)
	}

	totalChunks := (totalTurns + ChunkSize - 1) / ChunkSize
	if totalChunks == 0 {
		totalChunks = 1
	}

	rows, err := s.db.Query(`
		SELECT role, content, created_at FROM history
		WHERE CAST(session_id AS INTEGER) = ?
		ORDER BY created_at ASC
		LIMIT ? OFFSET ?
	`, sessionID, ChunkSize, chunk*ChunkSize)
	if err != nil {
		return HistoryGetResult{}, fmt.Errorf("store: history get: turns: %w", err)
	}
	defer rows.Close()

	var turns []HistoryTurn
	for rows.Next() {
		var t HistoryTurn
		var createdAt string
		if err := rows.Scan(&t.Role, &t.Content, &createdAt); err != nil {
			return HistoryGetResult{}, fmt.Errorf("store: history get: scan: %w", err)
		}
		t.SessionID = sessionID
		t.Chunk = chunk
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return HistoryGetResult{}, err
	}

	var prevID, nextID int64
	_ = s.db.QueryRow(`
		SELECT id FROM sessions
		WHERE ended_at IS NOT NULL AND id < ?
		ORDER BY id DESC LIMIT 1
	`, sessionID).Scan(&prevID)
	_ = s.db.QueryRow(`
		SELECT id FROM sessions
		WHERE id > ?
		ORDER BY id ASC LIMIT 1
	`, sessionID).Scan(&nextID)

	res := HistoryGetResult{
		SessionID:     sessionID,
		Chunk:         chunk,
		TotalChunks:   totalChunks,
		PrevSessionID: prevID,
		NextSessionID: nextID,
		Turns:         turns,
	}
	res.SessionStartedAt, _ = time.Parse(time.RFC3339, startedAt)
	if endedAt.Valid {
		res.SessionEndedAt, _ = time.Parse(time.RFC3339, endedAt.String)
	}
	return res, nil
}
```

- [ ] **Step 3: Implement `HistorySearch`**

```go
// HistorySearch runs an FTS5 search over history content and returns
// matching turns with their session_id and chunk index for follow-up fetch.
func (s *Store) HistorySearch(query string, limit int) (HistorySearchResult, error) {
	if limit <= 0 {
		limit = 20
	}

	rows, err := s.db.Query(`
		SELECT CAST(session_id AS INTEGER), role, content, created_at
		FROM history
		WHERE history MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return HistorySearchResult{}, fmt.Errorf("store: history search: %w", err)
	}
	defer rows.Close()

	var hits []HistoryTurn
	for rows.Next() {
		var t HistoryTurn
		var createdAt string
		if err := rows.Scan(&t.SessionID, &t.Role, &t.Content, &createdAt); err != nil {
			return HistorySearchResult{}, fmt.Errorf("store: history search: scan: %w", err)
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		hits = append(hits, t)
	}
	if err := rows.Err(); err != nil {
		return HistorySearchResult{}, err
	}

	// Compute chunk index for each hit: count earlier turns in same session.
	for i := range hits {
		var earlier int
		err := s.db.QueryRow(`
			SELECT COUNT(*) FROM history
			WHERE CAST(session_id AS INTEGER) = ? AND created_at < ?
		`, hits[i].SessionID, hits[i].CreatedAt.UTC().Format(time.RFC3339)).Scan(&earlier)
		if err != nil {
			return HistorySearchResult{}, fmt.Errorf("store: history search: chunk index: %w", err)
		}
		hits[i].Chunk = earlier / ChunkSize
	}

	return HistorySearchResult{TotalHits: len(hits), Hits: hits}, nil
}
```

- [ ] **Step 4: Replace history tests in `internal/store/store_test.go`**

Replace `TestStore_HistoryLatest` and `TestStore_AppendAndSearchHistory`:

```go
func TestStore_HistoryGet_DefaultsToLatestCompleted(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	s1, _ := st.StartSession()
	st.AppendHistory(s1, "user", "old")
	st.EndSession(s1)

	s2, _ := st.StartSession()
	st.AppendHistory(s2, "user", "current") // not ended

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
	defer st.Close()

	id, _ := st.StartSession()
	for i := 0; i < 60; i++ {
		st.AppendHistory(id, "user", fmt.Sprintf("turn-%d", i))
	}
	st.EndSession(id)

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
		t.Errorf("ordering wrong")
	}
}

func TestStore_HistoryGet_PrevNext(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	a, _ := st.StartSession()
	st.AppendHistory(a, "u", "a")
	st.EndSession(a)
	b, _ := st.StartSession()
	st.AppendHistory(b, "u", "b")
	st.EndSession(b)
	c, _ := st.StartSession()
	st.AppendHistory(c, "u", "c")
	st.EndSession(c)

	res, _ := st.HistoryGet(b, 0)
	if res.PrevSessionID != a || res.NextSessionID != c {
		t.Errorf("prev/next: got %d/%d want %d/%d",
			res.PrevSessionID, res.NextSessionID, a, c)
	}
}

func TestStore_HistorySearch_ReturnsLocator(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	id, _ := st.StartSession()
	for i := 0; i < 30; i++ {
		st.AppendHistory(id, "user", fmt.Sprintf("plain turn %d", i))
	}
	st.AppendHistory(id, "assistant", "JWT_EXPIRY=3600")
	st.EndSession(id)

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
		t.Errorf("chunk: got %d want 1", hit.Chunk)
	}
}
```

Add `"fmt"` import to test file if not already present.

- [ ] **Step 5: Run store tests**

Run: `go test ./internal/store/...`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): paged HistoryGet + HistorySearch with chunk locators"
```

---

## Task 4: Update tool definitions in `internal/persona/persona.go`

**Files:**
- Modify: `internal/persona/persona.go:43-49` (TemplateData), `186-242` (storeTools)

- [ ] **Step 1: Extend `TemplateData` with `CoreMemories`**

In `internal/persona/persona.go`, replace `TemplateData`:

```go
// TemplateData holds values injected into persona template bodies.
type TemplateData struct {
	Skills       string             // output of skills.BuildSection
	Time         string             // formatted current time
	CWD          string             // working directory
	Model        string             // active model name
	CoreMemories []store.MemoryEntry // memories with core=true; injected into prompt
}
```

Add import: `"github.com/weatherjean/shell3/internal/store"`.

- [ ] **Step 2: Replace `storeTools` with 3 tools**

Replace the `storeTools` slice:

```go
var storeTools = []ToolDef{
	{
		Name: "memory_upsert",
		Description: "Insert, update, or delete a project memory entry. " +
			"Pass an empty value to delete an entry. " +
			"Pass core=true to mark a fact important enough to be injected into every session prompt. " +
			"Omit core when updating to preserve its current setting.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember; empty string deletes the entry"},
				"core":  map[string]any{"type": "boolean", "description": "If true, memory is injected into the system prompt every session. Omit to preserve."},
			},
			"required": []string{"key", "value"},
		},
	},
	{
		Name: "memory_query",
		Description: "Query project memory. " +
			"Omit query to list newest-first. " +
			"Provide query for full-text search ranked by relevance. " +
			"Set core_only=true to restrict to core memories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":     map[string]any{"type": "string", "description": "Optional FTS query; omit to list all"},
				"core_only": map[string]any{"type": "boolean", "description": "Only return core memories"},
				"limit":     map[string]any{"type": "integer", "description": "Maximum results (default 50)"},
			},
		},
	},
	{
		Name: "history_query",
		Description: "Query past conversation history. " +
			"With a query, runs full-text search across all sessions; each hit includes session_id and chunk so you can fetch surrounding context. " +
			"Without a query, fetches one chunk of one session: defaults to the most recent COMPLETED session (not the current one), chunk 0. " +
			"Use the next_session_id / prev_session_id from a get response to walk the chain. " +
			"Use chunk + total_chunks to page within a long session.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query":      map[string]any{"type": "string", "description": "Optional FTS query"},
				"session_id": map[string]any{"type": "integer", "description": "Session id to fetch (get mode)"},
				"chunk":      map[string]any{"type": "integer", "description": "Chunk index within session, 0-based (get mode)"},
				"limit":      map[string]any{"type": "integer", "description": "Max search hits (search mode, default 20)"},
			},
		},
	},
}
```

- [ ] **Step 3: Run persona tests**

Run: `go test ./internal/persona/...`
Expected: pass (existing tests don't touch removed tools).

- [ ] **Step 4: Build entire project to surface call-site breakage**

Run: `go build ./...`
Expected: build fails in `internal/chat/tools.go` (uses removed tool names) and possibly `cmd/shell3/run.go`. That's caught in the next task.

- [ ] **Step 5: Commit**

```bash
git add internal/persona/persona.go
git commit -m "feat(persona): replace 6 store tools with memory_upsert/query, history_query; add CoreMemories template field"
```

---

## Task 5: Rewrite `dispatchStore` in `internal/chat/tools.go`

**Files:**
- Modify: `internal/chat/tools.go:49-129`

- [ ] **Step 1: Replace `dispatchStore` with new tool handlers**

Replace the `dispatchStore` function:

```go
func dispatchStore(name, rawArgs string, st *store.Store) string {
	if st == nil {
		return fmt.Sprintf("error: store not available for tool %s", name)
	}

	switch name {
	case "memory_upsert":
		return handleMemoryUpsert(rawArgs, st)
	case "memory_query":
		return handleMemoryQuery(rawArgs, st)
	case "history_query":
		return handleHistoryQuery(rawArgs, st)
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}

func handleMemoryUpsert(rawArgs string, st *store.Store) string {
	var args struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		Core  *bool  `json:"core"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err)
	}
	if args.Key == "" {
		return "error: key required"
	}
	if err := st.MemoryUpsert(args.Key, args.Value, args.Core); err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if args.Value == "" {
		return "Removed: " + args.Key
	}
	if args.Core != nil && *args.Core {
		return "Stored (core): " + args.Key
	}
	return "Stored: " + args.Key
}

func handleMemoryQuery(rawArgs string, st *store.Store) string {
	var args struct {
		Query    string `json:"query"`
		CoreOnly bool   `json:"core_only"`
		Limit    int    `json:"limit"`
	}
	json.Unmarshal([]byte(rawArgs), &args)

	results, err := st.MemoryQuery(args.Query, args.CoreOnly, args.Limit)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if len(results) == 0 {
		return "No memories found."
	}
	var sb strings.Builder
	for _, r := range results {
		marker := ""
		if r.Core {
			marker = " (core)"
		}
		fmt.Fprintf(&sb, "[%s%s]: %s\n", r.Key, marker, r.Value)
	}
	return sb.String()
}

func handleHistoryQuery(rawArgs string, st *store.Store) string {
	var args struct {
		Query     string `json:"query"`
		SessionID int64  `json:"session_id"`
		Chunk     int    `json:"chunk"`
		Limit     int    `json:"limit"`
	}
	json.Unmarshal([]byte(rawArgs), &args)

	if args.Query != "" {
		res, err := st.HistorySearch(args.Query, args.Limit)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if res.TotalHits == 0 {
			return "No history found."
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "search hits: %d\n", res.TotalHits)
		for _, h := range res.Hits {
			fmt.Fprintf(&sb, "[session %d chunk %d | %s | %s] %s\n",
				h.SessionID, h.Chunk,
				h.CreatedAt.Format("2006-01-02 15:04"), h.Role, h.Content)
		}
		return sb.String()
	}

	res, err := st.HistoryGet(args.SessionID, args.Chunk)
	if err != nil {
		return fmt.Sprintf("error: %v", err)
	}
	if res.SessionID == 0 && len(res.Turns) == 0 {
		return "No history found."
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "session %d, chunk %d/%d (started %s)",
		res.SessionID, res.Chunk, res.TotalChunks,
		res.SessionStartedAt.Format("2006-01-02 15:04"))
	if res.PrevSessionID != 0 {
		fmt.Fprintf(&sb, " | prev=%d", res.PrevSessionID)
	}
	if res.NextSessionID != 0 {
		fmt.Fprintf(&sb, " | next=%d", res.NextSessionID)
	}
	sb.WriteByte('\n')
	for _, t := range res.Turns {
		fmt.Fprintf(&sb, "[%s | %s] %s\n",
			t.CreatedAt.Format("2006-01-02 15:04"), t.Role, t.Content)
	}
	return sb.String()
}
```

- [ ] **Step 2: Build**

Run: `go build ./...`
Expected: build succeeds (or only `cmd/shell3/run.go` fails on `CoreMemories` not yet wired — Task 6).

- [ ] **Step 3: Commit**

```bash
git add internal/chat/tools.go
git commit -m "feat(chat): dispatch memory_upsert, memory_query, history_query"
```

---

## Task 6: Wire `CoreMemories` and 2KB warning in `cmd/shell3/run.go`

**Files:**
- Modify: `cmd/shell3/run.go:132-141`

- [ ] **Step 1: Load core memories before persona load**

In `cmd/shell3/run.go`, replace the `personaData := persona.TemplateData{...}` block (lines ~132-138) with:

```go
	var coreMemories []store.MemoryEntry
	if st != nil {
		mems, err := st.MemoryQuery("", true, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, "warning: load core memories:", err)
		} else {
			coreMemories = mems
			var bytes int
			for _, m := range mems {
				bytes += len(m.Key) + len(m.Value) + 4 // overhead
			}
			if bytes > 2048 {
				fmt.Fprintf(os.Stderr,
					"warning: core memories total %d bytes (>2KB), consider demoting some\n",
					bytes)
			}
		}
	}

	personaData := persona.TemplateData{
		Skills:       skills.BuildSection(loadedSkills),
		Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
		CWD:          cwd,
		Model:        model,
		CoreMemories: coreMemories,
	}
	pers, err := persona.Load(personasDir, personaName, personaData, st != nil, noBash, userToolDefs)
	if err != nil {
		return err
	}
```

Ensure `"github.com/weatherjean/shell3/internal/store"` is imported. If `os` and `fmt` aren't already imported, add them (likely already are).

- [ ] **Step 2: Build the binary**

Run: `go build ./...`
Expected: success.

- [ ] **Step 3: Run all tests**

Run: `go test ./...`
Expected: success.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(shell3): inject core memories into persona; warn at >2KB"
```

---

## Task 7: Add persona test for `CoreMemories` rendering

**Files:**
- Modify: `internal/persona/persona_test.go`

- [ ] **Step 1: Write test**

Append to `internal/persona/persona_test.go`:

```go
func TestPersona_CoreMemoriesRendered(t *testing.T) {
	dir := t.TempDir()
	body := `---
name: t
---
Persona body.
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}`
	writePersona(t, dir, "t", body)

	p, err := persona.Load(dir, "t", persona.TemplateData{
		CoreMemories: []store.MemoryEntry{
			{Key: "stack", Value: "Go + SQLite"},
			{Key: "style", Value: "terse"},
		},
	}, false, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.SystemPrompt, "## Core memories") {
		t.Fatalf("expected core memories section, got:\n%s", p.SystemPrompt)
	}
	if !strings.Contains(p.SystemPrompt, "- stack: Go + SQLite") {
		t.Fatalf("expected memory line, got:\n%s", p.SystemPrompt)
	}
}
```

Add imports if missing: `"strings"`, `"github.com/weatherjean/shell3/internal/store"`.

- [ ] **Step 2: Run**

Run: `go test ./internal/persona/...`
Expected: pass.

- [ ] **Step 3: Commit**

```bash
git add internal/persona/persona_test.go
git commit -m "test(persona): cover CoreMemories template rendering"
```

---

## Task 8: Update `.shell3/personas/base.md` (local default)

**Files:**
- Modify: `.shell3/personas/base.md`

- [ ] **Step 1: Replace persona body**

Overwrite `.shell3/personas/base.md` with:

```
---
name: code
description: Agentic coding assistant with bash and memory tools
model: kimi-k2.6:cloud
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are shell3 — an agentic coding assistant running in the user's terminal.

Today is {{.Time}}. Working directory: {{.CWD}}. Model: {{.Model}}.
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_upsert  — store, update, or delete a memory by key. Empty value deletes. Pass core=true to inject the memory into every future session prompt; omit core to preserve.
memory_query   — list or search memories. Omit query to list newest-first. Set core_only=true to filter.
history_query  — read past conversations. With a query: full-text search returning hits with session_id+chunk locators. Without: fetch one 25-turn chunk of one session (defaults to the latest COMPLETED session, chunk 0); response carries prev_session_id / next_session_id / total_chunks for navigation.

RULES:
- When told "remember X" → call memory_upsert immediately. Mark it core=true if it should persist across every session.
- When asked about memories or past context → call memory_query first. Never answer from training data.
- Never use bash to find or store memories.
- history_query searches and walks past conversations. Never use bash for chat history.
- After gathering enough information, respond clearly — do not call tools indefinitely.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

Read before writing. Minimal changes. Test after every change.
{{- if .Skills}}

# Skills

Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.

{{.Skills}}
{{- end}}
```

- [ ] **Step 2: Verify renders OK**

Run: `go build ./... && go test ./...`
Expected: pass.

- [ ] **Step 3: Commit**

```bash
git add .shell3/personas/base.md
git commit -m "docs(persona): update base.md for new memory tools and core memories block"
```

---

## Task 9: Update `internal/scaffold/scaffold.go` template for `shell3 init`

**Files:**
- Modify: `internal/scaffold/scaffold.go:52-110`

- [ ] **Step 1: Replace `codePersonaTemplate`**

Replace the constant body to match the updated `.shell3/personas/base.md` (without `model: kimi-k2.6:cloud` — keep model `~` so init doesn't bake a specific model):

```go
const codePersonaTemplate = `---
name: code
description: Agentic coding assistant with bash and memory tools
model: ~
provider: ~
db: ~
no_bash: false
no_memory: false
on_session_start: ~
on_session_end: ~
on_turn_start: ~
on_turn_end: ~
on_tool_call: ~
on_tool_result: ~
on_context_build: ~
on_error: ~
---
You are shell3 — an agentic coding assistant running in the user's terminal.

Today is {{.Time}}. Working directory: {{.CWD}}. Model: {{.Model}}.
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}

## Tools

bash — execute shell commands to read files, search code, run tests, and make changes.

memory_upsert  — store, update, or delete a memory by key. Empty value deletes. Pass core=true to inject the memory into every future session prompt; omit core to preserve.
memory_query   — list or search memories. Omit query to list newest-first. Set core_only=true to filter.
history_query  — read past conversations. With a query: full-text search returning hits with session_id+chunk locators. Without: fetch one 25-turn chunk of one session (defaults to the latest COMPLETED session, chunk 0); response carries prev_session_id / next_session_id / total_chunks for navigation.

RULES:
- When told "remember X" → call memory_upsert immediately. Mark it core=true if it should persist across every session.
- When asked about memories or past context → call memory_query first. Never answer from training data.
- Never use bash to find or store memories.
- history_query searches and walks past conversations. Never use bash for chat history.
- After gathering enough information, respond clearly — do not call tools indefinitely.

## bash tips

File reading — check size first:
  ls -la path/           # directory
  wc -l file.go          # single file: under 150: cat; 150-500: sed -n; over 500: rg
Search: rg 'pattern' path
Find:   fd 'pattern' or find . -name '*.go'
Edit:   sd 'old' 'new' file or sed -i 's/old/new/g' file
Test:   go test ./...

Read before writing. Minimal changes. Test after every change.
{{- if .Skills}}

# Skills

Skills are instruction files. When a skill applies to your task, read its file using bash and follow the instructions inside.

{{.Skills}}
{{- end}}`
```

- [ ] **Step 2: Run scaffold tests if any, plus full build**

Run: `go test ./... && go build ./...`
Expected: pass.

- [ ] **Step 3: Commit**

```bash
git add internal/scaffold/scaffold.go
git commit -m "feat(scaffold): update init template for new memory tools"
```

---

## Task 10: Smoke test the binary end-to-end

**Files:** none modified.

- [ ] **Step 1: Build**

Run: `go build -o /tmp/shell3 ./cmd/shell3`
Expected: success.

- [ ] **Step 2: Smoke-run migration on existing local DB (if present)**

```bash
ls -la .shell3/*.db 2>/dev/null || echo "no existing db"
/tmp/shell3 --help 2>&1 | head -20 || true
```

- [ ] **Step 3: Manual sanity (optional, if user wants)**

Run shell3, ask it to `memory_upsert key="stack" value="Go + SQLite" core=true`, restart session, verify the memory appears in the rendered system prompt header (use `/inspect` or whatever introspection exists; otherwise check via DB tooling).

- [ ] **Step 4: Final test sweep**

Run: `go test ./... -count=1`
Expected: all green.

- [ ] **Step 5: No commit needed if nothing changed**

---

## Self-Review Notes

- **Spec coverage:**
  - Tools reduced 6→3 ✓ (Tasks 4, 5)
  - `core` column + migration ✓ (Task 1)
  - Empty-value delete ✓ (Task 2 test `TestStore_MemoryUpsert_EmptyValueDeletes`)
  - `*bool` core preserve/demote ✓ (Task 2)
  - History session+chunk paging ✓ (Task 3)
  - Defaults to latest **completed** session ✓ (Task 3 test `TestStore_HistoryGet_DefaultsToLatestCompleted`)
  - Search returns `session_id` + `chunk` locator ✓ (Task 3 test `TestStore_HistorySearch_ReturnsLocator`)
  - prev/next session ids ✓ (Task 3 test `TestStore_HistoryGet_PrevNext`)
  - 25-turn `ChunkSize` ✓ named constant (Task 3)
  - `CoreMemories` in `TemplateData` ✓ (Task 4)
  - 2KB warning printed to user ✓ (Task 6)
  - Persona templates render core memories ✓ (Tasks 7, 8, 9)
- **Type consistency:**
  - `MemoryEntry`/`HistoryTurn`/`HistoryGetResult`/`HistorySearchResult` defined in Tasks 2-3 and reused consistently in 4-7.
  - `*bool` for core in `MemoryUpsert` matches the `*bool` field in chat dispatcher.
- **No placeholders.**
