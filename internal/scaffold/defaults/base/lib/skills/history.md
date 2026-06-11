---
name: history
description: Read and full-text-search past conversations from the shell3 SQLite store, read-only, via bash + sqlite3.
---

# History — read past conversations from SQLite

shell3 persists every conversation turn to a SQLite database. You read it
yourself with `bash` and the `sqlite3` CLI. There is no history tool.

## Where the DB is

The absolute database path is in the `## Environment` section of your system
prompt as `history_db`. Always open it READ-ONLY:

    sqlite3 'file:<history_db>?mode=ro' "<SQL>"

The `?mode=ro` URI opens the file read-only — you physically cannot write. Never
open it read-write, never `UPDATE`/`INSERT`/`DELETE`/`DROP`, never touch the
`-wal`/`-shm` sidecar files. The live session is writing to this same DB; your
reads are lock-free and safe only because they are read-only.

## Schema (exact)

Two relations:

`sessions` — one row per conversation:
- `id` INTEGER PRIMARY KEY        — session id (monotonic; higher = newer)
- `started_at` TEXT               — RFC3339 UTC, e.g. 2026-06-11T14:03:22Z
- `ended_at` TEXT                 — RFC3339 UTC, or NULL while in progress
- `summary` TEXT                  — optional compaction summary, or NULL

`history` — one row per turn, an FTS5 full-text table (`content` is indexed;
the rest are UNINDEXED columns, plus the implicit `rowid` for ordering):
- `content` TEXT                  — the message text (full-text searchable)
- `session_id`                    — the owning `sessions.id` (stored as text;
                                    CAST to INTEGER when joining/filtering)
- `role` TEXT                     — 'user' | 'assistant' | 'tool' | 'system'
- `created_at` TEXT               — RFC3339 UTC

Turn order within a session is `rowid ASC` (oldest first).

## Canonical queries

Most-recent COMPLETED session, in order:

    sqlite3 'file:<history_db>?mode=ro' "
      SELECT created_at, role, content FROM history
      WHERE CAST(session_id AS INTEGER) = (
        SELECT id FROM sessions WHERE ended_at IS NOT NULL
        ORDER BY id DESC LIMIT 1)
      ORDER BY rowid ASC LIMIT 100;"

List recent sessions with a preview of their first user message:

    sqlite3 'file:<history_db>?mode=ro' "
      SELECT s.id, s.started_at,
        (SELECT h.content FROM history h
         WHERE CAST(h.session_id AS INTEGER) = s.id AND h.role='user'
         ORDER BY h.rowid ASC LIMIT 1) AS first_user_msg
      FROM sessions s ORDER BY s.id DESC LIMIT 20;"

Full-text search across ALL sessions (FTS5 `MATCH`; rank orders best-first):

    sqlite3 'file:<history_db>?mode=ro' "
      SELECT CAST(session_id AS INTEGER) AS session, created_at, role,
             snippet(history, 0, '[', ']', '…', 12) AS hit
      FROM history WHERE history MATCH 'JWT OR expiry'
      ORDER BY rank LIMIT 20;"

FTS5 query tips: space-separated terms are AND by default; use `OR` for broad
recall; quote a phrase with double-quotes (`"context window"`); a trailing `*`
is a prefix match (`compact*`). Then re-read a hit's full session with the
first query, swapping in its `session` id.

## Rules

- READ-ONLY, always. `?mode=ro`. Never mutate the DB.
- Pull only what you need (`LIMIT`); sessions can be large.
- Cite what you find by session id + timestamp so the user can follow up.
