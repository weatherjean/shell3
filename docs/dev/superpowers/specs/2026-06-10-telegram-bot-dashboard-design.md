# Telegram front-end + Mini App dashboard

Date: 2026-06-10
Status: approved (design), pending implementation plan

## Goal

A personal "remote terminal" for the shell3 agent: a **Telegram bot** to drive
the agent from a phone (control + push notifications + tool approvals), paired
with a **Telegram Mini App dashboard** for rich read-only observability of the
conversation (full history, code blocks, diffs, tool calls with timings).

Both are Go front-ends over the existing `pkg/shell3.Runtime` — the same surface
the TUI consumes. Nothing in `internal/chat` or the runtime engine changes; this
spec is "render and route data the engine already produces."

The guiding principle, consistent with the rest of shell3: **Lua configures, Go
transports.** Bot token, allowed chat, and dashboard settings live in
`shell3.lua` (token via `shell3.env.secret`, exactly like the model `api_key`);
the long-poll loop, HTTP server, and TLS exposure are Go.

## Non-goals (v1)

- **Multi-user / public bot.** One trusted user, one allowed chat id. No auth
  beyond the chat-id allowlist (bot) and `initData` verification (dashboard), no
  quotas, no per-user isolation.
- **Interactive dashboard.** The Mini App is **read-only**. Sending messages
  stays in the Telegram chat itself. (A future interactive "PWA" front-end can
  reuse the same `Runtime` and HTTP server, but not now.)
- **Token-level streaming in Telegram.** Telegram renders "typing…" then a final
  message per turn. No `editMessageText` streaming.
- **Conversation resume across restarts beyond what the store already gives.**
  The session is persistent via the SQLite store/JSONL sink as-is.

## Architecture

```
                         shell3 telegram   (new subcommand in cmd/shell3)
                                 │ assembles config via agentsetup.Build
                                 ▼
   ┌──────────────────────  pkg/shell3.Runtime  ──────────────────────┐
   │   one persistent Session "telegram"  (SQLite store + JSONL sink)  │
   └───────────────▲───────────────────────────────────▲─────────────┘
                   │ Send/SendParts/Interject/          │ History/Snapshot
                   │ SetApprover  + WakeEvents           │ + rt.Events() (SSE)
        ┌──────────┴───────────┐               ┌─────────┴────────────┐
        │  internal/telegram   │               │ internal/telegram/web │
        │  Bot (long-poll)     │               │ Dashboard (HTTP, RO)  │
        └──────────┬───────────┘               └─────────┬────────────┘
                   │ Telegram Bot API                     │ localhost:PORT
                   ▼  (long polling)                      ▼  exposed via
            Telegram servers  ───────────────────►  Tailscale Serve (*.ts.net)
                   ▲                                      ▲   HTTPS, tailnet-only
                   │ chat messages / inline buttons       │ Mini App webview
                   └──────────────  your phone  ──────────┘   loads initData
```

- **`shell3 telegram` subcommand** (`cmd/shell3`): assembles config through the
  same `agentsetup.Build` path the TUI uses, reads the `telegram` config block,
  constructs the `Runtime` + the single `"telegram"` session, builds the `Bot`
  and `Dashboard`, runs both until SIGINT, then `rt.Close()`. Thin glue only.
- **`internal/telegram` package** (`Bot`): the long-poll loop, update→turn
  routing, inline-button approver, and Wake listener. The analogue of
  `internal/tui/interactive.go`.
- **`internal/telegram/web` package** (`Dashboard`): a read-only `net/http`
  server that renders the session's stored conversation and live-tails
  `rt.Events()` over SSE. Serves the Mini App HTML/JS + a small JSON/SSE API,
  all gated by `initData` verification.
- **`telegram { … }` config block** in `shell3.lua`, parsed by `luacfg`.

### Configuration (Lua declares, Go transports)

```lua
telegram = {
  token     = shell3.env.secret("TELEGRAM_BOT_TOKEN"),  -- secret, from .env
  chat_id   = 123456789,            -- your numeric chat id (allowlist of one)
  dashboard = {
    enabled = true,
    addr    = "127.0.0.1:8765",     -- bind localhost; expose via Tailscale Serve
    url     = "https://host.tailnet.ts.net/",  -- Mini App URL set on the menu button
  },
}
```

