---
name: history
description: Search and read past conversations and list background jobs via the shell3 fts / list-projects / list-sessions / read-session / jobs commands (read-only).
---

# History — search and read past conversations

shell3 persists every conversation turn to a single SQLite database, shared
across ALL projects (namespaced by `project_uuid`). Use the first-class
commands below.

## Searching history

    shell3 fts "JWT OR expiry" --project-id <project_uuid>   # this project
    shell3 fts "context window"                              # ALL projects
    shell3 fts "compact*" --page 1                           # next page

Your `project_uuid` is in the `## Environment` section of your system prompt.
Omit `--project-id` to search across every project in the DB.
Output columns: session-id, timestamp, role, snippet.

FTS5 query tips: space-separated terms are AND by default; use `OR` for broad
recall; quote a phrase with double-quotes (`"context window"`); a trailing `*`
is a prefix match (`compact*`). Re-read a hit's full session with
`shell3 read-session <session-id>`, using its session-id.

## Listing projects

    shell3 list-projects            # distinct projects, newest-active first
    shell3 list-projects --page 1

Output columns: uuid, workdir, session count, last activity.

## Listing sessions

    shell3 list-sessions --project-id <project_uuid>   # this project, newest first
    shell3 list-sessions                               # ALL projects
    shell3 list-sessions --page 1

Output columns: session-id, status, parent (subagent's parent, or `-`), message
count, started, preview of the first user message.

## Background jobs

    shell3 jobs            # tracked background jobs for the current workdir
    shell3 jobs --page 1

Lists the background jobs (`bash_bg` runs and subagents) tracked for this
session's workdir. Read-only; dead jobs are auto-pruned on listing.

Output columns: id, pid, log, cmd.

## Reading a full session

To read a past session's full transcript in chronological order (oldest-first):

    shell3 read-session <session-id>            # full transcript
    shell3 read-session <session-id> --page 1   # next page (large sessions)

The session-id comes from `shell3 list-sessions` or an `shell3 fts` hit.

## Rules

- READ-ONLY, always; the commands are inherently read-only.
- Pull only what you need (`--page`); sessions can be large.
- Cite what you find by session id + timestamp so the user can follow up.
