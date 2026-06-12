# Durable Subagent Delegation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the per-session sink-file mechanism with replayable SQLite conversations + Unix-domain-socket transport so subagents can delegate to arbitrary depth and every completion reliably reaches a living consumer (the human at root as backstop), with dormant delegators revived to process their child's result.

**Architecture:** Three durable facts live in SQLite (`messages` for replay, `parent_session_id` as the report pointer, `inbox` for parked notifications); one ephemeral transport is a per-session Unix-domain socket. At completion a session reads its own `parent_session_id`, resolves the parent's liveness from a registry, and either pushes over the socket (live) or writes to the inbox + revives (`shell3 run --resume`) (dormant). The ancestor chain is reconstructed from persisted pointers at report time, so a revived process reports correctly without inheriting any spawn-time state.

**Tech Stack:** Go, `modernc.org/sqlite`, Unix-domain sockets (`net.UnixListener`), cobra CLI.

**Reference spec:** `docs/superpowers/specs/2026-06-12-durable-subagent-delegation-design.md`

**Clean-break mandate:** Pre-release. No data migration, no back-compat shims. The `internal/sink` package, `pkg/shell3/sink.go`, the `--append-sinkfile`/`--no-subagents` flags, and the `SHELL3_NO_SUBAGENTS` env are all **deleted**, not deprecated. Phase 7 is a dispatched cleanup sweep that removes every dead reference; Phase 8 verifies with `make`.

---

## File Structure

**New files:**
- `internal/store/messages.go` — replayable message persistence (`AppendMessage`, `LoadSessionMessages`).
- `internal/store/sessions.go` — session lifecycle + pointer + liveness + inbox + revive-claim API (split out of the growing `store.go`).
- `internal/notify/notify.go` — the `Notification` type (moved out of the retired `internal/sink`) shared by transport + inbox.
- `internal/socket/socket.go` — Unix-domain-socket listener + client (`Listen`, `Send`).
- `pkg/shell3/transport.go` — replaces `pkg/shell3/sink.go`: socket listener wiring, liveness registration, the report-at-completion walk, revive logic.

**Heavily modified:**
- `internal/store/store.go` — schema migration rewrite (messages table, sessions columns, inbox table).
- `internal/chat/turn.go`, `internal/chat/tools.go`, `internal/chat/session.go` — persist full message stream incl. tool results; mirror compaction into store; seed session from `InitialMessages`.
- `cmd/shell3/run.go`, `cmd/shell3/main.go` — split root (TUI + `--resume`) from `shell3 run`.
- `pkg/shell3/shell3.go`, `pkg/shell3/runtime.go`, `pkg/shell3/delegation.go` — resume path, socket transport, pointer reporting, delegation context no longer suppressed.
- `internal/bgjobs/bgjobs.go` — drop `SHELL3_NO_SUBAGENTS=1`; subagent self-reports via transport.
- `internal/tui/once.go`, `internal/tui/interactive.go` — resume dispatch + tail render.

**Deleted (Phase 7):**
- `internal/sink/` (whole package)
- `pkg/shell3/sink.go`

---

## Conventions used throughout

- After every code step that compiles, run `go build ./...` before the test command unless the step says otherwise.
- Test command for a single package: `go test ./internal/store/ -run TestName -v`.
- Commit messages follow the repo style; end with the `Co-Authored-By` trailer the repo uses.
- `Notification` type lives in `internal/notify` from Phase 4 on; Phases before that still reference `internal/sink`. Phase 7 deletes the old package after all references move.

---

# Phase 0 — SQLite schema & store API (replayable foundation)

This phase makes conversations replayable and adds the pointer/liveness/inbox/claim primitives. No behavior change yet — pure storage layer, fully unit-tested against `:memory:`.

### Task 0.1: Rewrite schema migration

