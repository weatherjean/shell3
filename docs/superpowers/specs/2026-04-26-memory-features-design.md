# Memory Features Redesign

**Date:** 2026-04-26
**Status:** Draft

## Goals

- Make the SQLite-backed memory database a primary, well-shaped feature.
- Give the model a way to navigate long conversation history without flooding context.
- Surface "core" facts to the model automatically without requiring a search.
- Reduce tool surface area: fewer, more orthogonal tools.

## Non-Goals

- Vector search / embeddings.
- Cross-project memory sharing.
- Memory/history GC or compression.

## Tool Surface

Reduces 6 store tools → 3.

### `memory_upsert(key, value, core?: bool)`

- Insert or update a memory entry.
- Empty `value` (`""`) deletes the entry.
- `core` semantics:
  - On insert: `core` defaults to `false` if not provided.
  - On update: omitted `core` preserves existing value; explicit `true`/`false` updates.
- Implementation note: distinguish unset vs `false` via `*bool` in Go.

### `memory_query(query?: string, core_only?: bool, limit?: int)`

- `query` set → FTS5 search ranked by BM25.
- `query` unset → list newest-first.
- `core_only=true` filters to core entries.
- `limit` defaults to 50.
- Output rows: `{key, value, core, updated_at}`.

### `history_query(query?: string, session_id?: int, chunk?: int, limit?: int)`

Two modes, single output shape (array of turns + metadata):

**Search mode** (`query` set):
- FTS5 search across history content, ranked by BM25.
- Each hit carries its `session_id` and `chunk` index so the model can fetch surrounding context.
- Metadata block: `{mode: "search", total_hits}`.

**Get mode** (`query` unset):
- Defaults: `session_id` = id of latest **completed** session (i.e. the most recent session whose `ended_at` is non-null), `chunk` = 0.
- Returns chunk of up to 25 turns, ordered oldest → newest within session.
- Metadata block: `{mode: "get", session_id, chunk, total_chunks, prev_session_id, next_session_id, started_at, ended_at}`.
- `prev_session_id` / `next_session_id` are computed by ordering completed sessions by id. `next_session_id` of the latest completed session points to the current in-progress session id.
- The current in-progress session is intentionally not returned by default (its content already lives in the live prompt).

### Tools removed

`memory_store`, `memory_list`, `memory_search`, `memory_remove`, `history_latest`, `history_search`.

## Schema Changes

### `memories` — add `core` column

The `memories` FTS5 virtual table gains an UNINDEXED `core` column. Because FTS5 does not support `ALTER TABLE ADD COLUMN`, the migration rebuilds the table:

```sql
BEGIN;
CREATE VIRTUAL TABLE memories_new USING fts5(
    key,
    value,
    core       UNINDEXED,
    updated_at UNINDEXED
);
INSERT INTO memories_new(rowid, key, value, core, updated_at)
    SELECT rowid, key, value, 0, updated_at FROM memories;
DROP TABLE memories;
ALTER TABLE memories_new RENAME TO memories;
COMMIT;
```

Migration runs idempotently: detect missing `core` column via `PRAGMA table_info(memories)` before rebuilding.

### `history` and `sessions` — unchanged

Chunk math is computed at query time:
- Total turns for a session: `SELECT COUNT(*) FROM history WHERE session_id = ?`.
- Total chunks: `ceil(total_turns / 25)`.
- Chunk fetch: `WHERE session_id = ? ORDER BY created_at LIMIT 25 OFFSET chunk*25`.

`prev_session_id` / `next_session_id`:
```sql
SELECT id FROM sessions
WHERE ended_at IS NOT NULL AND id < ?
ORDER BY id DESC LIMIT 1;  -- prev
SELECT id FROM sessions
WHERE id > ? ORDER BY id ASC LIMIT 1;  -- next (may be in-progress)
```

## Core Memory Injection

Core memories are loaded into the persona template at session start.

### `persona.TemplateData` adds:

```go
type TemplateData struct {
    Skills       string
    Time         string
    CWD          string
    Model        string
    CoreMemories []store.MemoryEntry  // new
}
```

`CoreMemories` is populated by `cmd/shell3/run.go` from `store.MemoryQuery(coreOnly: true)` and passed to `persona.Load`.

### Default persona block

`base.md` and the assistant persona gain:

```
{{- if .CoreMemories}}

## Core memories

{{range .CoreMemories}}- {{.Key}}: {{.Value}}
{{end}}
{{- end}}
```

Position: after the working-directory header, before `## Tools`. This keeps it stable for prompt caching.

### Size warning

At session start, after loading core memories, if their total serialized byte count exceeds **2048 bytes**, print a warning to the **user terminal** (not the model):

```
warning: core memories total NNNN bytes (>2KB), consider demoting some
```

No hard cap. Model and user can self-manage.

## `MemoryEntry` shape

```go
type MemoryEntry struct {
    Key       string
    Value     string
    Core      bool
    UpdatedAt time.Time
}
```

## Tool Description Notes

`history_query` tool description must explicitly state:

- "Default returns the most recent **completed** session, not the current one."
- "Use `next_session_id` / `prev_session_id` from a get response to walk the chain."
- "Use `chunk` to page within a long session; `total_chunks` tells you the range."

`memory_upsert` description must state:

- "Pass empty `value` to delete an entry."
- "Omit `core` when updating to preserve its current setting."

## Risks

1. **FTS5 rebuild migration** — on large existing memory stores this rewrites all rows. Acceptable; memories are small.
2. **Empty-string delete** — accidental delete via empty value. Mitigation: tool description is explicit; model should rarely pass empty strings unintentionally.
3. **`*bool` JSON unmarshalling** — `nil` vs `false` must be distinguished. Use `*bool` in Go arg structs and check for `nil`.
4. **Chunk size of 25** — picked by feel. May need tuning. Lives as a single named constant.

## Out of Scope (Future)

- Memory expiration / TTL.
- Memory categories beyond `core`.
- Storing chunked history materialized for faster reads.
- Per-persona memory namespaces.
