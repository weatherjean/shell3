# Memory, History & Sessions — Design

**Date:** 2026-04-23  
**Status:** Approved

## Problem

`internal/memory` and `internal/history` exist but are not wired into the agent loop. History is markdown (no FTS, no metadata). Memory is a separate SQLite DB. Sessions are ephemeral and untracked. Nothing persists across runs.

## Goals

- Persist conversation history globally, searchable by the agent
- Persist key-value memories, searchable by the agent
- Group history by session for coherent retrieval
- Single storage artifact per project, gitignored by default

## Non-Goals

- Session resume (deferred; schema supports it, no flag yet)
- History auto-injected into context on startup
- Cross-project shared history or memory

---

## Architecture

### Single DB: `.shell3/shell3.db`

Replaces `memory.db` and `history.md`. One file, one connection, one gitignore entry.

```sql
CREATE TABLE sessions (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at TEXT NOT NULL,
    ended_at   TEXT,
    summary    TEXT
);

CREATE VIRTUAL TABLE history USING fts5(
    content,
    session_id UNINDEXED,
    role       UNINDEXED,
    created_at UNINDEXED
);

CREATE VIRTUAL TABLE memories USING fts5(
    key,
    value,
    updated_at UNINDEXED
);
```

### Package: `internal/store`

Single package owns the DB connection and exposes typed methods. Existing `internal/memory` and `internal/history` collapse into `store`. `internal/agent/session.go` remains in-memory only — `store` is the persistence layer beneath it.

Public surface:

```go
type Store struct { /* unexported */ }

func Open(path string) (*Store, error)
func (s *Store) Close() error

// Sessions
func (s *Store) StartSession() (int64, error)
func (s *Store) EndSession(id int64) error

// History
func (s *Store) AppendHistory(sessionID int64, role, content string) error
func (s *Store) SearchHistory(query string, limit int) ([]HistoryResult, error)
// HistoryResult: {SessionID int64, Role string, Content string, CreatedAt time.Time, SessionStartedAt time.Time}

// Memory
func (s *Store) MemoryStore(key, value string) error
func (s *Store) MemorySearch(query string, limit int) ([]MemoryEntry, error)
// MemoryEntry: {Key string, Value string, UpdatedAt time.Time}
```

---

## Session Lifecycle

1. `shell3 code` calls `store.StartSession()` on launch → receives `session_id`
2. Every user + assistant turn: `store.AppendHistory(sessionID, role, content)`
3. On exit (normal or signal): `store.EndSession(sessionID)`
4. Session row stays in DB forever — queryable, minable

**Future `--resume <id>`:** load last assistant message from session, inject as system context seed. No schema change required.

---

## Agent Tools

Three tools exposed to the LLM:

| Tool | Args | Returns |
|------|------|---------|
| `memory_store` | `key`, `value` | confirmation |
| `memory_search` | `query` | top-5 entries by BM25 rank |
| `history_search` | `query` | top-5 turns with session metadata (started_at, role) |

Tools are registered alongside `bash` in `loop.go`. Tool execution calls `store` methods directly.

---

## Scaffold & Migration

- `scaffold.go` creates `shell3.db` on `shell3 init` via `store.Open()`
- `store.Open()` runs `CREATE TABLE/VIRTUAL TABLE IF NOT EXISTS` — no migration runner needed
- `config.yaml` gets new field: `store_db: .shell3/shell3.db`
- Old `memory_db` and `history_md` fields remain in config for backward compat but are ignored
- `.gitignore` adds `shell3.db`; old entries (`memory.db`, `history.md`) stay

---

## File Changes Summary

| File | Change |
|------|--------|
| `internal/store/store.go` | New package — DB open, sessions, history, memory |
| `internal/store/store_test.go` | Tests for all store methods |
| `internal/memory/` | Deprecated — kept for reference, not called |
| `internal/history/` | Deprecated — kept for reference, not called |
| `internal/codeagent/loop.go` | Wire store: StartSession, AppendHistory, EndSession, register 3 tools |
| `internal/scaffold/scaffold.go` | Create shell3.db on init, update gitignore |
| `internal/config/config.go` | Add `StoreDB` field |
| `.shell3/config.yaml` (template) | Add `store_db` field |
