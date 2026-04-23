# Memory, History & Sessions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire a single SQLite DB (`shell3.db`) for memory, history, and sessions into both agent commands (`shell3 run` and `shell3 code`), exposing memory and history as LLM-callable tools.

**Architecture:** New `internal/store` package owns the DB with three tables: `sessions` (plain), `history` (FTS5), `memories` (FTS5). `internal/tools/memory.go` updates to use `*store.Store`. Both `cmd/shell3/run.go` and `internal/codeagent/loop.go` wire in store for session lifecycle and history appending.

**Tech Stack:** Go, `modernc.org/sqlite` (pure-Go SQLite driver, already in go.mod), SQLite FTS5

---

## File Map

| Action | Path | Responsibility |
|--------|------|----------------|
| Create | `internal/store/store.go` | DB open/close, all memory + session + history methods |
| Create | `internal/store/store_test.go` | Tests for all store methods |
| Modify | `internal/tools/memory.go` | Swap `*memory.DB` → `*store.Store`; add `MemoryRemoveTool`, `HistorySearchTool` |
| Modify | `internal/tools/memory_test.go` | Swap `memory.Open` → `store.Open` in tests; add tests for new tools |
| Modify | `internal/config/config.go` | Add `StoreDB string` field to `ProjectConfig` |
| Modify | `internal/config/config_test.go` | Assert `StoreDB` parsed from YAML |
| Modify | `internal/scaffold/scaffold.go` | Add `shell3.db` to gitignore; open store on init to create DB |
| Modify | `internal/scaffold/scaffold_test.go` | Assert `shell3.db` created on init |
| Modify | `cmd/shell3/run.go` | Replace memory+history with store; session lifecycle; history append per turn |
| Modify | `internal/codeagent/loop.go` | Add `Store` to Config; session lifecycle; history append; tool dispatch |
| Modify | `cmd/shell3/code.go` | Open store; pass to `codeagent.Config` |

---

## Task 1: `internal/store` — memory methods

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests for Open, MemoryStore, MemorySearch, MemoryDelete**

`internal/store/store_test.go`:
```go
package store_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestStore_Open(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
}

func TestStore_MemoryStoreAndSearch(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	if err := st.MemoryStore("auth-decision", "use JWT with 1h expiry"); err != nil {
		t.Fatal(err)
	}
	if err := st.MemoryStore("deploy-notes", "always run migrations before deploy"); err != nil {
		t.Fatal(err)
	}

	results, err := st.MemorySearch("JWT", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Key != "auth-decision" {
		t.Errorf("got key %q, want auth-decision", results[0].Key)
	}
}

func TestStore_MemoryUpsert(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryStore("key1", "original")
	st.MemoryStore("key1", "updated")

	results, _ := st.MemorySearch("updated", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
}

func TestStore_MemoryDelete(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryStore("to-delete", "some value")
	if err := st.MemoryDelete("to-delete"); err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("some value", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -v
```
Expected: compile error — package does not exist yet.

- [ ] **Step 3: Implement `internal/store/store.go` — memory methods**

```go
// Package store provides a SQLite-backed store for memories, history, and sessions.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database with tables for sessions, history, and memories.
type Store struct{ db *sql.DB }

// Open opens or creates the SQLite store at path and runs schema migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

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
			updated_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// MemoryEntry is one memory record.
type MemoryEntry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// MemoryStore upserts key with value into the memory store.
func (s *Store) MemoryStore(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO memories(key, value, updated_at) VALUES(?, ?, ?)`, key, value, now); err != nil {
		return fmt.Errorf("store: memory insert: %w", err)
	}
	return nil
}

