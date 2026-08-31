---
name: history
description: Recall and send past conversations with the history tool — including main, subagent, and cron sessions — and retrieve background-job logs.
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

Narrow either search results or recent-run listings with any combination of
`agent`, `cron`, `parent`, `since`, and `before` (`YYYY-MM-DD` or RFC3339):

    history {"query": "deploy", "agent": "ops", "since": "2026-08-01"}
    history {"runs": true, "cron": "nightly-sync"}
    history {"parent": "<parent session id>"}

Run listings include each session's first user prompt, which is usually the
fastest way to distinguish otherwise similar subagent or background runs.

## Send the exact record

When the user asks to see or receive a past conversation, use search and a
read-around excerpt to identify the right session first. Then send the exact
stored transcript as a self-contained HTML document:

    send_record_telegram {"kind": "conversation", "session": "<id>"}

Subagent and cron turns are ordinary stored sessions, so the same operation
works for them. Use `history {"runs": true, "agent": "<name>"}` when the
agent name is the best clue. If several sessions plausibly match, ask a short
clarifying question or name the candidates instead of sending a pile of files.

A `bash_bg` command is a log rather than a conversation. Its task status or
the `/status` snapshot gives you its job id and parent session; send it with:

    send_record_telegram {"kind": "job_log", "session": "<parent-session>", "job": "<job-id>"}

Never send a transcript or log merely because it looks interesting. Telegram
attachments persist in chat history, so export only in response to an explicit
user request.

## Notes

- Sessions are stored in `.shell3_project/shell3.db` (SQLite). The history
  tool is the interface; treat the database itself as off-limits for writes.
- A `bash_bg` job's full output remains a plain file under the runs store; use
  `task_status` to inspect current jobs and `send_record_telegram` to deliver
  the exact persisted log.
- Cite what you find by session id so the user can follow up.
