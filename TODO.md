# TODO

## `/stop` cannot interrupt an in-flight turn (serial telegram loop deadlock)

**Symptom:** When a tool call hangs (e.g. a flaky/slow browser or external call), the
Telegram bot stops responding entirely and `/stop` does nothing. The turn — and the
whole bot — is wedged until the hung call returns on its own.

**Root cause (verified in code, not the MCP layer):**
- `internal/mcp/client.go:140` already honors `ctx.Done()`, so tool dispatch itself
  *is* cancelable. The bug is one level up.
- `internal/telegram/bot.go` processes messages **serially**: `Run` (~:81-90) reads one
  update and calls `handleMsg`, which **blocks** at `~:138`
  (`b.drainTurn(b.sess.Send(turnCtx, text))`) until the turn finishes. The code comment
  at `~:127-128` says so: "handleMsg is serial, so a running turn blocks here until the
  channel drains."
- `/stop` (`internal/telegram/commands.go:63`) calls `b.cancelTurn`, which *is* wired
  (`bot.go:137`). But while a turn runs, the loop is parked at the turn and never reads
  the next update — so the `/stop` message sits unread in Telegram's queue and
  `cancelTurn` is never called. It only runs *after* the turn ends, when there's nothing
  left to stop.

**Fix direction:** run the turn on its own goroutine (or otherwise keep consuming updates
during a turn) so `/stop` → `cancelTurn` can land mid-turn. Add a timeout/cancel boundary
around individual tool calls so a single flaky tool can't hang a turn indefinitely. Keep
the existing `HasQueuedInput`/`Interject` steering path working.

**Scope note:** pre-existing engine bug in the merged telegram front-end; independent of
the MCP-removal / browser-skill work. Surfaced by the chrome-devtools-mcp `--autoConnect`
hang on 2026-06-11.