// MemorySearch runs an FTS5 full-text search and returns up to limit results by BM25 rank.
func (s *Store) MemorySearch(query string, limit int) ([]MemoryEntry, error) {
	rows, err := s.db.Query(`
		SELECT key, value, updated_at
		FROM memories
		WHERE memories MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: memory search: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: memory scan: %w", err)
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}

// MemoryDelete removes the entry with the given key from the memory store.
func (s *Store) MemoryDelete(key string) error {
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete %q: %w", key, err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/store/... -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): add Store package with memory methods"
```

---

## Task 2: `internal/store` — session and history methods

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

- [ ] **Step 1: Write failing tests for sessions and history**

Append to `internal/store/store_test.go`:
```go
func TestStore_SessionLifecycle(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

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

func TestStore_AppendAndSearchHistory(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "how do I configure JWT expiry")
	st.AppendHistory(sessionID, "assistant", "set JWT_EXPIRY env var to 3600")

	results, err := st.SearchHistory("JWT", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one history result")
	}
	if results[0].SessionID != sessionID {
		t.Errorf("got session_id %d, want %d", results[0].SessionID, sessionID)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/store/... -run TestStore_Session -v
go test ./internal/store/... -run TestStore_AppendAndSearchHistory -v
```
Expected: compile error — `StartSession`, `EndSession`, `AppendHistory`, `SearchHistory` undefined.

- [ ] **Step 3: Add session and history methods to `internal/store/store.go`**

Append to `internal/store/store.go` (after `MemoryDelete`):
```go
// StartSession inserts a new session row and returns its id.
func (s *Store) StartSession() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO sessions(started_at) VALUES(?)`, now)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	return res.LastInsertId()
}

// EndSession sets ended_at for the given session.
func (s *Store) EndSession(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("store: end session %d: %w", id, err)
	}
	return nil
}

