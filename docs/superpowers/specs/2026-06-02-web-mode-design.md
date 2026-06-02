# Design: `shell3 web` — interactive web frontend (MVP)

**Date:** 2026-06-02
**Branch:** `feat/web-mode`
**Status:** Approved (design), pending spec review

## Goal

Add a long-running, persistent `shell3 web --port <n>` mode: an interactive web UI that
drives the same chat engine as the TUI and streams the **same event stream** to the browser.
Meant for server deployments where you check in on / drive a persistent agent from a browser
instead of a terminal. This is an MVP designed to extend cleanly to a full solution.

## Hard constraint: clean separation

All web-specific code lives in a single self-contained package, `internal/web/`. The only
touchpoints in existing code are intentionally minimal:

- `cmd/shell3/web.go` — a thin subcommand that boots config + session + hub + server (mirrors
  `cmd/shell3/doctor.go`/`docs.go` in size and style).
- `pkg/chat` — extract one shared helper `MarshalEventJSON(ev) ([]byte, error)` from the
  existing `OutSink.WriteChatEvent` so the SSE stream and the `--out` JSONL share one schema.
- Delete `examples/webui/` (decoupled prototype, superseded).

No changes to the engine (`pkg/chat` turn/session logic), `internal/tui`, `internal/luacfg`, etc.

## Architecture

The web mode reuses the exact wiring the TUI uses, swapping the renderer for a fan-out hub.

Existing TUI flow (reference): `shell3.New()` → `chat.NewSession(...)` → background goroutine
ranges `sess.Events() <-chan chat.Event` and renders (see `internal/tui/interactive.go`,
`drainTurn`). Turns launched via `sess.Run(ctx, tc, input)`.

Key engine property to preserve: `chat.emit` is a **non-blocking** send that **drops on full
buffer**; each event on the channel is delivered to exactly **one** receiver. Therefore a single
drainer must own the channel and fan out — multiple consumers cannot each `range` it.

### Components (each independently testable)

**1. `internal/web/hub.go` — the Hub (core unit).**
Owns all session/state coordination; no HTTP knowledge.

- Fields: the live `*chat.Session`, the `chat.TurnConfig` (or a builder), an append-only
  in-memory `[]chat.Event` log (replay buffer), a subscriber set (each a buffered chan), a
  `busy` flag, the current turn's `context.CancelFunc`, and a `sync.Mutex`.
- One goroutine ranges `sess.Events()`: append to log, then non-blocking broadcast to every
  subscriber. The hub is the sole drainer (preserves engine non-block/drop semantics).
- Slow-subscriber policy: per-subscriber buffered channel (e.g. 256). On full, the hub
  **drops that subscriber** (closes + unregisters) rather than blocking — the engine and other
  clients are never stalled. The dropped browser's `EventSource` auto-reconnects and replays.
- Public API:
  - `Subscribe() (replay []chat.Event, ch <-chan chat.Event, unsub func())` — snapshot the log
    under lock, register a new channel, return both atomically (no gap/dupe between replay and live).
  - `Submit(text string) error` — if `busy`, return `ErrBusy`; else mark busy, launch the turn
    goroutine (`sess.Run`), clear busy on completion.
  - `Cancel()` — cancel the current turn's context (web analog of TUI Esc).
  - `Clear()` — reset engine conversation context via `sess.SetMessages(nil)` (exactly what the
    TUI `/clear` does — verified at `internal/tui/interactive.go:387`), clear the in-memory log,
    and broadcast a session-reset marker so connected UIs reset their scrollback.
- Constructed with the session + turn config so tests can inject a `pkg/llm/fakellm` client.

**2. `internal/web/server.go` — HTTP layer (thin).**
Routes only; delegates to the Hub.

- `GET /` → serve embedded SPA.
- `GET /events` → SSE. Sets headers (`text/event-stream`, no-cache, keep-alive), calls
  `Hub.Subscribe`, writes the replay as SSE `data:` frames, then streams live events until the
  client disconnects (`r.Context().Done()`), then `unsub`. Periodic heartbeat comment to keep
  proxies from idling the connection.
- `POST /input` → read `{ "text": "..." }`, call `Hub.Submit`; `409` on `ErrBusy`.
- `POST /cancel` → `Hub.Cancel`.
- `POST /clear` → `Hub.Clear`.
- Each event is serialized with `chat.MarshalEventJSON` (shared with `--out`).