**Files:**
- Modify: `internal/store/store.go:56-77` (the `migrate` function)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestMigrate_CreatesNewSchema(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	// Every new table/column must exist.
	checks := []string{
		`SELECT id, started_at, ended_at, summary, parent_session_id, pid, sock, status FROM sessions LIMIT 0`,
		`SELECT session_id, seq, role, content, tool_calls_json, tool_call_id, name, created_at FROM messages LIMIT 0`,
		`SELECT session_id, seq, payload_json, created_at FROM inbox LIMIT 0`,
	}
	for _, q := range checks {
		if _, err := st.db.Exec(q); err != nil {
			t.Errorf("schema missing for %q: %v", q, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMigrate_CreatesNewSchema -v`
Expected: FAIL — `no such column: parent_session_id` (and the `messages`/`inbox` tables don't exist).

- [ ] **Step 3: Rewrite the migration**

Replace the `migrate` function body (`store.go:56-77`) statements slice with:

```go
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id                INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at        TEXT NOT NULL,
			ended_at          TEXT,
			summary           TEXT,
			parent_session_id INTEGER,
			pid               INTEGER NOT NULL DEFAULT 0,
			sock              TEXT NOT NULL DEFAULT '',
			status            TEXT NOT NULL DEFAULT 'dormant'
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			session_id      INTEGER NOT NULL,
			seq             INTEGER NOT NULL,
			role            TEXT NOT NULL,
			content         TEXT NOT NULL,
			tool_calls_json TEXT NOT NULL DEFAULT '',
			tool_call_id    TEXT NOT NULL DEFAULT '',
			name            TEXT NOT NULL DEFAULT '',
			created_at      TEXT NOT NULL,
			PRIMARY KEY (session_id, seq)
		)`,
		`CREATE TABLE IF NOT EXISTS inbox (
			session_id  INTEGER NOT NULL,
			seq         INTEGER PRIMARY KEY AUTOINCREMENT,
			payload_json TEXT NOT NULL,
			created_at  TEXT NOT NULL
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS history USING fts5(
			content,
			session_id UNINDEXED,
			role       UNINDEXED,
			created_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}
```

> Note: `history` (FTS5) is kept for the `history` bash skill's search. `messages` is the new replay source of truth.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestMigrate_CreatesNewSchema -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat(store): replayable schema — messages, inbox, session pointer/liveness columns"
```

---

### Task 0.2: Message persistence + replay API

**Files:**
- Create: `internal/store/messages.go`
- Test: `internal/store/messages_test.go`

The store must round-trip a full `llm.Message` slice including `RoleTool` results. `tool_calls_json` serializes `[]llm.ToolCall`.

- [ ] **Step 1: Write the failing test**

Create `internal/store/messages_test.go`:

```go
package store

import (
	"reflect"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestMessages_RoundTrip(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	id, err := st.StartSession()
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleUser, Content: "list the files"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "bash", RawArgs: `{"command":"ls"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "a.go\nb.go\n"},
		{Role: llm.RoleAssistant, Content: "Two Go files."},
	}
	for i, m := range want {
		if err := st.AppendMessage(id, i, m); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestMessages_RoundTrip -v`
Expected: FAIL — `st.AppendMessage undefined`.

- [ ] **Step 3: Implement messages.go**

Create `internal/store/messages.go`:

```go
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// AppendMessage persists one conversation message at the given seq for a
// session. The full message is replayable: tool calls are JSON-encoded and
// tool results (RoleTool) are stored verbatim, unlike the lossy history FTS
// table. Idempotent on (session_id, seq) via INSERT OR REPLACE so a compaction
// rewrite can overwrite a seq in place.
func (s *Store) AppendMessage(sessionID int64, seq int, m llm.Message) error {
	var toolCalls string
	if len(m.ToolCalls) > 0 {
		b, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return fmt.Errorf("store: marshal tool calls: %w", err)
		}
		toolCalls = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages
		 (session_id, seq, role, content, tool_calls_json, tool_call_id, name, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		sessionID, seq, string(m.Role), m.Content, toolCalls, m.ToolCallID, m.Name, now,
	)
	if err != nil {
		return fmt.Errorf("store: append message: %w", err)
	}
	return nil
}

// DeleteMessagesFrom removes all messages at seq >= from for a session. Used by
// compaction mirroring to collapse a range before re-inserting the summary.
func (s *Store) DeleteMessagesFrom(sessionID int64, from int) error {
	if _, err := s.db.Exec(
		`DELETE FROM messages WHERE session_id = ? AND seq >= ?`, sessionID, from,
	); err != nil {
		return fmt.Errorf("store: delete messages: %w", err)
	}
	return nil
}

// LoadSessionMessages reconstructs the full ordered message slice for a session,
// suitable for seeding a resumed chat.Session.
func (s *Store) LoadSessionMessages(sessionID int64) ([]llm.Message, error) {
	rows, err := s.db.Query(
		`SELECT role, content, tool_calls_json, tool_call_id, name
		 FROM messages WHERE session_id = ? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: load messages %d: %w", sessionID, err)
	}
	defer rows.Close()
	var out []llm.Message
	for rows.Next() {
		var m llm.Message
		var role, toolCalls string
		if err := rows.Scan(&role, &m.Content, &toolCalls, &m.ToolCallID, &m.Name); err != nil {
			return nil, fmt.Errorf("store: load messages: scan: %w", err)
		}
		m.Role = llm.Role(role)
		if toolCalls != "" {
			if err := json.Unmarshal([]byte(toolCalls), &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("store: load messages: unmarshal tool calls: %w", err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

var _ = sql.ErrNoRows // keep database/sql imported if unused elsewhere
```

> Remove the `var _ = sql.ErrNoRows` line if `database/sql` is otherwise used; it's a guard against an unused-import error in isolation.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestMessages_RoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/store/messages.go internal/store/messages_test.go
git commit -m "feat(store): replayable message persistence (AppendMessage/LoadSessionMessages)"
```

---

### Task 0.3: Session pointer, liveness, inbox, and revive-claim API

**Files:**
- Create: `internal/store/sessions.go`
- Test: `internal/store/sessions_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sessions_test.go`:

```go
package store

import "testing"

func TestSession_PointerAndLiveness(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()

	parent, _ := st.StartSession()
	child, err := st.StartSessionWithParent(parent)
	if err != nil {
		t.Fatalf("start child: %v", err)
	}

	gotParent, err := st.ParentSessionID(child)
	if err != nil || gotParent != parent {
		t.Fatalf("parent pointer = %d,%v; want %d", gotParent, err, parent)
	}

	if err := st.SetLiveness(parent, 4242, "/tmp/p.sock", "live"); err != nil {
		t.Fatalf("set liveness: %v", err)
	}
	pid, sock, status, err := st.Liveness(parent)
	if err != nil || pid != 4242 || sock != "/tmp/p.sock" || status != "live" {
		t.Fatalf("liveness = %d,%q,%q,%v", pid, sock, status, err)
	}
}

func TestSession_ReviveClaim_SingleWinner(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession()
	_ = st.SetLiveness(id, 0, "", "dormant")

	won1, err := st.ClaimRevive(id)
	if err != nil {
		t.Fatalf("claim1: %v", err)
	}
	won2, _ := st.ClaimRevive(id)
	if !won1 || won2 {
		t.Fatalf("expected exactly one winner; won1=%v won2=%v", won1, won2)
	}
}

func TestSession_Inbox_AppendDrain(t *testing.T) {
	st, _ := Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession()

	_ = st.AppendInbox(id, []byte(`{"kind":"agent_done","id":"a1"}`))
	_ = st.AppendInbox(id, []byte(`{"kind":"agent_done","id":"a2"}`))

	got, err := st.DrainInbox(id)
	if err != nil || len(got) != 2 {
		t.Fatalf("drain = %d items, %v; want 2", len(got), err)
	}
	// Draining again yields nothing — drain is destructive.
	again, _ := st.DrainInbox(id)
	if len(again) != 0 {
		t.Fatalf("second drain = %d; want 0", len(again))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestSession_ -v`
Expected: FAIL — `st.StartSessionWithParent undefined`.

- [ ] **Step 3: Implement sessions.go**

Create `internal/store/sessions.go`:

```go
package store

import (
	"database/sql"
	"fmt"
	"time"
)

// StartSessionWithParent inserts a new session row whose parent_session_id
// records the report pointer (who this session reports to on completion).
func (s *Store) StartSessionWithParent(parent int64) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO sessions(started_at, parent_session_id) VALUES(?, ?)`, now, parent)
	if err != nil {
		return 0, fmt.Errorf("store: start session with parent: %w", err)
	}
	return res.LastInsertId()
}

// ParentSessionID returns the report pointer for a session, or 0 if it is a
// root (NULL parent) or not found.
func (s *Store) ParentSessionID(id int64) (int64, error) {
	var p sql.NullInt64
	err := s.db.QueryRow(`SELECT parent_session_id FROM sessions WHERE id = ?`, id).Scan(&p)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: parent session id %d: %w", id, err)
	}
	if !p.Valid {
		return 0, nil
	}
	return p.Int64, nil
}

// SetLiveness records the current process whereabouts of a session. status is
// "live", "dormant", or "reviving". pid/sock are meaningful only when live.
func (s *Store) SetLiveness(id int64, pid int, sock, status string) error {
	if _, err := s.db.Exec(
		`UPDATE sessions SET pid = ?, sock = ?, status = ? WHERE id = ?`,
		pid, sock, status, id); err != nil {
		return fmt.Errorf("store: set liveness %d: %w", id, err)
	}
	return nil
}

// Liveness reads a session's current pid/sock/status.
func (s *Store) Liveness(id int64) (pid int, sock, status string, err error) {
	err = s.db.QueryRow(`SELECT pid, sock, status FROM sessions WHERE id = ?`, id).
		Scan(&pid, &sock, &status)
	if err == sql.ErrNoRows {
		return 0, "", "dormant", nil
	}
	if err != nil {
		return 0, "", "", fmt.Errorf("store: liveness %d: %w", id, err)
	}
	return pid, sock, status, nil
}

// ClaimRevive atomically transitions a session from "dormant" to "reviving",
// returning true only for the single caller that won the race. Losers (status
// already "reviving" or "live") get false. This is the leader election that
// ensures exactly one reviver process spawns for a dormant parent.
func (s *Store) ClaimRevive(id int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE sessions SET status = 'reviving' WHERE id = ? AND status = 'dormant'`, id)
	if err != nil {
		return false, fmt.Errorf("store: claim revive %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: claim revive rows %d: %w", id, err)
	}
	return n == 1, nil
}

// AppendInbox parks a notification payload for a session to consume when it
// (re)boots. Atomic single-row insert; no coordination needed across writers.
func (s *Store) AppendInbox(id int64, payload []byte) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO inbox(session_id, payload_json, created_at) VALUES(?,?,?)`,
		id, string(payload), now); err != nil {
		return fmt.Errorf("store: append inbox %d: %w", id, err)
	}
	return nil
}

// DrainInbox returns and deletes all parked payloads for a session, oldest
// first. Destructive: a second call returns nothing.
func (s *Store) DrainInbox(id int64) ([][]byte, error) {
	rows, err := s.db.Query(
		`SELECT seq, payload_json FROM inbox WHERE session_id = ? ORDER BY seq ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("store: drain inbox %d: %w", id, err)
	}
	defer rows.Close()
	var out [][]byte
	var maxSeq int64
	for rows.Next() {
		var seq int64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, fmt.Errorf("store: drain inbox: scan: %w", err)
		}
		out = append(out, []byte(payload))
		maxSeq = seq
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		if _, err := s.db.Exec(
			`DELETE FROM inbox WHERE session_id = ? AND seq <= ?`, id, maxSeq); err != nil {
			return nil, fmt.Errorf("store: drain inbox delete: %w", err)
		}
	}
	return out, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/ -run TestSession_ -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/store/sessions.go internal/store/sessions_test.go
git commit -m "feat(store): session pointer, liveness registry, inbox, revive-claim API"
```

---

# Phase 1 — Persist full messages + compaction mirror + session seeding

Wire the chat layer to write the new `messages` table alongside the existing FTS `history`, mirror compaction into it, and allow seeding a session from loaded messages.

### Task 1.1: Persist the full message stream to `messages`

**Files:**
- Modify: `internal/chat/turn.go:609-623` (`flushMessages`)
- Test: `internal/chat/persist_test.go` (new)

The current `flushMessages` only writes user/assistant text + tool-call summaries to `history`. Extend it to *also* append the raw `llm.Message` to the new `messages` table at its real seq, including `RoleTool`.

- [ ] **Step 1: Write the failing test**

Create `internal/chat/persist_test.go`:

```go
package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

func TestFlushMessages_PersistsFullStreamIncludingToolResults(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	id, _ := st.StartSession()

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "output"},
	}
	flushMessages(st, applog.Noop(), id, 0, msgs)

	got, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[2].Role != llm.RoleTool || got[2].Content != "output" {
		t.Fatalf("tool result not persisted: %#v", got)
	}
}
```

> If `applog.Noop()` does not exist, use the repo's existing no-op logger constructor (check `internal/applog`); replace `applog.Noop()` accordingly.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestFlushMessages_PersistsFullStreamIncludingToolResults -v`
Expected: FAIL — `flushMessages` signature has no `from int`, and tool results aren't loaded back.

- [ ] **Step 3: Extend flushMessages and its callers**

In `internal/chat/turn.go`, change `flushMessages` to take a base seq and persist every message to `messages`:

```go
// flushMessages appends each message in msgs to the replayable messages table
// (full fidelity, including tool results) starting at seq `from`, and mirrors
// user/assistant/tool-call summaries into the FTS history table for search.
// Best-effort: write failures are logged, not fatal.
func flushMessages(st *store.Store, lg applog.Logger, sessionID int64, from int, msgs []llm.Message) {
	for i, m := range msgs {
		if err := st.AppendMessage(sessionID, from+i, m); err != nil {
			lg.Warn("append message failed", "session_id", sessionID, "seq", from+i, "error", err)
		}
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			appendHistory(st, lg, sessionID, string(m.Role), m.Content)
			for _, tc := range m.ToolCalls {
				appendHistory(st, lg, sessionID, "tool", toolCallSummary(tc))
			}
		}
	}
}
```

Update `saveHistory` (`turn.go:597-607`) to pass `from`:

```go
func saveHistory(st *store.Store, lg applog.Logger, sess *Session, sessionID int64, from int) {
	if st == nil {
		return
	}
	if from > len(sess.messages) {
		return
	}
	flushMessages(st, lg, sessionID, from, sess.messages[from:])
}
```

Update the `compactInto` call site (`internal/chat/tools.go`, the `flushMessages(st, lg, prevSessionID, sess.messages)` line) to pass a base seq of 0:

```go
		flushMessages(st, lg, prevSessionID, 0, sess.messages)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -run TestFlushMessages_PersistsFullStreamIncludingToolResults -v && go build ./...`
Expected: PASS and clean build.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/turn.go internal/chat/tools.go internal/chat/persist_test.go
git commit -m "feat(chat): persist full replayable message stream incl tool results"
```

---

### Task 1.2: Mirror compaction into the messages table

**Files:**
- Modify: `internal/chat/tools.go` (`compactInto`)
- Test: `internal/chat/compact_mirror_test.go` (new)

After compaction rewrites `sess.messages` to `[continuation, trigger?]`, the persisted `messages` for the *new* session id must equal that compacted list (not the pre-compaction blob), so a resume loads a within-window context.

- [ ] **Step 1: Write the failing test**

Create `internal/chat/compact_mirror_test.go`:

```go
package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

func TestCompactInto_MirrorsCompactedContextToNewSession(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession()

	sess := NewSession(SessionOpts{StoreID: id})
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "old 1"},
		{Role: llm.RoleAssistant, Content: "old 2"},
	}
	allMsgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "old 1"},
		{Role: llm.RoleAssistant, Content: "old 2"},
	}

	compactInto(CompactSummary{Summary: "did stuff"}, st, sess, allMsgs, applog.Noop())

	// New session id is now sess.id; its persisted messages must equal the
	// compacted in-memory list.
	got, err := st.LoadSessionMessages(sess.id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(sess.messages) {
		t.Fatalf("persisted %d msgs, in-memory %d", len(got), len(sess.messages))
	}
	for i := range got {
		if got[i].Role != sess.messages[i].Role || got[i].Content != sess.messages[i].Content {
			t.Fatalf("seq %d mismatch: %#v vs %#v", i, got[i], sess.messages[i])
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestCompactInto_MirrorsCompactedContextToNewSession -v`
Expected: FAIL — the new session has no `messages` rows (compactInto never writes the compacted list to the new id).

- [ ] **Step 3: Mirror the compacted list after the session roll**

In `compactInto` (`internal/chat/tools.go`), immediately after `sess.messages = newMsgs` is published under `msgMu` (the block that sets `sess.messages`), add persistence of the compacted list to the new session id:

```go
	sess.msgMu.Lock()
	sess.messages = newMsgs
	sess.msgMu.Unlock()

	// Mirror the compacted context into the replayable messages table under the
	// NEW session id, so a resume of this session loads the within-window
	// compacted history rather than the pre-compaction blob. flushMessages above
	// wrote the OUTGOING session; this writes the incoming one.
	if st != nil {
		for i, m := range newMsgs {
			if err := st.AppendMessage(sess.id, i, m); err != nil {
				lg.Warn("mirror compacted message failed", "session_id", sess.id, "seq", i, "error", err)
			}
		}
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -run TestCompactInto_MirrorsCompactedContextToNewSession -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/tools.go internal/chat/compact_mirror_test.go
git commit -m "feat(chat): mirror compacted context into messages table for replay fidelity"
```

---

### Task 1.3: Seed a session from loaded messages

**Files:**
- Modify: `internal/chat/session.go` (`SessionOpts`, `NewSession`)
- Test: `internal/chat/session_seed_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/chat/session_seed_test.go`:

```go
package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestNewSession_SeedsInitialMessages(t *testing.T) {
	seed := []llm.Message{
		{Role: llm.RoleUser, Content: "earlier"},
		{Role: llm.RoleAssistant, Content: "reply"},
	}
	s := NewSession(SessionOpts{InitialMessages: seed})
	if len(s.messages) != 2 || s.messages[0].Content != "earlier" {
		t.Fatalf("session not seeded: %#v", s.messages)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/chat/ -run TestNewSession_SeedsInitialMessages -v`
Expected: FAIL — `InitialMessages` unknown field.

- [ ] **Step 3: Add the field and seed in the constructor**

In `internal/chat/session.go`, add to `SessionOpts`:

```go
	// InitialMessages seeds the conversation when resuming a stored session.
	// Applied verbatim as the starting in-memory history before the first turn.
	InitialMessages []llm.Message
```

And in `NewSession`, after constructing `s`:

```go
func NewSession(opts SessionOpts) *Session {
	s := &Session{id: opts.StoreID, sink: opts.Sink}
	s.reminders.contextWindowFor = opts.ContextWindowFor
	if s.sink == nil {
		s.sink = func(Event) {}
	}
	if len(opts.InitialMessages) > 0 {
		s.messages = append(s.messages, opts.InitialMessages...)
	}
	return s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/chat/ -run TestNewSession_SeedsInitialMessages -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/chat/session.go internal/chat/session_seed_test.go
git commit -m "feat(chat): seed Session from InitialMessages for resume"
```

---

# Phase 2 — CLI split (`shell3` TUI vs `shell3 run`)

Make mode explicit. Root = interactive TUI (+ `--resume`). New `run` subcommand = headless/prompt/subagent.

### Task 2.1: Add the `run` subcommand and unified flags

**Files:**
- Modify: `cmd/shell3/run.go`, `cmd/shell3/main.go`
- Test: `cmd/shell3/run_cli_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `cmd/shell3/run_cli_test.go`:

```go
//go:build unix

package main

import (
	"strings"
	"testing"
)

func TestRunCommand_HasNewFlagsNotOld(t *testing.T) {
	cmd := newRunCommand()
	fs := cmd.Flags()
	for _, name := range []string{"prompt", "resume", "parent-session"} {
		if fs.Lookup(name) == nil {
			t.Errorf("run is missing --%s", name)
		}
	}
	for _, gone := range []string{"append-sinkfile", "no-subagents"} {
		if fs.Lookup(gone) != nil {
			t.Errorf("run still has retired flag --%s", gone)
		}
	}
	if cmd.Use == "" || strings.HasPrefix(cmd.Use, "shell3 ") {
		// `run` is a subcommand; Use should be "run ..." not "shell3 ..."
		t.Errorf("unexpected Use %q", cmd.Use)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/shell3/ -run TestRunCommand_HasNewFlagsNotOld -v`
Expected: FAIL — `--prompt`/`--resume`/`--parent-session` missing; old flags present.

- [ ] **Step 3: Rewrite run.go**

Replace `cmd/shell3/run.go` contents (keeping `//go:build unix`) with:

```go
//go:build unix

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/weatherjean/shell3/internal/tui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

type runFlags struct {
	configPath    string
	outPath       string
	agent         string
	id            string
	prompt        string
	resume        int64
	parentSession int64
}

// newRunCommand builds `shell3 run`: every non-interactive invocation —
// direct prompts, subagent spawns, headless audit runs.
func newRunCommand() *cobra.Command {
	f := &runFlags{}
	cmd := &cobra.Command{
		Use:   "run [message]",
		Short: "Run shell3 headlessly (prompt, subagent, or resume)",
		RunE: func(cmd *cobra.Command, args []string) error {
			input := f.prompt
			if input == "" {
				input = strings.TrimSpace(strings.Join(args, " "))
			}
			if input == "" && !term.IsTerminal(int(os.Stdin.Fd())) {
				b, err := io.ReadAll(os.Stdin)
				if err != nil {
					return fmt.Errorf("read stdin: %w", err)
				}
				input = strings.TrimSpace(string(b))
			}
			return runHeadless(cmd.Context(), f, input)
		},
	}
	cmd.Flags().StringVarP(&f.configPath, "config", "c", "", "Path to shell3.lua (default: ./shell3.lua, else ~/.shell3/shell3.lua)")
	cmd.Flags().StringVar(&f.outPath, "out", "", "Stream a JSONL audit log of this run to <path>.")
	cmd.Flags().StringVar(&f.agent, "agent", "", "Select the active agent by name (default: first declared).")
	cmd.Flags().StringVar(&f.id, "id", "", "Caller-chosen id stamped into completion notifications.")
	cmd.Flags().StringVar(&f.prompt, "prompt", "", "The task/prompt to run (unified input; overrides positional args).")
	cmd.Flags().Int64Var(&f.resume, "resume", 0, "Resume an existing session id, continuing its conversation.")
	cmd.Flags().Int64Var(&f.parentSession, "parent-session", 0, "Session id to report completion to (set by the spawning agent).")
	return cmd
}

func runHeadless(ctx context.Context, f *runFlags, input string) error {
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	_ = os.Setenv("SHELL3_HEADLESS", "1")
	if f.outPath != "" {
		_ = os.Setenv("SHELL3_OUT", f.outPath)
	}
	spec := shell3.Spec{
		Prompt:        input,
		ConfigPath:    f.configPath,
		WorkDir:       cwd,
		Agent:         f.agent,
		Interactive:   false,
		OutPath:       f.outPath,
		ID:            f.id,
		ResumeID:      f.resume,
		ParentSession: f.parentSession,
	}
	return tui.RunOnce(ctx, spec)
}
```

> `shell3.Spec` gains `ResumeID` and `ParentSession` (and loses `NoSubagents`/`AppendSinkFile`) in Phase 3 Task 3.1 / Phase 5. If building this task standalone fails on those fields, do Task 3.1's Spec edit first — they are co-dependent; the subagent executing this plan should treat Tasks 2.1 + 3.1 as one commit if needed.

- [ ] **Step 4: Wire the subcommand and make root interactive-only**

In `cmd/shell3/main.go`, replace the run-command wiring:

```go
	root := &cobra.Command{
		Use:     "shell3",
		Short:   "Minimal Unix-composable coding agent",
		Version: version,
	}

	rootResume := root.Flags().Int64("resume", 0, "Resume a session id in the interactive TUI.")
	rootConfig := root.Flags().StringP("config", "c", "", "Path to shell3.lua")
	rootAgent := root.Flags().String("agent", "", "Select the active agent by name.")
	root.RunE = func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		return tui.RunInteractive(cmd.Context(), shell3.Spec{
			ConfigPath:  *rootConfig,
			WorkDir:     cwd,
			Agent:       *rootAgent,
			Interactive: true,
			ResumeID:    *rootResume,
		})
	}
	root.Args = cobra.ArbitraryArgs
	root.AddCommand(newRunCommand())
	root.AddCommand(newBootCommand())
	root.AddCommand(newTelegramCommand())
```

Add imports `"os"`, `"github.com/weatherjean/shell3/internal/tui"`, `"github.com/weatherjean/shell3/pkg/shell3"` to main.go if not present.

- [ ] **Step 5: Run test + build**

Run: `go test ./cmd/shell3/ -run TestRunCommand_HasNewFlagsNotOld -v && go build ./...`
Expected: PASS (build may require Phase 3 Task 3.1 Spec changes — land them together).

- [ ] **Step 6: Commit**

```bash
git add cmd/shell3/run.go cmd/shell3/main.go cmd/shell3/run_cli_test.go
git commit -m "feat(cli): split shell3 (TUI + --resume) from shell3 run (--prompt/--resume/--parent-session)"
```

---

# Phase 3 — Resume wiring (pkg/shell3 + TUI)

### Task 3.1: Spec fields + resume in newSession/Start

**Files:**
- Modify: `pkg/shell3/shell3.go` (`Spec`, `newSession`, `Start`), `pkg/shell3/runtime.go` (`SessionOpts`)
- Test: `pkg/shell3/resume_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `pkg/shell3/resume_test.go`:

```go
package shell3

import "testing"

func TestSpec_HasResumeAndParentFields(t *testing.T) {
	// Compile-time guard: these fields must exist with these types.
	var s Spec
	s.ResumeID = 7
	s.ParentSession = 3
	if s.ResumeID != 7 || s.ParentSession != 3 {
		t.Fatal("unreachable")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pkg/shell3/ -run TestSpec_HasResumeAndParentFields -v`
Expected: FAIL — unknown fields `ResumeID`, `ParentSession`.

- [ ] **Step 3: Edit Spec and the session-creation path**

In `pkg/shell3/shell3.go`, replace the `Spec` fields `NoSubagents`/`AppendSinkFile`/`ID` block with:

```go
	// ID is a caller-chosen id stamped into this run's completion notification.
	ID string
	// ResumeID, when non-zero, reloads that stored session's messages and
	// continues its conversation instead of starting fresh.
	ResumeID int64
	// ParentSession, when non-zero, is the session id this run reports its
	// completion to (the spawning agent). Persisted as parent_session_id.
	ParentSession int64
```

In `pkg/shell3/runtime.go`, add to `SessionOpts`:

```go
	// ResumeID reloads a stored session's messages when non-zero.
	ResumeID int64
	// ParentSession is the report pointer written to the new session row.
	ParentSession int64
```

Replace `newSession` (`shell3.go:382-408`) so it resumes or starts-with-parent:

```go
func newSession(cfg chat.Config, cleanup func(), opts SessionOpts) *Session {
	var storeID int64
	var seed []llm.Message
	if cfg.Store != nil {
		switch {
		case opts.ResumeID != 0:
			storeID = opts.ResumeID
			if msgs, err := cfg.Store.LoadSessionMessages(opts.ResumeID); err == nil {
				seed = msgs
			} else {
				chat.LogOrNoop(cfg.Log).Warn("resume load failed", "session_id", opts.ResumeID, "error", err)
			}
			if err := cfg.Store.SetLiveness(opts.ResumeID, os.Getpid(), "", "live"); err != nil {
				chat.LogOrNoop(cfg.Log).Warn("resume liveness failed", "error", err)
			}
		case opts.ParentSession != 0:
			if id, err := cfg.Store.StartSessionWithParent(opts.ParentSession); err == nil {
				storeID = id
			} else {
				chat.LogOrNoop(cfg.Log).Warn("start session with parent failed", "error", err)
			}
		default:
			if id, err := cfg.Store.StartSession(); err == nil {
				storeID = id
			} else {
				chat.LogOrNoop(cfg.Log).Warn("start session failed", "error", err)
			}
		}
	}
	s := &Session{
		cfg:         cfg,
		handlers:    chat.NewHandlers(cfg),
		cleanup:     cleanup,
		sinkCleanup: func() {},
	}
	s.sess = chat.NewSession(chat.SessionOpts{
		StoreID:          storeID,
		InitialMessages:  seed,
		ContextWindowFor: func(string) int { return cfg.ContextWindow },
		Sink:             s.route,
	})
	return s
}
```

Update the two `newSession(cfg, ...)` call sites (in `runtime.go`'s `Session` method and any in `shell3.go`) to pass `opts`. In `runtime.go`'s `Session` method, change `s := newSession(cfg, func() {})` to `s := newSession(cfg, func() {}, opts)`. Thread `ResumeID`/`ParentSession` from `Spec` into the `SessionOpts` built in `Start` (`shell3.go:333-365`):

```go
	s, err := rt.Session(SessionOpts{
		Name:             "main",
		Agent:            spec.Agent,
		Headless:         !spec.Interactive,
		ShellInteractive: spec.ShellInteractive,
		ResumeID:         spec.ResumeID,
		ParentSession:    spec.ParentSession,
	})
```

Add `"os"` and the `llm` import (`"github.com/weatherjean/shell3/internal/llm"`) to shell3.go if needed.

- [ ] **Step 4: Run test + build**

Run: `go test ./pkg/shell3/ -run TestSpec_HasResumeAndParentFields -v && go build ./...`
Expected: PASS. (Build now satisfies Phase 2's Spec dependency.)

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/shell3.go pkg/shell3/runtime.go pkg/shell3/resume_test.go
git commit -m "feat(shell3): resume + parent-session wiring in session creation"
```

---

### Task 3.2: End-to-end resume round-trip test

**Files:**
- Test: `pkg/shell3/resume_e2e_test.go` (new) — uses the fake LLM.

- [ ] **Step 1: Write the test**

Create `pkg/shell3/resume_e2e_test.go`. Model it on existing pkg/shell3 tests that build a Runtime with the fake LLM (check `pkg/shell3/sink_test.go` and `internal/llm/fakellm` for the established harness; reuse the same config-loading helper). The test:

1. Runs a headless turn with a fresh session, asserting a session row + messages persisted.
2. Calls a second run with `ResumeID` set to that session id and a new prompt.
3. Asserts the second session's loaded messages include both the original and new turn (i.e. context carried over).

```go
package shell3

import "testing"

func TestResume_CarriesPriorContext(t *testing.T) {
	t.Skip("fill in using the fakellm harness pattern from sink_test.go / fakellm")
	// 1. Start runtime with fakellm + temp store.
	// 2. Run prompt "remember X"; capture session id from store.ListSessions.
	// 3. Start a second session with SessionOpts{ResumeID: id}; run "what is X".
	// 4. Load messages; assert len > 2 and the first user message == "remember X".
}
```

> This is a scaffold with an explicit `t.Skip` so the suite stays green; the executing engineer must replace the skip with the real harness wiring (the fakellm setup is non-trivial and lives in the existing tests). Flag to reviewer that this test must be completed, not left skipped, before Phase 3 is "done."

- [ ] **Step 2: Commit**

```bash
git add pkg/shell3/resume_e2e_test.go
git commit -m "test(shell3): scaffold resume context-carryover e2e (TODO: wire fakellm)"
```

---

### Task 3.3: TUI resume dispatch + tail render

**Files:**
- Modify: `internal/tui/interactive.go` (`RunInteractive`)
- Test: manual + `internal/tui/resume_test.go` (new, light)

- [ ] **Step 1: Write a light test for the banner formatter**

Create `internal/tui/resume_test.go`:

```go
package tui

import (
	"strings"
	"testing"
)

func TestResumeBanner(t *testing.T) {
	got := resumeBanner(42, 17)
	if !strings.Contains(got, "42") || !strings.Contains(got, "17") {
		t.Fatalf("banner missing id/count: %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/tui/ -run TestResumeBanner -v`
Expected: FAIL — `resumeBanner` undefined.

- [ ] **Step 3: Implement banner + dispatch**

In `internal/tui/interactive.go`, add:

```go
// resumeBanner is the marker line shown when resuming a stored conversation in
// the TUI. We deliberately do not re-render the full history (could be huge);
// the banner plus the always-loaded model context is enough for the user to
// continue. Tail-rendering the last few turns is a future enhancement.
func resumeBanner(sessionID int64, numMsgs int) string {
	return fmt.Sprintf("⟲ resuming conversation %d (%d messages)", sessionID, numMsgs)
}
```

In `RunInteractive`, after the session is started and before the input loop, when `spec.ResumeID != 0`, print the banner via the app's render path (use the same mechanism the welcome banner uses — check how `patchapp.New`/the welcome line is emitted and mirror it). Pull the message count from `sess` (or `store.LoadSessionMessages`) — a `len(sess.Snapshot())`-style call if available, else `0`.

> Per the design, tail-render is optional; the banner fallback is acceptable. Keep this task to the banner. If `RunInteractive`'s spec doesn't yet carry `ResumeID` into `shell3.Start`, it already does via Task 3.1 (Start reads `spec.ResumeID`).

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/tui/ -run TestResumeBanner -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui/interactive.go internal/tui/resume_test.go
git commit -m "feat(tui): resume banner + dispatch (tail render deferred)"
```

---

# Phase 4 — Socket transport

Replace the polled sink file with a per-session Unix-domain socket. This phase introduces the transport but does not yet rewire reporting (Phase 5).

### Task 4.1: Move Notification type to internal/notify

**Files:**
- Create: `internal/notify/notify.go`
- Test: `internal/notify/notify_test.go` (new)

- [ ] **Step 1: Create the package by copying the type**

Create `internal/notify/notify.go` with the `Notification` struct and `Kind` constants copied verbatim from `internal/sink/sink.go:30-62` (the `KindBgDone`/`KindAgentDone` consts and the `Notification` struct), under `package notify`. Do **not** copy `Append` (that was file-transport; sockets replace it).

```go
// Package notify defines the cross-process completion notification carried over
// the socket transport and parked in the SQLite inbox.
package notify

const (
	KindBgDone    = "bg_done"
	KindAgentDone = "agent_done"
)

type Notification struct {
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	Status     string `json:"status,omitempty"`
	Exit       *int   `json:"exit,omitempty"`
	Log        string `json:"log,omitempty"`
	Transcript string `json:"transcript,omitempty"`
	Preview    string `json:"preview,omitempty"`
	Cmd        string `json:"cmd,omitempty"`
	TS         string `json:"ts"`
	// Origin is the session id this notification is about (the completing
	// child), so a cascaded delivery can name the true source even when it
	// surfaces several hops up.
	Origin int64 `json:"origin,omitempty"`
}
```

- [ ] **Step 2: Write a marshal round-trip test**

Create `internal/notify/notify_test.go`:

```go
package notify

import (
	"encoding/json"
	"testing"
)

func TestNotification_RoundTrip(t *testing.T) {
	n := Notification{Kind: KindAgentDone, ID: "a1", Preview: "done", Origin: 9}
	b, _ := json.Marshal(n)
	var got Notification
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Origin != 9 || got.Kind != KindAgentDone {
		t.Fatalf("round-trip lost data: %#v", got)
	}
}
```

- [ ] **Step 3: Run test**

Run: `go test ./internal/notify/ -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/notify/
git commit -m "feat(notify): Notification type for socket transport + inbox"
```

---

### Task 4.2: Unix-domain socket listener + client

**Files:**
- Create: `internal/socket/socket.go`
- Test: `internal/socket/socket_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/socket/socket_test.go`:

```go
package socket

import (
	"path/filepath"
	"testing"
	"time"
)

func TestSendReceive(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "s.sock")

	got := make(chan []byte, 1)
	lis, err := Listen(sock, func(line []byte) {
		got <- line
	})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer lis.Close()

	if err := Send(sock, []byte(`{"kind":"agent_done"}`)); err != nil {
		t.Fatalf("send: %v", err)
	}
	select {
	case line := <-got:
		if string(line) != `{"kind":"agent_done"}` {
			t.Fatalf("got %q", line)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for delivery")
	}
}

func TestSend_DeadSocketErrors(t *testing.T) {
	if err := Send(filepath.Join(t.TempDir(), "nope.sock"), []byte("x")); err == nil {
		t.Fatal("expected error sending to nonexistent socket")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/socket/ -v`
Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement socket.go**

Create `internal/socket/socket.go`:

```go
// Package socket provides a minimal line-oriented Unix-domain-socket transport:
// a listener that invokes a handler per newline-delimited message, and a Send
// that connects, writes one message, and closes. A failed Send (ENOENT /
// ECONNREFUSED) doubles as the liveness signal that an endpoint is gone.
package socket

import (
	"bufio"
	"fmt"
	"net"
	"os"
)

// Listener wraps the accept loop so callers can Close to stop it and remove the
// socket file.
type Listener struct {
	l    net.Listener
	path string
}

// Listen creates (replacing any stale file) a Unix-domain socket at path and
// invokes handler for each newline-delimited message received. macOS caps the
// socket path at ~104 bytes — keep path short (a numeric session id).
func Listen(path string, handler func(line []byte)) (*Listener, error) {
	_ = os.Remove(path) // clear a stale socket from an unclean prior exit
	if err := os.MkdirAll(dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("socket: mkdir: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("socket: listen %s: %w", path, err)
	}
	ls := &Listener{l: l, path: path}
	go ls.accept(handler)
	return ls, nil
}

func (ls *Listener) accept(handler func([]byte)) {
	for {
		conn, err := ls.l.Accept()
		if err != nil {
			return // listener closed
		}
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
			for sc.Scan() {
				b := append([]byte(nil), sc.Bytes()...)
				handler(b)
			}
		}(conn)
	}
}

// Close stops the accept loop and removes the socket file.
func (ls *Listener) Close() error {
	err := ls.l.Close()
	_ = os.Remove(ls.path)
	return err
}

// Send connects to a listening socket, writes one newline-terminated message,
// and closes. Returns an error if the socket is absent or refusing — which the
// caller treats as "endpoint dormant".
func Send(path string, msg []byte) error {
	conn, err := net.Dial("unix", path)
	if err != nil {
		return fmt.Errorf("socket: dial %s: %w", path, err)
	}
	defer conn.Close()
	if len(msg) == 0 || msg[len(msg)-1] != '\n' {
		msg = append(msg, '\n')
	}
	if _, err := conn.Write(msg); err != nil {
		return fmt.Errorf("socket: write: %w", err)
	}
	return nil
}

func dir(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' {
			return path[:i]
		}
	}
	return "."
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/socket/ -v`
Expected: PASS (both tests).

- [ ] **Step 5: Commit**

```bash
git add internal/socket/
git commit -m "feat(socket): unix-domain-socket line transport (Listen/Send)"
```

---

# Phase 5 — Pointer reporting + cascade (replace sink watcher)

This is the heart of the rewrite. Create `pkg/shell3/transport.go` to replace `pkg/shell3/sink.go`: each session listens on a socket, registers liveness, and on completion walks its `parent_session_id` to report. `bgjobs` stops injecting the depth env; subagents self-report.

### Task 5.1: Session socket path + liveness registration

**Files:**
- Modify: `pkg/shell3/shell3.go` (Session struct fields), `internal/paths/paths.go` (socket path helper), create `pkg/shell3/transport.go`
- Test: `pkg/shell3/transport_test.go` (new)

- [ ] **Step 1: Add a socket-path helper + failing test**

In `internal/paths/paths.go`, add beside `SinkPath`:

```go
// SockPath returns the per-session Unix-domain socket path. Kept short
// (numeric session id) because macOS caps socket paths at ~104 bytes.
func SockPath(workdir string, sessionID int64) string {
	return filepath.Join(workdir, ".shell3", "sock", fmt.Sprintf("%d.sock", sessionID))
}
```

Add `"fmt"` import if missing. Create `pkg/shell3/transport_test.go`:

```go
package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestSockPath_Short(t *testing.T) {
	p := paths.SockPath("/wd", 7)
	if !strings.HasSuffix(p, "/.shell3/sock/7.sock") {
		t.Fatalf("unexpected sock path %q", p)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestSockPath_Short -v`
Expected: FAIL — `paths.SockPath` undefined.

- [ ] **Step 3: Implement transport.go (listener + registration)**

Create `pkg/shell3/transport.go`:

```go
package shell3

import (
	"encoding/json"
	"os"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/socket"
)

// startTransport opens this session's socket listener and marks it live in the
// store registry. Replaces the old sink watcher. A session with no store id (no
// store configured) or no resolvable workdir skips the transport.
func (s *Session) startTransport(rt *Runtime) {
	id := s.sess.ID()
	if id == 0 || s.cfg.WorkDir == "" || s.cfg.Store == nil {
		return
	}
	sock := paths.SockPath(s.cfg.WorkDir, id)
	lis, err := socket.Listen(sock, func(line []byte) {
		var n notify.Notification
		if err := json.Unmarshal(line, &n); err != nil {
			return
		}
		s.deliverNotification(rt, n)
	})
	if err != nil {
		return
	}
	s.mu.Lock()
	s.listener = lis
	s.mu.Unlock()
	_ = s.cfg.Store.SetLiveness(id, os.Getpid(), sock, "live")
}

// stopTransport closes the listener and marks the session dormant so future
// reporters route to the inbox + revive instead of a dead socket.
func (s *Session) stopTransport() {
	s.mu.Lock()
	lis := s.listener
	s.listener = nil
	s.mu.Unlock()
	id := s.sess.ID()
	if lis != nil {
		_ = lis.Close()
	}
	if id != 0 && s.cfg.Store != nil {
		_ = s.cfg.Store.SetLiveness(id, 0, "", "dormant")
	}
}

// deliverNotification injects a received notification into the running session,
// waking it if idle. Same behavior the sink watcher had.
func (s *Session) deliverNotification(rt *Runtime, n notify.Notification) {
	s.sess.Interject(formatNotification(n))
	if !s.isBusy() {
		rt.emit(HostEvent{Session: s.name, Kind: Wake})
	}
}
```

In `pkg/shell3/shell3.go`, replace the Session struct's sink fields (`sinkStop chan struct{}`, `sinkDone chan struct{}`, `sinkFile string`) with:

```go
	listener *socket.Listener
```

Add the import `"github.com/weatherjean/shell3/internal/socket"`.

> `formatNotification` currently lives in `pkg/shell3/sink.go` and references `sink.Notification`. Move it into `transport.go` now, changing its parameter type to `notify.Notification` (the body is otherwise identical — copy from `sink.go:154-213`, swap `sink.` → `notify.`). It will be deleted from `sink.go` in Phase 7.

- [ ] **Step 4: Swap the watcher call sites**

In `pkg/shell3/runtime.go` `Session` method, replace `s.startSinkWatcher(rt, s.sinkPath())` with `s.startTransport(rt)`. In `pkg/shell3/shell3.go` `doClose`, replace `s.stopSinkWatcher()` with `s.stopTransport()`.

- [ ] **Step 5: Run test + build**

Run: `go test ./pkg/shell3/ -run TestSockPath_Short -v && go build ./...`
Expected: PASS. (Old `pkg/shell3/sink.go` still compiles; it's deleted in Phase 7.)

- [ ] **Step 6: Commit**

```bash
git add internal/paths/paths.go pkg/shell3/transport.go pkg/shell3/shell3.go pkg/shell3/runtime.go pkg/shell3/transport_test.go
git commit -m "feat(shell3): socket transport + liveness registration (replaces sink watcher)"
```

---

### Task 5.2: Report-at-completion walk (live → socket, dormant → inbox+revive)

**Files:**
- Modify: `pkg/shell3/transport.go` (add `report`), `pkg/shell3/shell3.go` (`doClose` calls report before stopTransport)
- Test: `pkg/shell3/report_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `pkg/shell3/report_test.go`. It exercises the pure routing decision via a small helper `routeReport` that returns an enum so we can test without spawning processes:

```go
package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestRouteReport_LiveParentGetsSocket(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession()
	_ = st.SetLiveness(parent, 123, "/tmp/p.sock", "live")

	route, sock := routeReport(st, parent)
	if route != routeSocket || sock != "/tmp/p.sock" {
		t.Fatalf("got route=%v sock=%q; want socket /tmp/p.sock", route, sock)
	}
}

func TestRouteReport_DormantParentGetsInboxRevive(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession()
	_ = st.SetLiveness(parent, 0, "", "dormant")

	route, _ := routeReport(st, parent)
	if route != routeRevive {
		t.Fatalf("got route=%v; want revive", route)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRouteReport -v`
Expected: FAIL — `routeReport`/`routeSocket`/`routeRevive` undefined.

- [ ] **Step 3: Implement the report walk**

Add to `pkg/shell3/transport.go`:

```go
type reportRoute int

const (
	routeNone   reportRoute = iota // no parent (root) or not found
	routeSocket                    // parent live → push over its socket
	routeRevive                    // parent dormant → inbox + revive
)

// routeReport decides how to deliver a completion to parentID based on its
// current liveness. Pure decision (no I/O side effects) so it is unit-testable.
func routeReport(st *store.Store, parentID int64) (reportRoute, string) {
	if parentID == 0 {
		return routeNone, ""
	}
	_, sock, status, err := st.Liveness(parentID)
	if err != nil {
		return routeRevive, "" // treat unknown as dormant; revive is the safe path
	}
	if status == "live" && sock != "" {
		return routeSocket, sock
	}
	return routeRevive, ""
}

// report delivers this session's completion notification to its parent: live
// parent → socket; dormant parent → SQLite inbox + revive (one winner).
// Revive failure falls back to escalating one hop up so the human at root still
// learns of it. Called once during Close, before stopTransport marks us dormant.
func (s *Session) report(n notify.Notification) {
	st := s.cfg.Store
	if st == nil {
		return
	}
	parentID, err := st.ParentSessionID(s.sess.ID())
	if err != nil || parentID == 0 {
		return
	}
	s.reportTo(st, parentID, n)
}

func (s *Session) reportTo(st *store.Store, parentID int64, n notify.Notification) {
	if n.Origin == 0 {
		n.Origin = s.sess.ID()
	}
	route, sock := routeReport(st, parentID)
	switch route {
	case routeSocket:
		payload, _ := json.Marshal(n)
		if err := socket.Send(sock, payload); err == nil {
			return
		}
		// Socket vanished between the liveness read and the send: fall through
		// to revive as if dormant.
		fallthrough
	case routeRevive:
		payload, _ := json.Marshal(n)
		_ = st.AppendInbox(parentID, payload)
		won, err := st.ClaimRevive(parentID)
		if err == nil && won {
			if spawnErr := s.spawnRevive(st, parentID); spawnErr == nil {
				return
			}
			// Revive spawn failed: release the claim and escalate one hop so the
			// result is not black-holed.
			_ = st.SetLiveness(parentID, 0, "", "dormant")
		}
		// Either we lost the claim (another reviver owns it) or revive failed.
		// If revive failed, escalate to grandparent so root still hears it.
		if err == nil && !won {
			return // winner will deliver; our inbox append is enough
		}
		if grand, gerr := st.ParentSessionID(parentID); gerr == nil && grand != 0 {
			s.reportTo(st, grand, n)
		}
	}
}
```

> `spawnRevive` is implemented in Phase 6 Task 6.1. For this task, add a temporary stub so the package compiles and the routing tests pass:

```go
// spawnRevive is implemented in Phase 6. Temporary stub.
func (s *Session) spawnRevive(st *store.Store, parentID int64) error { return nil }
```

Wire `report` into `doClose` (`shell3.go`): immediately before `s.stopTransport()`, build the notification and report it:

```go
	s.report(notify.Notification{
		Kind:    notify.KindAgentDone,
		ID:      s.opts.id(), // see note
		Status:  s.completionStatus(),
		Preview: s.completionPreview(),
	})
	s.stopTransport()
```

> For `s.opts.id()`, `s.completionStatus()`, `s.completionPreview()`: the simplest correct version reads the caller-chosen id and the last assistant message. If those helpers don't exist, inline: id from `spec.ID` (thread it onto the Session in `Start` as `s.reportID = spec.ID`), status `"ok"`/`"error"` from `s.sawError`, preview from the last assistant message text (≤200 runes) — mirror what `once.go:selfReport` built. Keep it minimal; the executing engineer should reuse `selfReport`'s preview logic (`once.go:82-100`) before that file is simplified in Phase 7.

- [ ] **Step 4: Run test + build**

Run: `go test ./pkg/shell3/ -run TestRouteReport -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/transport.go pkg/shell3/shell3.go pkg/shell3/report_test.go
git commit -m "feat(shell3): report-at-completion walk over parent pointer (socket/inbox+revive)"
```

---

### Task 5.3: Subagent spawn template + drop the depth env

**Files:**
- Modify: `pkg/shell3/delegation.go` (`renderDelegation`, remove `DisableSubagents`/env suppression), `internal/bgjobs/bgjobs.go` (drop `SHELL3_NO_SUBAGENTS=1`), `pkg/shell3/runtime.go`/`shell3.go` (drop `DisableSubagents`)
- Test: `pkg/shell3/delegation_test.go` (update)

- [ ] **Step 1: Update the delegation test for the new template**

In `pkg/shell3/delegation_test.go`, replace assertions about `--append-sinkfile`/`--no-subagents` with the new command shape. The spawn template must now read:

```
shell3 run --config <cfg> --agent <name> --out .shell3/agents/<id>.jsonl --parent-session <my-session-id> --id <id> --prompt "<task>"
```

Write/replace a test:

```go
func TestRenderDelegation_NewTemplate(t *testing.T) {
	out := renderDelegation(delegationParams{
		Binary:        "shell3",
		ConfigPath:    "/c/shell3.lua",
		WorkDir:       "/wd",
		ParentSession: 42,
		Subagents:     []subagentItem{{Name: "explore", Description: "search"}},
	})
	if !strings.Contains(out, "shell3 run ") {
		t.Errorf("expected `shell3 run` subcommand:\n%s", out)
	}
	if !strings.Contains(out, "--parent-session 42") {
		t.Errorf("expected --parent-session 42:\n%s", out)
	}
	if strings.Contains(out, "--append-sinkfile") || strings.Contains(out, "--no-subagents") {
		t.Errorf("retired flags must be gone:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRenderDelegation_NewTemplate -v`
Expected: FAIL — `delegationParams` has `SinkPath`, not `ParentSession`; template still old.

- [ ] **Step 3: Rewrite delegation rendering**

In `pkg/shell3/delegation.go`:

- Change `delegationParams`: replace `SinkPath string` with `ParentSession int64`.
- In `delegationSection`, remove the `s.opts.DisableSubagents || os.Getenv(noSubagentsEnv) == "1"` guard (delete the whole early-return condition referencing those), remove `sink := s.sinkPath()` and its empty-check, and pass `ParentSession: s.sess.ID()`:

```go
func (s *Session) delegationSection(rt *Runtime) string {
	if rt == nil {
		return ""
	}
	allowed := s.cfg.Subagents
	if len(allowed) == 0 {
		return ""
	}
	cfgPath, err := rt.ConfigPath()
	if err != nil || cfgPath == "" {
		return ""
	}
	return renderDelegation(delegationParams{
		Binary:        shell3Binary(),
		ConfigPath:    cfgPath,
		WorkDir:       s.cfg.WorkDir,
		ParentSession: s.sess.ID(),
		Subagents:     s.subagentList(rt, allowed),
	})
}
```

- Delete the `noSubagentsEnv` const.
- In `renderDelegation`, replace the `fmt.Fprintf` spawn-command line with:

```go
	fmt.Fprintf(&b, "  %s run --config %s --agent <name> --out %s --parent-session %d --id <id> --prompt \"<task>\"\n\n",
		p.Binary, p.ConfigPath, transcript, p.ParentSession)
```

- Update the surrounding prose line that mentions `notify_on_exit=false` to keep it (still valid — the subagent self-reports), but it now reports via the socket/inbox transport automatically.

In `internal/bgjobs/bgjobs.go`, change the env line (`bgjobs.go:115`) from:

```go
	c.Env = append(os.Environ(), "SHELL3_NO_SUBAGENTS=1")
```

to:

```go
	c.Env = append([]string(nil), os.Environ()...)
```

In `pkg/shell3/runtime.go` `SessionOpts`, delete the `DisableSubagents bool` field. In `pkg/shell3/shell3.go` `Start`, delete `DisableSubagents: spec.NoSubagents`. Remove the now-unused `opts.DisableSubagents` references.

- [ ] **Step 4: Run test + build**

Run: `go test ./pkg/shell3/ -run TestRenderDelegation_NewTemplate -v && go build ./...`
Expected: PASS. Other delegation tests referencing old flags will fail to compile — update or delete them now (they're superseded); the Phase 7 cleanup agent will catch any stragglers, but fix compile breaks here.

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/delegation.go internal/bgjobs/bgjobs.go pkg/shell3/runtime.go pkg/shell3/shell3.go pkg/shell3/delegation_test.go
git commit -m "feat(shell3): shell3-run spawn template + drop depth-1 env gate (multi-level delegation)"
```

---

# Phase 6 — Revive a dormant parent

### Task 6.1: spawnRevive — relaunch the parent with drained inbox

**Files:**
- Modify: `pkg/shell3/transport.go` (replace the `spawnRevive` stub)
- Test: `pkg/shell3/revive_test.go` (new) — tests inbox drain → prompt assembly, not the actual exec.

- [ ] **Step 1: Write the failing test for prompt assembly**

Create `pkg/shell3/revive_test.go`:

```go
package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/store"
)

func TestRevivePrompt_SummarizesDrainedInbox(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	parent, _ := st.StartSession()
	_ = st.AppendInbox(parent, mustJSON(notify.Notification{Kind: notify.KindAgentDone, ID: "explore1", Preview: "found 3 files"}))
	_ = st.AppendInbox(parent, mustJSON(notify.Notification{Kind: notify.KindAgentDone, ID: "explore2", Preview: "all green"}))

	prompt, err := revivePrompt(st, parent)
	if err != nil {
		t.Fatalf("revivePrompt: %v", err)
	}
	if !strings.Contains(prompt, "explore1") || !strings.Contains(prompt, "found 3 files") ||
		!strings.Contains(prompt, "explore2") || !strings.Contains(prompt, "all green") {
		t.Fatalf("prompt missing drained notifications:\n%s", prompt)
	}
}
```

Add a tiny helper at the bottom of the test file:

```go
func mustJSON(n notify.Notification) []byte {
	b, _ := jsonMarshal(n)
	return b
}
```

> Use the package's existing JSON import; if none is exported as `jsonMarshal`, replace `mustJSON` with an inline `encoding/json` marshal in the test.

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./pkg/shell3/ -run TestRevivePrompt -v`
Expected: FAIL — `revivePrompt` undefined.

- [ ] **Step 3: Implement revivePrompt + spawnRevive**

Replace the `spawnRevive` stub in `pkg/shell3/transport.go` with:

```go
// revivePrompt drains a dormant parent's inbox and renders the combined
// notifications into a single wake prompt. Draining here (in the spawner) is
// safe because ClaimRevive already elected a single winner; any notifications
// that arrive after this drain land in the inbox and are picked up because the
// revived process drains again on boot (newSession resume path). To be fully
// race-free we drain on the spawned side; this function is used for the
// prompt only when the inbox is drained at boot — see spawnRevive.
func revivePrompt(st *store.Store, parentID int64) (string, error) {
	payloads, err := st.DrainInbox(parentID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\nYou were resumed to handle completed delegated work. The following subagent results arrived while you were idle:\n</system-reminder>\n\n")
	for _, p := range payloads {
		var n notify.Notification
		if err := json.Unmarshal(p, &n); err != nil {
			continue
		}
		b.WriteString(formatNotification(n))
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// spawnRevive relaunches the dormant parent as a background `shell3 run
// --resume <parentID>` whose prompt is the drained inbox. The revived process
// loads the parent's full message history (newSession resume path), processes
// the results, and on its own completion reports up ITS parent pointer —
// continuing the cascade toward root.
func (s *Session) spawnRevive(st *store.Store, parentID int64) error {
	prompt, err := revivePrompt(st, parentID)
	if err != nil {
		return err
	}
	bin := shell3Binary()
	cfgPath := ""
	if s.runtime != nil {
		if p, e := s.runtime.ConfigPath(); e == nil {
			cfgPath = p
		}
	}
	argv := []string{
		bin, "run",
		"--resume", fmt.Sprintf("%d", parentID),
		"--prompt", prompt,
	}
	if cfgPath != "" {
		argv = append(argv, "--config", cfgPath)
	}
	_, err = bgjobs.Start(argv, "revive session "+fmt.Sprintf("%d", parentID),
		s.cfg.WorkDir, nil, "", false)
	return err
}
```

Add imports to `transport.go`: `"fmt"`, `"strings"`, `"github.com/weatherjean/shell3/internal/bgjobs"`, `"github.com/weatherjean/shell3/internal/store"`.

> Note the resume path in `newSession` (Task 3.1) loads messages but does NOT drain the inbox; the inbox is drained here in the spawner via `revivePrompt`, and the drained text is passed as the `--prompt`. This keeps a single drain point. (If you prefer drain-on-boot for the startup-window guarantee in the design, move the `DrainInbox` call into `newSession`'s resume branch and append its rendered text to `seed` as a trailing user message; either is acceptable — pick one and note it. Default: drain in spawner as written.)

- [ ] **Step 4: Run test + build**

Run: `go test ./pkg/shell3/ -run TestRevivePrompt -v && go build ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pkg/shell3/transport.go pkg/shell3/revive_test.go
git commit -m "feat(shell3): revive dormant parent via shell3 run --resume with drained inbox"
```

---

### Task 6.2: Integration test — dormant parent revived by child completion

**Files:**
- Test: `test/delegation_e2e_test.go` (new) — full-binary integration.

- [ ] **Step 1: Write the integration test**

Model it on the existing `test/cli_e2e_test.go` harness (which builds the binary and runs it). The scenario:

1. Build `shell3` (the test helper likely already does — reuse it).
2. Start a session A (parent) headless that "delegates" then exits (use a fakellm-backed config, or a real config with a scripted agent — reuse whatever `cli_e2e_test.go` uses).
3. Spawn child B with `--parent-session <A>`; B finishes after A has exited.
4. Assert that A is revived: a new session row continuing A's conversation appears, OR A's inbox is drained and a revive process ran (assert via store state: A's `status` returns to `live` then `dormant`, and B's notification is no longer in A's inbox).

```go
//go:build unix

package test

import "testing"

func TestDelegation_DormantParentRevived(t *testing.T) {
	t.Skip("fill in using the cli_e2e_test.go binary-build harness")
	// See plan Phase 6 Task 6.2 for the scenario steps.
}
```

> Scaffolded with `t.Skip` (the e2e harness is involved). The executing engineer MUST complete this before declaring Phase 6 done — flag to reviewer. This is the single most important behavioral test in the plan; do not leave it skipped.

- [ ] **Step 2: Commit**

```bash
git add test/delegation_e2e_test.go
git commit -m "test: scaffold dormant-parent-revival e2e (TODO: wire harness)"
```

---

# Phase 7 — Cleanup sweep (dispatched agent)

The dead code is now isolated: `internal/sink/`, `pkg/shell3/sink.go`, the old flags/tests, scaffold `shell3.lua` and docs referencing the old spawn template. This phase is executed by a **dispatched cleanup subagent** because it is a wide, mechanical sweep over the 138-reference blast radius — well-suited to a focused agent with the grep list.

### Task 7.1: Dispatch the cleanup agent

- [ ] **Step 1: Dispatch a general-purpose cleanup subagent** with this prompt:

> In /Users/weatherjean/CODE/AGENTS/shell3 we have replaced the sink-file mechanism with a socket + SQLite transport (see git log on branch feat/bash-first). Perform a CLEAN-BREAK removal of all dead sink/depth-gate code. Do all of the following, compiling after each deletion (`go build ./...`):
> 1. Delete the file `internal/sink/sink.go` and the whole `internal/sink/` package. Update every importer to use `internal/notify` instead (the `Notification` type + `KindBgDone`/`KindAgentDone` moved there). Importers to fix: `internal/bgjobs/bgjobs.go`, any test files.
> 2. Delete `pkg/shell3/sink.go` entirely (its `formatNotification` moved to `pkg/shell3/transport.go`; its watcher methods are obsolete). Remove `pkg/shell3/sink_test.go` or rewrite its cases against the socket transport.
> 3. Remove every remaining reference to: `--append-sinkfile`, `AppendSinkFile`, `appendSinkFile`, `--no-subagents`, `NoSubagents`, `noSubagents`, `SHELL3_NO_SUBAGENTS`, `noSubagentsEnv`, `DisableSubagents`, `sinkPath`, `SinkPath`, `paths.SinkPath`, `startSinkWatcher`, `stopSinkWatcher`, `tailSink`, `drainSink`. Grep the whole repo; fix or delete each hit. The CLI flags and Spec fields are already gone in cmd/ and pkg/shell3 — verify none linger in tests (`internal/tui/once_test.go`, `pkg/shell3/delegation_test.go`, `internal/bgjobs/*_test.go`, `internal/agentsetup/agentsetup_test.go`, `test/cli_e2e_test.go`).
> 4. Remove `chat.TurnConfig.SinkPath` and `chat.ToolConfig.SinkPath` and the threading in `internal/chat/turn.go`, `internal/chat/toolhandler.go`, `internal/chat/tools.go`, `internal/chat/handler_bash_bg.go` — bash_bg's plain-job reaper now passes an empty sink path (best-effort, unchanged behavior for non-subagent jobs) OR is updated to the notify type; keep `bgjobs.Start`'s signature but drop the now-unused sink-file delivery if nothing uses it. Preserve plain bg-job completion logging behavior; only remove the sink-FILE coupling.
> 5. Delete `paths.SinkPath` from `internal/paths/paths.go` (keep `SockPath`).
> 6. Simplify `internal/tui/once.go`: remove `selfReport` and the `internal/sink` import — completion reporting now happens inside `pkg/shell3` (Session.report in doClose), so `RunOnce` should NOT self-report to a sink file. Keep the event-draining loop and error handling.
> 7. Update the scaffold config `internal/scaffold/` (the embedded `shell3.lua` and any `.env`/templates) and ALL docs under `docs/` (cookbook, spec drafts, `pkg/shell3` doc comments in `shell3.go:78-88`) that show the OLD subagent spawn command. The NEW spawn command is: `shell3 run --config <cfg> --agent <name> --out .shell3/agents/<id>.jsonl --parent-session <id> --id <id> --prompt "<task>"`. Replace every occurrence of the old `--append-sinkfile ... --no-subagents` template.
> 8. After all deletions, run `go build ./...` and `go vet ./...` and report any remaining errors. Do NOT run `go test` (the plan's verify phase does that). Report a summary of every file changed/deleted and any reference you could not cleanly remove.

- [ ] **Step 2: Review the cleanup agent's report**; fix anything it flagged as un-removable.

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "refactor: delete sink mechanism + depth-1 gate (clean break to socket/sqlite transport)"
```

---

# Phase 8 — Verification (make + full test suite)

### Task 8.1: Build and test with make

- [ ] **Step 1: Build**

Run: `make build`
Expected: clean build, binary produced. If it fails, fix compile errors before proceeding.

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: all packages PASS. Pay special attention to:
- `internal/store/` (Phase 0)
- `internal/chat/` (Phase 1)
- `internal/socket/`, `internal/notify/` (Phase 4)
- `pkg/shell3/` (Phases 3, 5, 6)
- `test/` (the e2e — these must be the *completed* versions, not `t.Skip` stubs).

If any `t.Skip` scaffolds from Tasks 3.2 / 6.2 remain, they MUST be completed now (wire the fakellm / binary-build harness). A skipped delegation-revival test means the core feature is unverified.

- [ ] **Step 3: Manual smoke test**

Run (in a scratch dir with a configured `shell3.lua` + `.env`):

```bash
make install
shell3 run --prompt "echo hello via bash" --agent <agent>     # headless prompt works
shell3 run --prompt "delegate a trivial task to <subagent>"    # spawns shell3 run child; revival on completion
shell3 --resume <session-id-from-above>                        # TUI resumes, banner shows
```

Expected: headless prompt completes; delegated child's result surfaces back (live or via revival); resume shows the banner and continues context.

- [ ] **Step 4: Final commit**

```bash
git add -A
git commit -m "test: complete e2e harness wiring; full suite green for durable delegation"
```

---

## Self-Review (completed by plan author)

**Spec coverage:**
- §1 CLI surface → Phase 2 (Tasks 2.1) + root TUI in main.go. ✓
- §2 Replayable persistence → Phase 0 (0.1–0.3) + Phase 1 (1.1–1.3); compaction fidelity → Task 1.2. ✓
- §3 Socket transport → Phase 4 (4.2) + Phase 5 (5.1). ✓
- §4 Pointer + cascade → Phase 5 (5.2, 5.3); orphan/self-report → 5.3 + bgjobs env drop. ✓
- §5 Resume/reactivation + inbox/claim → Phase 0 (0.3) + Phase 6 (6.1, 6.2); revive-fallback-escalate → Task 5.2 `reportTo`. ✓
- §6 TUI resume rendering → Task 3.3 (banner; tail render explicitly deferred per spec). ✓
- §7 Testing & migration → tests throughout; Phase 8 verify; no-migration clean break → Phase 7. ✓

**Open risks flagged for the executor (not placeholders — real work to finish):**
- Tasks 3.2 and 6.2 ship as `t.Skip` scaffolds because the fakellm / binary-build harnesses are non-trivial and already exist in the repo; they MUST be completed (Phase 8 Step 2 enforces this). They are the behavioral acceptance tests.
- Task 5.2's report helpers (`completionStatus`/`completionPreview`/report id) reuse `once.go:selfReport` logic — capture it before Phase 7 simplifies that file.
- Tasks 2.1 + 3.1 are co-dependent on the `Spec` shape; land them together if a standalone build of 2.1 fails.

**Type consistency:** `Notification` (notify pkg), `routeReport`→`reportRoute`/`routeSocket`/`routeRevive`, `AppendMessage`/`LoadSessionMessages`/`DeleteMessagesFrom`, `StartSessionWithParent`/`ParentSessionID`/`SetLiveness`/`Liveness`/`ClaimRevive`/`AppendInbox`/`DrainInbox`, `startTransport`/`stopTransport`/`report`/`reportTo`/`spawnRevive`/`revivePrompt`, `Spec.ResumeID`/`Spec.ParentSession`, `SessionOpts.ResumeID`/`SessionOpts.ParentSession`/`InitialMessages` — names used consistently across tasks.