// AppendHistory stores one conversation turn in the history FTS5 table.
func (s *Store) AppendHistory(sessionID int64, role, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO history(content, session_id, role, created_at) VALUES(?, ?, ?, ?)`,
		content, sessionID, role, now,
	)
	if err != nil {
		return fmt.Errorf("store: append history: %w", err)
	}
	return nil
}

// HistoryResult is one row from a history search.
type HistoryResult struct {
	SessionID        int64
	Role             string
	Content          string
	CreatedAt        time.Time
	SessionStartedAt time.Time
}

// SearchHistory runs an FTS5 search on history content and returns up to limit results.
// Results include the session started_at for display context.
func (s *Store) SearchHistory(query string, limit int) ([]HistoryResult, error) {
	rows, err := s.db.Query(`
		SELECT h.session_id, h.role, h.content, h.created_at, s.started_at
		FROM (
			SELECT session_id, role, content, created_at
			FROM history
			WHERE history MATCH ?
			ORDER BY rank
			LIMIT ?
		) h
		JOIN sessions s ON CAST(h.session_id AS INTEGER) = s.id
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search history: %w", err)
	}
	defer rows.Close()

	var results []HistoryResult
	for rows.Next() {
		var r HistoryResult
		var createdAt, sessionStartedAt string
		if err := rows.Scan(&r.SessionID, &r.Role, &r.Content, &createdAt, &sessionStartedAt); err != nil {
			return nil, fmt.Errorf("store: history scan: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		r.SessionStartedAt, _ = time.Parse(time.RFC3339, sessionStartedAt)
		results = append(results, r)
	}
	return results, rows.Err()
}
```

- [ ] **Step 4: Run all store tests to verify they pass**

```bash
go test ./internal/store/... -v
```
Expected: all 6 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): add session and history methods"
```

---

## Task 3: `internal/tools` — update to use store, add MemoryRemoveTool and HistorySearchTool

**Files:**
- Modify: `internal/tools/memory.go`
- Modify: `internal/tools/memory_test.go`

- [ ] **Step 1: Write failing tests**

Replace the full contents of `internal/tools/memory_test.go` with:
```go
package tools_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tools"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMemorySearchTool(t *testing.T) {
	st := openTestStore(t)
	st.MemoryStore("jwt", "use JWT with 1h expiry")

	tool := tools.NewMemorySearchTool(st)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMemoryStoreTool(t *testing.T) {
	st := openTestStore(t)

	tool := tools.NewMemoryStoreTool(st)
	_, err := tool.Execute(context.Background(), map[string]any{
		"key":   "auth",
		"value": "JWT tokens",
	})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("JWT", 5)
	if len(results) == 0 {
		t.Error("expected stored entry to be searchable")
	}
}

func TestMemoryRemoveTool(t *testing.T) {
	st := openTestStore(t)
	st.MemoryStore("temp-key", "temp value")

	tool := tools.NewMemoryRemoveTool(st)
	_, err := tool.Execute(context.Background(), map[string]any{"key": "temp-key"})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("temp value", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results after remove, got %d", len(results))
	}
}

func TestHistorySearchTool(t *testing.T) {
	st := openTestStore(t)
	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "how do I set up JWT authentication")
	st.AppendHistory(sessionID, "assistant", "use the jwt-go library")

	tool := tools.NewHistorySearchTool(st)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT authentication"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result == "No history found." {
		t.Error("expected non-empty history result")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/tools/... -v
```
Expected: compile errors — `NewMemoryRemoveTool`, `NewHistorySearchTool` undefined; `NewMemorySearchTool`/`NewMemoryStoreTool` wrong type.

- [ ] **Step 3: Replace `internal/tools/memory.go`**

```go
package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

// MemorySearchTool searches the SQLite memory store by full-text query.
type MemorySearchTool struct{ db *store.Store }

func NewMemorySearchTool(db *store.Store) *MemorySearchTool { return &MemorySearchTool{db} }

func (t *MemorySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_search",
		Description: "Search project memory for relevant past decisions, notes, or context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *MemorySearchTool) Execute(_ context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.MemorySearch(q, 5)
	if err != nil {
		return "", fmt.Errorf("memory_search: %w", err)
	}
	if len(results) == 0 {
		return "No memories found.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
	}
	return sb.String(), nil
}

// MemoryStoreTool stores a key-value entry in the SQLite memory store.
type MemoryStoreTool struct{ db *store.Store }

func NewMemoryStoreTool(db *store.Store) *MemoryStoreTool { return &MemoryStoreTool{db} }

func (t *MemoryStoreTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_store",
		Description: "Store a key-value entry in project memory for future reference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember"},
			},
			"required": []string{"key", "value"},
		},
	}
}

func (t *MemoryStoreTool) Execute(_ context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	value, _ := params["value"].(string)
	if err := t.db.MemoryStore(key, value); err != nil {
		return "", fmt.Errorf("memory_store: %w", err)
	}
	return fmt.Sprintf("Stored: %s", key), nil
}

// MemoryRemoveTool removes a key-value entry from the memory store.
type MemoryRemoveTool struct{ db *store.Store }

func NewMemoryRemoveTool(db *store.Store) *MemoryRemoveTool { return &MemoryRemoveTool{db} }

func (t *MemoryRemoveTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "memory_remove",
		Description: "Remove a key-value entry from project memory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key to remove"},
			},
			"required": []string{"key"},
		},
	}
}

func (t *MemoryRemoveTool) Execute(_ context.Context, params map[string]any) (string, error) {
	key, _ := params["key"].(string)
	if err := t.db.MemoryDelete(key); err != nil {
		return "", fmt.Errorf("memory_remove: %w", err)
	}
	return fmt.Sprintf("Removed: %s", key), nil
}

// HistorySearchTool searches past conversation history by full-text query.
type HistorySearchTool struct{ db *store.Store }

func NewHistorySearchTool(db *store.Store) *HistorySearchTool { return &HistorySearchTool{db} }

func (t *HistorySearchTool) Definition() llm.ToolDefinition {
	return llm.ToolDefinition{
		Name:        "history_search",
		Description: "Search past conversation history for relevant context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	}
}

func (t *HistorySearchTool) Execute(_ context.Context, params map[string]any) (string, error) {
	q, _ := params["query"].(string)
	results, err := t.db.SearchHistory(q, 5)
	if err != nil {
		return "", fmt.Errorf("history_search: %w", err)
	}
	if len(results) == 0 {
		return "No history found.", nil
	}
	var sb strings.Builder
	for _, r := range results {
		fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
			r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
	}
	return sb.String(), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/tools/... -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tools/memory.go internal/tools/memory_test.go
git commit -m "feat(tools): update memory tools to use store.Store; add MemoryRemoveTool, HistorySearchTool"
```

---

## Task 4: `internal/config` — add StoreDB field

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test**

In `internal/config/config_test.go`, add to `TestLoadProjectConfig`:
```go
// After the existing assertions:
if cfg.StoreDB != ".shell3/shell3.db" {
    t.Errorf("got store_db %q, want .shell3/shell3.db", cfg.StoreDB)
}
```

And update the YAML string in that test to include:
```yaml
store_db: .shell3/shell3.db
```

The full updated YAML block (replace the existing `yaml` constant in the test):
```go
yaml := `
model: llama3.2
provider: ollama
default_personality: coder
memory_db: .shell3/memory.db
history_md: .shell3/history.md
store_db: .shell3/shell3.db
hooks:
  on_tool_call: ".shell3/hooks/guard.sh"
`
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/config/... -run TestLoadProjectConfig -v
```
Expected: FAIL — `cfg.StoreDB` field does not exist.

- [ ] **Step 3: Add `StoreDB` to `ProjectConfig` in `internal/config/config.go`**

Replace the `ProjectConfig` struct:
```go
type ProjectConfig struct {
	Model     string `yaml:"model"`
	Provider  string `yaml:"provider"`
	StoreDB   string `yaml:"store_db"`
	MemoryDB  string `yaml:"memory_db"`
	HistoryMD string `yaml:"history_md"`
	Hooks     Hooks  `yaml:"hooks"`
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/config/... -v
```
Expected: all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add StoreDB field to ProjectConfig"
```

---

## Task 5: `internal/scaffold` — create shell3.db on init, update gitignore

**Files:**
- Modify: `internal/scaffold/scaffold.go`
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Write failing test**

Add to `internal/scaffold/scaffold_test.go`:
```go
func TestInit_CreatesShell3DB(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	if err := scaffold.InitProject(dir, homeDir); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(dir, ".shell3", "shell3.db")
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected .shell3/shell3.db to exist: %v", err)
	}
}

