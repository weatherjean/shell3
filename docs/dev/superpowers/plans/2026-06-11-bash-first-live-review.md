# bash-first — live review script (you drive, I fix)

Manual run-through to exercise the branch live before merge (handoff §2). The
unit suite is green; this catches the things only a real session shows. Work
top-to-bottom; for each step do the action, compare against **Expect**, and tell
me the result. On any mismatch, paste what you saw — I fix on `feat/bash-first`,
you re-run that step.

**Configs under test**
- TUI: `~/.shell3/shell3.lua` — agents `code` (default) + `plan`, subagent
  `explorer`, model `minmax`. Loads clean (verified).
- Telegram: `~/.shell3/telegram/shell3.lua` — agent `code`, subagent `explorer`.
  Was load-breaking under bash-first; **now fixed** (`on_tool_call` + removed
  `history`/`prune`/`compact` gates stripped, no-op `wrap_bash` added). Loads
  clean (verified).

## 0. Pre-flight

```bash
cd ~/CODE/AGENTS/shell3
git branch --show-current        # expect: feat/bash-first
go build ./... && go test ./...  # expect: green
make install                     # reinstall the branch binary as `shell3`
shell3 --version
```
**Expect:** suite green; `which shell3` → `~/go/bin/shell3`. (The branch binary
is already installed, but reinstall so the live review matches HEAD.)

---

## Phase A — TUI (`shell3` in a real terminal)

Run `shell3` from a normal terminal (the TUI needs raw mode — it won't run piped
or inside a non-TTY). Default agent is `code`.

### A1. bash with ANSI color
Type: `run: ls --color=always -la` (or `CLICOLOR_FORCE=1 git -c color.ui=always status`).
- **Expect:** the bash result keeps its colors (green/blue file names, etc.), not
  flattened to one dim gray. Color forwarding is display-only; truncation still
  applies to long output.
- **If fail:** colors stripped/dimmed → `internal/tui/render.go` color path.

### A2. edit_file
Ask: `create /tmp/bf-test.txt containing "hello bash-first", then change "hello" to "hi"`.
- **Expect:** agent uses `edit_file` (empty `old_string` creates the file, then a
  targeted replace). `cat /tmp/bf-test.txt` → `hi bash-first`. No guard/approval
  prompt appears (edit_file is unguarded by design).

### A3. history skill (read-only SQLite)
Ask: `how many sessions are in your history DB? use the sqlite path from your Environment section, read-only`.
- **Expect:** agent runs `sqlite3 'file:<db>?mode=ro' "select count(*) from ..."`
  against the DB path shown in its `## Environment` block and reports a number.
  No `history_get`/`history_search` tool is used (those are gone).

### A4. bash_bg job → sink notification lands
Ask: `start a background job that sleeps 8 seconds then echoes done, using bash_bg; tell me the pid, then wait for it to finish`.
- **Expect:** `bash_bg` returns a pid + log path immediately. ~8s later a pointer
  notification is injected and the session wakes, worded roughly:
  `background job <id> exited (code 0). Output log: <path>. cmd: ...`
  The agent then reads the log with bash. The heavy output is NOT inlined in the
  notification — only the pointer.
- **If fail:** no notification / no wake → sink watcher (`pkg/shell3/sink.go`),
  bg reaper (`internal/bgjobs`).

### A5. Subagent end-to-end (delegation)
First confirm the prompt reads right: ask `show me your Delegation section verbatim`.
- **Expect** a `## Delegation` section listing `explorer`, and the exact spawn
  command shape:
  `shell3 --config <cfg> --agent <name> --out .shell3/agents/<id>.jsonl --append-sinkfile <sink> --id <id> --no-subagents "<task>"`
  plus the note to pass `notify_on_exit=false` to `bash_bg`.