**3. `internal/web/assets/` — embedded SPA (`//go:embed`).**
A single `index.html` with inline CSS/JS (no build step, no external deps):

- Opens an `EventSource` to `/events`; renders events into a scrollback styled after the TUI
  (assistant tokens, reasoning dim, tool call headers, tool results dim, errors red, usage line).
- Handles the session-reset marker by clearing the scrollback.
- An input box → `POST /input`; on `409` it disables the box and shows "agent busy" until the
  next `turn_done`. A Stop button → `POST /cancel`. A Clear button → `POST /clear`.

**4. `cmd/shell3/web.go` — subcommand.**
`shell3 web --port <n> [--host 127.0.0.1] [--config ...]`. Resolves config via the existing
discovery (reuse `run.go`'s resolver), `shell3.New(...)`, `chat.NewSession(...)` with the store
session started (so history persists in SQLite as usual), constructs the Hub, starts the HTTP
server, and blocks until SIGINT/SIGTERM — then ends the session, closes events, drains, and
shuts the server down gracefully.

**5. `pkg/chat` refactor (minimal).**
Extract the event→JSON marshaling currently inline in `OutSink.WriteChatEvent` into an exported
`MarshalEventJSON(ev Event) ([]byte, error)`; `OutSink` calls it. Web SSE calls it too. One
schema, two drains.

### Data flow

```
browser EventSource ──GET /events──▶ Hub.Subscribe ──▶ [replay log] + [live chan] ──SSE──▶ browser
browser input box  ──POST /input──▶ Hub.Submit ──▶ sess.Run(turn) ──▶ engine emits
                                                            │
                              sess.Events() ──▶ Hub drainer ─┴─▶ append to log + broadcast ──▶ all SSE clients
```

## Decisions (approved defaults)

- **Transport:** SSE (events) + POST (input). Not WebSocket.
- **Sessions:** one shared, long-lived session per process; all browsers attach to the same
  conversation and see the same stream.
- **Persistence:** in-memory event log for reconnect replay; cleared on `/clear`. Durable
  conversation state remains in SQLite (engine default). No process-restart rehydration in MVP.
- **Concurrency:** one turn at a time; `POST /input` while busy → `409`. No input queue.
- **Binding/auth:** default bind `127.0.0.1`; `--host 0.0.0.0` to expose. No auth/TLS in MVP
  (run behind a reverse proxy). Auth token + TLS is the first post-MVP add.

## Non-goals (MVP) → extension points

- Multi-session / per-browser conversations → add a session registry; the Hub is already the
  natural per-session unit.
- Auth token + TLS.
- WebSocket transport.
- Full slash-command parity (MVP handles only `/clear`; `/model`, `/parameters`, etc. later).
- DB rehydration of the event log on process restart.
- Image upload, file attachments, mobile-polished UI.

## Error handling

- Slow/dead SSE client: dropped by the hub (never blocks engine/others); browser auto-reconnects
  and replays.
- Turn-while-busy: `409` + UI lockout until `turn_done`.
- Config load failure in the subcommand: same error surface as `run.go` ("no shell3.lua found …").
- Server shutdown: graceful `http.Server.Shutdown`, then session end + event drain.

## Testing

- **Hub** (`internal/web/hub_test.go`, using `pkg/llm/fakellm`): replay-then-live with no gap or
  dupe; fan-out to N subscribers; `Submit` returns `ErrBusy` during a turn; `Clear` empties log +
  broadcasts reset; slow subscriber is dropped without blocking others.
- **Server** (`internal/web/server_test.go`, `httptest`): SSE emits replay + live frames;
  `/input` triggers a turn and returns `409` when busy; `/cancel` and `/clear` behave.
- **Marshaling**: `MarshalEventJSON` round-trips the same shape `--out` produced before the
  refactor (guard against schema drift).
- `go build ./...` and `go test ./...` green.

## Risks

- **Event loss under load:** mitigated by hub-drops-slow-subscriber + replay-on-reconnect; the
  hub drainer does only cheap work (append + non-blocking sends) so it keeps pace with the 256
  engine buffer.
- **`/clear` semantics:** resolved — the TUI `/clear` is `sess.SetMessages(nil)`
  (`internal/tui/interactive.go:387`), a clean public method the hub reuses directly. No risk.
- **Multiple browsers driving one session:** acceptable for MVP (shared control, like a shared
  terminal); the busy-lockout prevents overlapping turns.