func TestInit_GitignoreContainsShell3DB(t *testing.T) {
	dir := t.TempDir()
	homeDir := t.TempDir()
	writeTestCredentials(t, homeDir)

	scaffold.InitProject(dir, homeDir)

	data, err := os.ReadFile(filepath.Join(dir, ".shell3", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "shell3.db") {
		t.Error("expected .gitignore to contain shell3.db")
	}
}
```

Add `"strings"` to imports in `scaffold_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/scaffold/... -run TestInit_Creates -v
go test ./internal/scaffold/... -run TestInit_Gitignore -v
```
Expected: FAIL — `shell3.db` does not exist.

- [ ] **Step 3: Update `internal/scaffold/scaffold.go`**

Change `defaultGitignore` constant:
```go
const defaultGitignore = `memory.db
history.md
shell3.db
`
```

Update `buildConfig` to include `store_db`:
```go
func buildConfig(provider, model string) string {
	return fmt.Sprintf(`# shell3 project configuration
model: %s
provider: %s
store_db: .shell3/shell3.db
memory_db: .shell3/memory.db
history_md: .shell3/history.md
hooks:
  on_tool_call: ""
  on_context_build: ""
`, model, provider)
}
```

Add import `"github.com/weatherjean/shell3/internal/store"` to `scaffold.go`.

In `initShell3Dir`, after writing files, add:
```go
// Create the store DB so it exists before first use.
dbPath := filepath.Join(shell3Dir, "shell3.db")
if _, err := os.Stat(dbPath); os.IsNotExist(err) {
    st, err := store.Open(dbPath)
    if err != nil {
        return fmt.Errorf("scaffold: create store: %w", err)
    }
    st.Close()
}
```

Place this block after the `for path, content := range files` loop, before `return nil`.

- [ ] **Step 4: Run all scaffold tests to verify they pass**

```bash
go test ./internal/scaffold/... -v
```
Expected: all 5 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/scaffold.go internal/scaffold/scaffold_test.go
git commit -m "feat(scaffold): create shell3.db on init; add to gitignore"
```

---

## Task 6: `cmd/shell3/run.go` — wire store

**Files:**
- Modify: `cmd/shell3/run.go`

This task replaces the `memory.DB` + `history.md` setup with `store.Store`. Session lifecycle and per-turn history appending are added. The `history.Load`/`history.Save` calls are removed — sessions are ephemeral; history is appended to DB only.

- [ ] **Step 1: Replace full contents of `cmd/shell3/run.go`**

```go
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/weatherjean/shell3/internal/agent"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/hooks"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/output"
	"github.com/weatherjean/shell3/internal/skills"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tools"
)

type runFlags struct {
	model      string
	baseURL    string
	apiKey     string
	storeDB    string
	stream     bool
	out        string
	skillPaths []string
	noBash     bool
	noMemory   bool
}

func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "shell3 [message]",
		Short: "Run the agent",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAgent(cmd.Context(), f, strings.Join(args, " "))
		},
	}
	bindRunFlags(cmd, f)
	return cmd
}

func bindRunFlags(cmd *cobra.Command, f *runFlags) {
	cmd.Flags().StringVar(&f.model, "model", "", "Model override")
	cmd.Flags().StringVar(&f.baseURL, "base-url", "", "LLM base URL override")
	cmd.Flags().StringVar(&f.apiKey, "api-key", "", "API key override")
	cmd.Flags().StringVar(&f.storeDB, "store-db", "", "SQLite store DB path")
	cmd.Flags().BoolVar(&f.stream, "stream", false, "Emit JSONL event stream")
	cmd.Flags().StringVar(&f.out, "out", "", "Pipe output to this command")
	cmd.Flags().StringSliceVar(&f.skillPaths, "skills", nil, "Additional skill directories")
	cmd.Flags().BoolVar(&f.noBash, "no-bash", false, "Disable bash tool")
	cmd.Flags().BoolVar(&f.noMemory, "no-memory-tools", false, "Disable memory and history tools")
}

func runAgent(ctx context.Context, f *runFlags, initialInput string) error {
	cwd, _ := os.Getwd()
	homeDir, _ := os.UserHomeDir()

	projCfg, err := config.LoadProject(cwd)
	if err != nil {
		return err
	}
	creds, err := config.LoadCredentials(homeDir)
	if err != nil {
		return err
	}
	if err := config.Validate(projCfg, creds); err != nil {
		return err
	}

	model, baseURL, apiKey := resolveConnectionParams(projCfg, creds, f)
	storeDB := coalesce(f.storeDB, projCfg.StoreDB, ".shell3/shell3.db")

	emitter := buildEmitter(f.stream)
	st, ts, err := buildTools(cwd, f, storeDB)
	if err != nil {
		return err
	}
	if st != nil {
		defer st.Close()
	}

	systemPrompt := buildSystemPrompt(f.skillPaths)
	sess := &agent.Session{}

	var sessionID int64
	if st != nil {
		sessionID, err = st.StartSession()
		if err != nil {
			return fmt.Errorf("run: start session: %w", err)
		}
		defer st.EndSession(sessionID)
	}

	hookRunner := hooks.NewRunner(hooks.Config(projCfg.Hooks))
	agentCfg := agent.Config{
		SystemPrompt: systemPrompt,
		LLM:          llm.NewClient(baseURL, apiKey, model),
		Tools:        ts,
		Hooks:        hookRunner,
		Emitter:      emitter,
	}

	hookRunner.OnSessionStart(ctx)
	defer hookRunner.OnSessionEnd(ctx)

	if initialInput != "" {
		return runAndSave(ctx, agentCfg, sess, initialInput, st, sessionID)
	}
	return runInteractive(ctx, agentCfg, sess, st, sessionID)
}

func resolveConnectionParams(cfg *config.ProjectConfig, creds *config.Credentials, f *runFlags) (model, baseURL, apiKey string) {
	model = cfg.Model
	if f.model != "" {
		model = f.model
	}
	provCreds, _ := creds.Get(cfg.Provider)
	baseURL = provCreds.BaseURL
	if f.baseURL != "" {
		baseURL = f.baseURL
	}
	apiKey = provCreds.APIKey
	if f.apiKey != "" {
		apiKey = f.apiKey
	}
	return
}

func buildEmitter(stream bool) output.Emitter {
	if stream {
		return output.NewJSONLEmitter(os.Stdout)
	}
	return output.NewPlainEmitter(os.Stdout)
}

func buildTools(cwd string, f *runFlags, storeDB string) (*store.Store, []tools.Tool, error) {
	var ts []tools.Tool
	if !f.noBash {
		ts = append(ts, tools.NewBashTool(cwd, 30))
	}
	if f.noMemory || storeDB == "" {
		return nil, ts, nil
	}
	st, err := store.Open(storeDB)
	if err != nil {
		return nil, nil, fmt.Errorf("run: open store: %w", err)
	}
	ts = append(ts,
		tools.NewMemoryStoreTool(st),
		tools.NewMemorySearchTool(st),
		tools.NewMemoryRemoveTool(st),
		tools.NewHistorySearchTool(st),
	)
	return st, ts, nil
}

func buildSystemPrompt(extraSkillPaths []string) string {
	dirs := append([]string{".shell3/skills"}, extraSkillPaths...)
	loadedSkills, _ := skills.LoadAll(dirs)
	return "You are an expert software engineer. Use tools to accomplish tasks.\n" +
		skills.BuildSection(loadedSkills)
}

func runAndSave(ctx context.Context, cfg agent.Config, sess *agent.Session, input string, st *store.Store, sessionID int64) error {
	prevLen := len(sess.Messages)
	if err := agent.RunTurn(ctx, cfg, sess, input); err != nil {
		return err
	}
	if st != nil {
		for _, m := range sess.Messages[prevLen:] {
			if m.Role == llm.RoleUser || m.Role == llm.RoleAssistant {
				st.AppendHistory(sessionID, string(m.Role), m.Content)
			}
		}
	}
	return nil
}

func runInteractive(ctx context.Context, cfg agent.Config, sess *agent.Session, st *store.Store, sessionID int64) error {
	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("> ")
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			fmt.Print("> ")
			continue
		}
		if err := runAndSave(ctx, cfg, sess, line, st, sessionID); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
		}
		fmt.Print("\n> ")
	}
	return nil
}

func coalesce(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 2: Verify it compiles**

```bash
go build ./cmd/shell3/...
```
Expected: exits 0 with no output.

- [ ] **Step 3: Run the full test suite to check for regressions**

```bash
go test ./...
```
Expected: all tests PASS (the old `memory_test.go` and `tools_test.go` no longer import `internal/memory` or pass `*memory.DB`).

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "feat(run): wire store for session lifecycle, history, and memory/history tools"
```

---

## Task 7: `internal/codeagent/loop.go` + `cmd/shell3/code.go` — wire store into code agent

**Files:**
- Modify: `internal/codeagent/loop.go`
- Modify: `cmd/shell3/code.go`

- [ ] **Step 1: Add `Store` field to `Config` and `storeToolDefs` in `internal/codeagent/loop.go`**

Add import `"github.com/weatherjean/shell3/internal/store"` to `loop.go` imports.

Replace the `Config` struct:
```go
type Config struct {
	LLM           LLMClient
	Store         *store.Store
	WorkDir       string
	Provider      string
	Model         string
	Models        []string
	ModelSwitcher func(string)
}
```

Add `storeToolDefs` var after the `bashTool` var:
```go
var storeToolDefs = []llm.ToolDefinition{
	{
		Name:        "memory_store",
		Description: "Store a key-value entry in project memory for future reference.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":   map[string]any{"type": "string", "description": "Short unique key"},
				"value": map[string]any{"type": "string", "description": "Content to remember"},
			},
			"required": []string{"key", "value"},
		},
	},
	{
		Name:        "memory_search",
		Description: "Search project memory for relevant past decisions, notes, or context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name:        "memory_remove",
		Description: "Remove a key-value entry from project memory.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key to remove"},
			},
			"required": []string{"key"},
		},
	},
	{
		Name:        "history_search",
		Description: "Search past conversation history for relevant context.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
}
```

- [ ] **Step 2: Update `Run()` — session lifecycle, history append, build tools slice**

Replace the `Run` function:
```go
func Run(ctx context.Context, cfg Config) error {
	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: CodeSystemPrompt},
	}

	var sessionID int64
	if cfg.Store != nil {
		var err error
		sessionID, err = cfg.Store.StartSession()
		if err != nil {
			return fmt.Errorf("code: start session: %w", err)
		}
		defer cfg.Store.EndSession(sessionID)
	}

	activeTools := []llm.ToolDefinition{bashTool}
	if cfg.Store != nil {
		activeTools = append(activeTools, storeToolDefs...)
	}

	fmt.Println(colorYellow + colorBold + "shell3 code" + colorReset)
	if cfg.Provider != "" {
		fmt.Printf(colorDim+"provider: %s"+colorReset+"\n", cfg.Provider)
	}
	if cfg.Model != "" {
		fmt.Printf(colorDim+"model:    %s"+colorReset+"\n", cfg.Model)
	}
	fmt.Println(colorDim + "type / for commands, ctrl+c to exit" + colorReset)

	var lastUsage llm.Usage

	for {
		input, err := ReadInput()
		if err == io.EOF {
			fmt.Println()
			return nil
		}
		if err != nil {
			return err
		}

		if handled := handleSlashCommand(input, &cfg, &messages, &lastUsage); handled {
			continue
		}

		prevLen := len(messages)
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: input})
		messages = runTurn(ctx, cfg, messages, activeTools, &lastUsage)

		if cfg.Store != nil {
			for _, m := range messages[prevLen:] {
				if m.Role == llm.RoleUser || m.Role == llm.RoleAssistant {
					cfg.Store.AppendHistory(sessionID, string(m.Role), m.Content)
				}
			}
		}

		fmt.Println()
	}
}
```

- [ ] **Step 3: Update `runTurn` and `streamTurn` signatures and dispatch**

Replace `runTurn`:
```go
func runTurn(ctx context.Context, cfg Config, messages []llm.Message, activeTools []llm.ToolDefinition, lastUsage *llm.Usage) []llm.Message {
	turnCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT)
	go func() {
		select {
		case <-sigChan:
			cancel()
		case <-turnCtx.Done():
		}
		signal.Stop(sigChan)
	}()

	for {
		text, toolCalls, u, cancelled, err := streamTurn(turnCtx, cfg.LLM, messages, activeTools)
		if u != nil {
			*lastUsage = *u
		}
		if cancelled {
			fmt.Println(colorDim + "\n[cancelled]" + colorReset)
			return messages
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, colorRed+"\nerror: %v\n"+colorReset, err)
			return messages
		}

		if text == "" && len(toolCalls) > 0 {
			text = " "
		}
		assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: text}
		assistantMsg.ToolCalls = toolCalls
		messages = append(messages, assistantMsg)

		if len(toolCalls) == 0 {
			return messages
		}

		for _, tc := range toolCalls {
			if turnCtx.Err() != nil {
				fmt.Println(colorDim + "[cancelled]" + colorReset)
				return messages
			}

			var out string
			if tc.Name == "bash" {
				command := parseCommand(tc.RawArgs)
				fmt.Printf(colorYellow+"$ %s"+colorReset+"\n", command)
				out = ExecuteBlock(turnCtx, command, cfg.WorkDir)
				fmt.Print(out)
			} else {
				out = dispatchStoreTool(tc.Name, tc.RawArgs, cfg.Store)
			}

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    out,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			})
		}
	}
}
```

Replace `streamTurn`:
```go
func streamTurn(ctx context.Context, client LLMClient, messages []llm.Message, tools []llm.ToolDefinition) (text string, toolCalls []llm.ToolCall, usage *llm.Usage, cancelled bool, err error) {
	var sb strings.Builder
	labelPrinted := false
	streamErr := client.Stream(ctx, messages, tools, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			if !labelPrinted {
				fmt.Print("\n" + colorBlue + "shell3:" + colorReset + "\n")
				labelPrinted = true
			}
			fmt.Print(ev.TextDelta)
			sb.WriteString(ev.TextDelta)
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Usage != nil {
			usage = ev.Usage
		}
	})
	if ctx.Err() != nil {
		return sb.String(), toolCalls, usage, true, nil
	}
	return sb.String(), toolCalls, usage, false, streamErr
}
```

- [ ] **Step 4: Add `dispatchStoreTool` function to `loop.go`**

Add after `parseCommand`:
```go
func dispatchStoreTool(name, rawArgs string, st *store.Store) string {
	if st == nil {
		return fmt.Sprintf("error: store not available for tool %s", name)
	}
	var args map[string]any
	json.Unmarshal([]byte(rawArgs), &args)

	switch name {
	case "memory_store":
		key, _ := args["key"].(string)
		value, _ := args["value"].(string)
		if err := st.MemoryStore(key, value); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("Stored: %s", key)
	case "memory_search":
		q, _ := args["query"].(string)
		results, err := st.MemorySearch(q, 5)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No memories found."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s]: %s\n", r.Key, r.Value)
		}
		return sb.String()
	case "memory_remove":
		key, _ := args["key"].(string)
		if err := st.MemoryDelete(key); err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		return fmt.Sprintf("Removed: %s", key)
	case "history_search":
		q, _ := args["query"].(string)
		results, err := st.SearchHistory(q, 5)
		if err != nil {
			return fmt.Sprintf("error: %v", err)
		}
		if len(results) == 0 {
			return "No history found."
		}
		var sb strings.Builder
		for _, r := range results {
			fmt.Fprintf(&sb, "[%s | %s | session %d]: %s\n",
				r.SessionStartedAt.Format("2006-01-02"), r.Role, r.SessionID, r.Content)
		}
		return sb.String()
	default:
		return fmt.Sprintf("unknown tool: %s", name)
	}
}
```

- [ ] **Step 5: Update `cmd/shell3/code.go` to open store and pass to Config**

Add imports to `code.go`:
```go
"path/filepath"
"github.com/weatherjean/shell3/internal/store"
```

In `runCodeLoop`, add store opening right before the `client := llm.NewClient(...)` line (in the non-flags-only branch):
```go
// Open store (best-effort — code agent works without it).
var st *store.Store
storeDBPath := filepath.Join(cwd, ".shell3", "shell3.db")
if projCfg != nil && projCfg.StoreDB != "" {
    storeDBPath = filepath.Join(cwd, projCfg.StoreDB)
}
if s, err := store.Open(storeDBPath); err == nil {
    st = s
    defer st.Close()
}
```

Update both `codeagent.Run` call sites to include `Store: st`:

For the flags-only branch:
```go
return codeagent.Run(cmd.Context(), codeagent.Config{
    LLM:           client,
    Store:         nil, // no project config in flags-only mode
    WorkDir:       cwd,
    Model:         models[0],
    Models:        models,
    ModelSwitcher: client.SetModel,
})
```

For the main branch:
```go
client := llm.NewClient(provCreds.BaseURL, provCreds.APIKey, startModel)
return codeagent.Run(cmd.Context(), codeagent.Config{
    LLM:           client,
    Store:         st,
    WorkDir:       cwd,
    Provider:      provName,
    Model:         startModel,
    Models:        models,
    ModelSwitcher: client.SetModel,
})
```

- [ ] **Step 6: Build and run full test suite**

```bash
go build ./...
go test ./...
```
Expected: build exits 0, all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/codeagent/loop.go cmd/shell3/code.go
git commit -m "feat(codeagent): wire store for session lifecycle, history append, memory/history tools"
```