Then: `delegate to the explorer subagent: "summarize what internal/sink does in 3 bullets". Don't poll — wait for it to report back.`
- **Expect:** agent spawns via `bash_bg` with the delegation command and
  `notify_on_exit=false`; it does NOT poll. When the child finishes it
  self-reports — a single pointer arrives, worded roughly:
  `subagent <id> finished (ok). <preview> Full transcript: .shell3/agents/<id>.jsonl — read it for detail.`
  The agent then cats the transcript and relays the 3 bullets. Exactly ONE
  notification per spawn (not a bg_done *and* an agent_done).
- **If fail:** two notifications → `notify_on_exit` default; no agent_done → child
  `--append-sinkfile` self-report (`cmd/shell3/run.go`).

### A6. Auto-compaction (low threshold)
Edit `~/.shell3/shell3.lua`, set the minmax model `compact_at = 6000` (from
100000), then `/reload` (or restart `shell3`). Hold a conversation / paste a
large file until the prompt crosses ~6000 tokens.
- **Expect:** the host interrupts before a turn, summarizes the conversation, and
  continues — history is preserved (no lost thread), no `prune`/`compact` tool is
  ever called, and a failed summarize never blocks the turn (it just proceeds).
- **Cleanup:** restore `compact_at = 100000` and `/reload`.

### A7. wrap_bash messaging (unsafe-by-default)
Sanity: `~/.shell3/shell3.lua` has `shell3.wrap_bash(function(cmd) return cmd end)`.
Optionally prove the hook fires: temporarily change it to
```lua
shell3.wrap_bash(function(cmd)
  if cmd:match("rm%s+-rf") then return nil, "blocked: rm -rf" end
  return cmd
end)
```
`/reload`, then ask the agent to `run: rm -rf /tmp/bf-nope`.
- **Expect:** the command is blocked with the reason `blocked: rm -rf`; there is
  **no y/N approval prompt** (the approval engine is gone — only allow/block).
- **Cleanup:** restore the no-op `wrap_bash` and `/reload`.

---

## Phase B — Telegram (`shell3 telegram`)

```bash
shell3 telegram          # uses ~/.shell3/telegram/shell3.lua
```
- **Expect at startup:** the bot comes up cleanly with **no config error** (this
  is the load-breaking fix landing — before, it died on `on_tool_call`). Reply
  keyboard shows `/stop /reload /clear`.

### B1. Basic turn + no approval prompts
Send: `run: echo hi && ls`.
- **Expect:** normal result; **no Approve/Deny buttons** ever appear (the
  Telegram approval flow was removed with the guard engine).

### B2. /stop kills a running subagent (now a bg process)
Send: `delegate to explorer: "sleep 60 then echo done". Don't poll.` Once it's
spawned (you'll see it go to work), send `/stop`.
- **Expect:** the running turn is interrupted AND the backgrounded subagent
  process is killed (no orphaned `shell3 --agent explorer` lingering — verify
  with `pgrep -fl "shell3 --agent explorer"` → empty). This is the bash-first
  change: a subagent is a bg process, so `/stop` must reap it.
- **If fail:** orphaned child → `/stop` reaping path in `cmd/shell3/telegram.go`.
  (Note: the old `/stop` deadlock bug is tracked in TODO.md — watch for a hang.)

### B3. Cron Notice
Uncomment the sample cron block at the bottom of
`~/.shell3/telegram/shell3.lua` (the `daily` / `explorer` job), call the `reload`
tool (or `/reload`), then fire it on demand: send `/run daily`.
- **Expect:** the job dispatches the `explorer` subagent and posts an operator
  **Notice** to the chat with the result — delivered via the cron Notice path,
  NOT the sink (cron keeps operator-Notice semantics by design).
- **Cleanup:** re-comment the cron block and reload.

### B4. self-reconfigure round-trip (optional)
Ask the bot to add a trivial skill to itself and `reload` (per its self-evolve
skill). Confirm a bad edit is rejected and the old config keeps running, and a
good edit goes live next turn.

---

## After the run

When every step passes, ping me — I'll take it through
`finishing-a-development-branch` (likely FF / PR to `main`, then `make install`
from `main`), only with your OK. If anything failed, paste the symptom and I fix
on `feat/bash-first` before we merge.