`luacfg` gains an optional `Telegram` struct on the loaded config
(`Token`, `ChatID`, `Dashboard{Enabled, Addr, URL}`). Absent block ⇒ the
subcommand exits with a helpful "no `telegram` block configured" message. This
mirrors the existing `api_key = shell3.env.secret(...)` flow (a secret string
read in Lua and surfaced to Go), so it introduces no new secrets mechanism.

## Data flow — Bot

**Inbound message → turn or steering.** The long-poll loop receives an update.
Reject anything whose chat id ≠ the configured `chat_id` (silent drop or a
single "not authorized" reply). For an allowed text/media message:

- If the session is **idle**: `SendParts(ctx, text, parts)` and drain the
  returned `<-chan Event`. Show the Telegram "typing…" chat action while the
  turn runs; on completion, send the assistant's final text as one
  `sendMessage` (chunked at the 4096-char limit; code rendered as MarkdownV2/
  HTML code blocks). Tool calls may optionally be posted as short dim messages.
- If the session is **busy** (a turn is running): `Interject(text, parts)`. The
  inbox drains at the next round boundary — this is exactly the TUI's
  Enter-while-busy steering path. No turn is dropped.

This mirrors `internal/tui` `launchTurn`/`Interject` logic; the busy check uses
the same `Send` single-turn / `ErrBusy` contract the TUI relies on.

**Media.** Telegram photos/voice/documents are downloaded via the Bot API
`getFile` and passed as `shell3.Part`s to `SendParts`. The inbound-media
plumbing (MIME-routed byte loaders, `LoadMediaPart`) already exists, so vision
and audio "just work."

**Approvals (ask-guards) → inline buttons.** `SetApprover` is registered with a
function that, on an `ApprovalRequest`, sends a Telegram message describing the
tool call with two inline-keyboard buttons (Approve / Deny) carrying opaque
callback data keyed to a pending-approval id. The approver blocks on a channel
keyed by that id until the callback arrives, then resolves true/false. A
**timeout → deny** (configurable, default e.g. 5 min) so an unanswered prompt
can't wedge a turn. The turn layer already supports ctx-cancel and fail-closed
defaults, so this composes cleanly. The callback handler edits the prompt
message to show the resolved decision (no dangling buttons).

**Wake bus → unprompted push.** A goroutine ranges over `WakeEvents()`. When a
subagent result (or, in Spec B, a cron job) lands in the inbox and the session
is idle, the Bot runs `RunQueued(ctx)`, drains the events, and pushes the
result to the chat — the agent "messages you" without you prompting.

**Bot commands** (subset, mapped to existing Session methods):

| Command            | Maps to                         |
|--------------------|---------------------------------|
| `/clear`           | `Session.Clear()`               |
| `/agent <name>`    | `Session.SwitchAgent(name)`     |
| `/agents`          | `Session.AgentNames()`          |
| `/set <k> <v>`     | `Session.SetParam(k, v)`        |
| `/stop`            | cancel the in-flight turn ctx   |
| `/rollback`        | `Session.Rollback()`            |
| `/dash`            | reply with the Mini App button  |

## Data flow — Mini App dashboard (read-only)

**What it is.** A Telegram Mini App: the bot's menu button (or a `/dash` inline
button) opens `dashboard.url` in Telegram's in-app webview. Telegram appends
`initData` — a payload including the user id, **HMAC-signed with the bot
token**. The Mini App is a single static page + a small API, themed via the
Telegram WebApp JS SDK to match the client's light/dark theme.

**Auth (`initData` verification).** Every API/SSE request carries the `initData`
(header or query). The server recomputes the HMAC per Telegram's documented
algorithm using the bot token; rejects on mismatch; then checks the embedded
user id equals the configured `chat_id`. This is real cryptographic auth with no
login to build. Combined with Tailscale-only network reachability, it's defense
in depth: a request must be *both* on the tailnet *and* carry valid
Telegram-signed `initData` for the owner.

**Endpoints** (all read-only, all `initData`-gated):

- `GET /` — the Mini App HTML/JS.
- `GET /api/history` — the rendered conversation from `Session.History()` /
  `Snapshot()` plus the JSONL sink for full message-shaped detail (tool calls,
  args, results, timings).
