---
name: history
description: Recall past conversations with the history tool — full-text search over everything you and the user have said, across all stored sessions.
---

# History — recall past conversations

Everything you and the user have said, in every stored session, is
full-text-searchable through the `history` tool. Use it whenever the user
references something from before ("that certificate thing we fixed", "what
did I ask you last week"), before saying you don't remember.

## Search, then read around the hit

    history {"query": "certificate renewal"}
    history {"session": "<id from a hit>", "around": 41}

Query syntax (FTS5): bare words AND together, "quoted phrases" match
exactly, `OR` / `NOT` / `prefix*` work. Search covers user and assistant
text only — tool output is not indexed, so search for what was *said about*
a thing, not for raw command output.

## Notes

- Sessions are stored in `.shell3_project/shell3.db` (SQLite). The history
  tool is the interface; treat the database itself as off-limits for writes.
- A `bash_bg` job's full output is a plain file:
  `.shell3_project/runs/<session-id>/jobs/<job-id>.log` — `cat`/`tail` it.
- Cite what you find by session id so the user can follow up.
