---
name: spawning-subagents
description: Use when delegating a sub-task to a fresh shell3 process so it runs in parallel, isolated from the current conversation. Covers spawning with bash_bg, polling the JSONL audit log, and timing the wait with sleep.
---

# Spawning subagents

When a task is independent enough that you want a fresh agent to work it without polluting the current context, spawn a sibling `shell3` process. Each spawned agent writes a JSONL audit log; you watch the log to know what it did and when it finished.

## Pattern

1. Pick a temp path for the audit log. Prefer `/tmp/shell3-<short-slug>-<timestamp>.jsonl` — temp dirs are cleaned by the OS and writable without permission worries.
2. Spawn with `bash_bg` so the call returns immediately:
   ```bash
   shell3 "your-task-description-here" --out /tmp/shell3-find-deps-1715537000.jsonl
   ```
3. Sleep, then read the log. The last line is always `{"kind":"end","status":"ok|error"}`. If absent, the agent is still working.

## When to use this

- The sub-task is **self-contained** (no back-and-forth with you).
- You'd rather not pay context cost for the sub-task's tool noise.
- You have other work to do in parallel.

## When NOT to use this

- The sub-task needs interactive input — spawned agents run headless and refuse `shell_interactive`.
- The sub-task can finish in a single bash call — just use bash directly.
- You need streaming feedback — JSONL polling is batch-style.

## Polling pattern

```bash
# Spawn
OUT=/tmp/shell3-task-$(date +%s).jsonl
shell3 "summarise the open PRs on this repo" --out $OUT  # via bash_bg

# Wait + check
sleep 30
if tail -n1 $OUT | grep -q '"kind":"end"'; then
  cat $OUT | jq -r 'select(.kind=="text").text' | head -50
else
  echo "still working, sleep more"
fi
```

For long-running work, sleep in increasing increments (30s, 60s, 120s) rather than a tight poll loop. The JSONL is append-only, so reading it at the end is fine.

## Reading the JSONL

Each line is one event. Useful filters:

- Final assistant text:
  ```bash
  jq -r 'select(.kind=="text") | .text' < $OUT
  ```
- Tool calls only:
  ```bash
  jq 'select(.kind=="tool")' < $OUT
  ```
- Final usage:
  ```bash
  jq 'select(.kind=="turn_done")' < $OUT
  ```
- Was it cancelled / did anything break?
  ```bash
  jq 'select(.kind=="error")' < $OUT
  ```

See `docs/headless.md` in the shell3 repo for the full schema reference.

## Headless caveats

A spawned agent runs with `SHELL3_HEADLESS=1` and the default `confirm-bash` hook will **block destructive commands automatically**. The blocked call appears in the JSONL as a tool result containing "Headless mode: destructive command requires human approval." If your sub-task legitimately needs destructive operations, either:

- Refactor the sub-task to avoid them, OR
- Spawn with `SHELL3_HEADLESS_TRUST=1 shell3 ...` to opt the child into "trust the agent" mode (only do this when you're sure the task is safe).

## Output location convention

- `/tmp/shell3-<slug>-<unix-timestamp>.jsonl` — default for ad-hoc spawns.
- `.shell3/agents/<slug>.jsonl` — when you want the log persisted alongside the project (commit-ignore via `.gitignore`).
