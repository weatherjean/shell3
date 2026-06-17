---
name: history
description: Search and read past conversations and background job logs via the file-native runs store (read-only).
---

# History — search and read past conversations

shell3 persists every conversation as plain JSONL files under
`.shell3_project/runs/<session-id>/messages.jsonl`, one JSON object per line.
Session IDs are sortable timestamp strings (e.g. `20060102T150405.000000000`).

## Searching history

Use `rg` (ripgrep) to search across all sessions:

    rg -n "JWT|expiry" .shell3_project/runs        # OR: alternation, not the word "OR"
    rg -n "context window" .shell3_project/runs
    rg -in "compact" .shell3_project/runs          # case-insensitive

Output shows `<path>:<line>:<json-line>`. Extract the session id from the path
(the directory component after `runs/`).

## Listing sessions

    ls -lt .shell3_project/runs/                   # newest directories first
    cat .shell3_project/runs/<id>/meta.json        # session metadata (workdir, model, status)

The `meta.json` fields: `id`, `workdir`, `config_path`, `model`, `status`
(`live` or `ended`), `parent_id` (set for subagents), `started_at`, `last_at`,
`ended_at` (set once the session ends).

## Reading a full session

    cat .shell3_project/runs/<id>/messages.jsonl \
      | jq -r '.role + ": " + (.content // "")'

Or print raw JSON lines with line numbers:

    cat -n .shell3_project/runs/<id>/messages.jsonl

Use `shell3 read-session <session-id>` for a formatted chronological dump:

    shell3 read-session 20060102T150405.000000000

## Background job logs

Background job logs are written under `.shell3_project/runs/jobs/`:

    ls .shell3_project/runs/jobs/                  # list job files
    cat .shell3_project/runs/jobs/<job-id>.jsonl   # read a job's output (stdout+stderr)
    cat .shell3_project/runs/jobs/<job-id>.status  # pid, started_at, exit code

## Rules

- READ-ONLY always; do not modify any file under `.shell3_project/runs/`.
- Pull only what you need; sessions can be large — pipe through `head`/`tail`/`jq`.
- Cite what you find by session id so the user can follow up.
