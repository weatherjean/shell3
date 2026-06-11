# TODO

_No open items._

## Resolved

### `/stop` cannot interrupt an in-flight turn (serial telegram loop deadlock) — FIXED

The Telegram `Run` loop blocked inside `handleMsg`→`drainTurn` while a turn ran,
so `/stop` was never read mid-turn and the whole bot wedged until the hung call
returned. Fixed by running each turn on its own goroutine (the loop stays
responsive) and extending `/stop`'s reach: it now cancels the turn ctx (which
SIGTERM→SIGKILLs synchronous `bash`/`node` process groups), kills tracked
`bash_bg` jobs (`bgjobs.KillAll`, process-group SIGKILL), and cancels in-flight
subagents (session-scoped subagent context + `Session.CancelSubagents`).
Intentionally-persistent infra — the detached browser window and model proxies —
is left running.

Commits: `0d2ea4f` (concurrent turn + reachable `/stop`), `2541928` (bash_bg
kill), `2297a65` (subagent cancellation).
