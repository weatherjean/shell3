---
name: history
description: Search and read past conversations from the shell3 SQLite store via shell3 fts / shell3 list-projects commands (read-only).
---

# History — search and read past conversations

shell3 persists every conversation turn to a single SQLite database, shared
across ALL projects (namespaced by `project_uuid`). Use the first-class
commands below — no need to hand-write SQL for search.

## Searching history

    shell3 fts "JWT OR expiry" --project-id <project_uuid>   # this project
    shell3 fts "context window"                              # ALL projects
    shell3 fts "compact*" --page 1                           # next page

Your `project_uuid` is in the `## Environment` section of your system prompt.
Omit `--project-id` to search across every project in the DB.
Output columns: session-id, timestamp, role, snippet.

FTS5 query tips: space-separated terms are AND by default; use `OR` for broad
recall; quote a phrase with double-quotes (`"context window"`); a trailing `*`
is a prefix match (`compact*`). Re-read a hit's full session with the raw replay
query below, using its session-id.

## Listing projects

    shell3 list-projects            # distinct projects, newest-active first
    shell3 list-projects --page 1

Output columns: uuid, workdir, session count, last activity.

## Advanced: raw replay

To read a full past session in chronological order, use `history_db` from
`## Environment` — always open READ-ONLY:

    sqlite3 'file:<history_db>?mode=ro' "<SQL>"

Both `sessions` and `history` carry a `project_uuid` column, so raw queries
can scope to a single project with `AND project_uuid = '<uuid>'`.

Most-recent completed session in this project, in order:

    sqlite3 'file:<history_db>?mode=ro' "
      SELECT created_at, role, content FROM history
      WHERE history.project_uuid = '<project_uuid>'
        AND CAST(session_id AS INTEGER) = (
          SELECT id FROM sessions
          WHERE ended_at IS NOT NULL AND project_uuid = '<project_uuid>'
          ORDER BY id DESC LIMIT 1)
      ORDER BY rowid ASC LIMIT 100;"

Never open read-write; never `UPDATE`/`INSERT`/`DELETE`/`DROP`; never touch
the `-wal`/`-shm` sidecar files.

## Rules

- READ-ONLY, always. `?mode=ro` for raw queries; commands are already safe.
- Pull only what you need (`LIMIT` / `--page`); sessions can be large.
- Cite what you find by session id + timestamp so the user can follow up.