- `GET /api/stream` — **SSE** live-tail of `rt.Events()` (`HostEvent` lifecycle:
  turn start/done, tool call/result, wake, approval) so the open dashboard
  updates as the agent works.

**Data source.** No new persistence: the dashboard reads what the engine already
writes — `internal/chat`'s JSONL sink (message-shaped audit) and
`internal/store`'s SQLite history, surfaced through `Session.History()` /
`Snapshot()`, with `rt.Events()` for the live layer.

**Hosting / exposure.** The server binds `dashboard.addr` (localhost). Exposure
is a **runtime concern, not code**: the recommended path is **Tailscale Serve**
(`tailscale serve https / proxy 127.0.0.1:8765`), giving a real Let's Encrypt
cert on `host.tailnet.ts.net`, reachable only from the tailnet. The machine runs
`bash`, so keeping the dashboard off the public internet matters.

> **Spike before committing (build step 0):** verify Telegram's in-app webview
> loads a `*.ts.net` URL. Mini Apps are a client-side webview over HTTPS, so a
> publicly-trusted `.ts.net` cert *should* load on a phone that's on the tailnet
> — but it's not an officially documented path. If it balks, fallbacks are
> Tailscale **Funnel** (exposes the `.ts.net` publicly — then `initData` is the
> sole auth, still cryptographically sound) or sslip.io + a managed cert. The
> dashboard code is identical regardless; only the exposure changes.

## Error handling & edge cases

- **Unauthorized chat id:** drop (optionally one terse reply). Never run a turn.
- **Telegram API failures / long-poll disconnects:** exponential backoff + retry
  in the poll loop; the Runtime/session is unaffected.
- **Approval timeout:** resolve deny, edit the prompt message to "⏱ denied
  (timeout)". Fail-closed matches the turn layer's default.
- **Message too long:** chunk at 4096 chars on line boundaries; code fences kept
  intact per chunk.
- **`initData` invalid/expired:** dashboard returns 401; no data leaks.
- **Dashboard disabled:** `/dash` replies that the dashboard is off; server not
  started.
- **SIGINT:** stop the poll loop, close the HTTP server, `rt.Close()` (which
  already joins subagent goroutines and flushes the sink).

## Testing

- **Bot routing (unit):** a fake Telegram transport (interface around
  send/poll/answerCallback) + `fakellm`. Assert: idle message → `SendParts`;
  busy message → `Interject` (no dropped turn); unauthorized chat id → dropped;
  long reply → chunked.
- **Approver (unit):** simulate an `ApprovalRequest`; assert Approve callback →
  true, Deny → false, no callback before timeout → deny; prompt message edited.
- **Wake push (unit):** seed the inbox, signal `WakeEvents`, assert a wake turn
  runs and the result is sent.
- **`initData` verification (unit):** known-good signed payload passes; tampered
  payload and wrong user id rejected (use a fixed test bot token + vectors).
- **Dashboard API (unit):** `/api/history` renders a seeded session; `/api/stream`
  emits seeded `rt.Events()`; all endpoints 401 without valid `initData`.
- Run `go test ./... && go test -race ./internal/telegram/...` green before
  declaring done.

## Implementation approach

Build with **Sonnet subagents in parallel, then verify/fix** (the workflow used
for the agent-runtime audit): decompose into disjoint units — (1) `luacfg`
`telegram` block + config struct, (2) `internal/telegram` Bot (routing, media,
commands), (3) the inline-button approver + Wake listener, (4)
`internal/telegram/web` dashboard (server, `initData`, history/SSE), (5) the
`cmd/shell3` subcommand + scaffold/`.env` template touch-ups — dispatch a Sonnet
subagent per unit with precise file scope, then the orchestrator verifies with
`go build ./...`, `go vet`, `gofmt -l`, and `go test -race ./...`, and fixes any
integration gaps. Do the Tailscale/webview spike (build step 0) first; it can
invalidate the hosting assumption cheaply.

## Open questions for the implementation plan

- Which Telegram library: recommend `github.com/go-telegram/bot` (modern,
  context-aware, no global state) over the older `telegram-bot-api`.
- Exact Mini App framing: vanilla HTML/JS + WebApp SDK (no build step) vs a tiny
  bundler. Lean vanilla for v1 (no toolchain).
- Whether tool calls post as separate Telegram messages by default or only show
  in the dashboard (lean: dashboard only, to keep the chat clean).
