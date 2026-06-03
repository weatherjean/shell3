# shell3 plugin API — design

**Date:** 2026-06-03 (revised)
**Status:** Approved (design)
**Branch:** feat/shell3-plugin-api

## Core principle

**Plugin mode is the TUI with a different front-end — nothing less.** The
embedder passes the same two inputs the CLI takes (config path + cwd) precisely
so the plugin resolves *the same* `.shell3/` project, *the same* SQLite store,
*the same* persona/docs/memory, and behaves **identically** to the interactive
TUI. The only thing that differs is the front-end: a structured event stream
instead of the bubbletea UI (`RunInteractive`) or the stdout loop (`RunOnce`).

This corrects an earlier draft that built a deliberately "minimal, stateless"
embedded config. That was wrong: it produced a *different, degraded* agent (no
store, no core memories, no real timestamp in the prompt, no docs). A plugin
embedder wants the real agent, driven by their own UI.

## Architecture: three front-ends over one shared core

```
                 internal/agentsetup.Build(opts)  ← THE shared assembly
                 (paths + bootstrap + log + store + core memories +
                  luacfg + client + models/switchModel + persona + docs)
                              │ returns (chat.Config, cleanup)
        ┌─────────────────────┼─────────────────────┐
        ▼                     ▼                     ▼
  tui.RunInteractive    tui.RunOnce          pkg/shell3 (NEW)
  (bubbletea)           (stdout one-shot)    (event stream)
```

### 1. `internal/agentsetup` — the shared builder

Lift the entire config assembly out of `cmd/shell3/run.go:runChat` (≈ lines
95–251) into one reusable function. It lives in `internal/` so both
`cmd/shell3` (package main) and `pkg/shell3` (library) can import it — neither
can import the other, but both can import `internal/`.

```go
package agentsetup

type Options struct {
    ConfigPath string // explicit path; "" triggers default resolution
    CWD        string
    HomeDir    string
    Headless   bool
    OutPath    string // JSONL audit log; "" disables
}

// Build resolves the config path (when ConfigPath is empty: ./shell3.lua, else
// ~/.shell3/shell3.lua), ensures the global + project dirs, opens the app log,
// opens the SQLite store when Gates.Memory/History, loads core memories, loads
// shell3.lua, builds the client + models/switchModel, assembles the persona
// (WITH timestamp + core memories) and the full chat.Config. The returned
// cleanup closes the Lua state, the store, and the log.
func Build(opts Options) (chat.Config, func(), error)
```

`resolveConfigPath` moves here too (shared default-resolution). `cmd/run.go`
shrinks to: parse flags → compute headless → `agentsetup.Build` → dispatch to
`RunInteractive` / `RunOnce`. CLI behavior is unchanged; its existing tests must
still pass.

The embedded markdown `docsContent` (`//go:embed shell3.md` in
`cmd/shell3/docs.go`, currently `package main`) moves to a small importable
embed (`internal/docs`) so the builder can populate `cfg.Docs` and the `docs`
subcommand still works.

### 2. `pkg/shell3` — the event-stream front-end

Mirrors `RunInteractive`'s session lifecycle (one persistent `chat.Session`,
`StartSession`/`EndSession`, one long-lived drain, turn boundaries via
`TurnDone`/`Error`) but translates events onto a per-`Send` channel instead of
rendering bubbletea.

```go
// Multi-turn handle — what a VSCode extension holds open.
func Start(ctx context.Context, spec Spec) (*Session, error)
func (s *Session) Send(ctx context.Context, prompt string) <-chan Event
func (s *Session) ID() string                       // store session id (live; rolls on compaction)
func (s *Session) Clear()                            // = /clear: reset history + re-stamp prompt
func (s *Session) Rollback() bool                    // = /rollback: drop last turn; false if nothing to drop
func (s *Session) SwitchModel(name string) error     // = /model <name>: swap client for later Sends
func (s *Session) Close() error                      // EndSession + cleanup

// One-shot convenience = Start → Send → Close.
func Run(ctx context.Context, spec Spec) (<-chan Event, error)

type Spec struct {
    Prompt     string // used by Run; ignored by Start/Send
    ConfigPath string // "" → default resolution (./shell3.lua, ~/.shell3/shell3.lua)
    WorkDir    string // "" → os.Getwd()
    // Phase 2 will add ResumeID here (additive, non-breaking).
}
```

## Public Event

```go
type Kind int
const (
    Token      Kind = iota // assistant text       → Text
    Reasoning              // thinking text         → Text
    ToolCall               // tool started          → ToolName, ToolInput
    ToolResult             // tool finished         → ToolName, ToolOutput
    Usage                  // per-roundtrip tokens  → token fields
    Retry                  // transient retry       → Text
    Error                  // turn error            → Err
    Done                   // turn end (normal)     → token fields (final totals)
)

type Event struct {
    Kind             Kind
    Text             string // Token, Reasoning, Retry
    ToolName         string // ToolCall, ToolResult
    ToolInput        string // ToolCall  (raw JSON args)
    ToolOutput       string // ToolResult
    PromptTokens     int    // Usage, Done
    CompletionTokens int    // Usage, Done
    TotalTokens      int    // Usage, Done
    Err              error  // Error
}
```

### Event mapping (`chat.EventKind` → public `Kind`)

| internal | public |
|---|---|
| `EventAssistantToken` | `Token` |
| `EventAssistantReasoning` | `Reasoning` |
| `EventToolCall` | `ToolCall` (ToolName, ToolInput) |
| `EventToolResult` | `ToolResult` (ToolName, ToolOutput) |
| `EventUsage` | `Usage` (token fields from `ev.Usage`) |
| `EventTurnDone` | `Done` (token fields from `ev.Usage`) |
| `EventRetry` | `Retry` (Text) |
| `EventError` | `Error` (Err = `errors.New(ev.Text)`) |
| `EventSessionStart/End`, `EventUserMessage`, `EventAssistantMessage`, `EventSystemReminder` | *dropped* |

Note: `EventError` carries its message in `Text` (no error object), so `Err`
wraps that string. `EventUsage`/`EventTurnDone` carry `*EventUsageData`
(nil-guard before reading).

## Multi-turn mechanism (the key concurrency design)

`Start` builds the config, starts the store session, creates one persistent
`chat.Session`, and launches one long-lived **drain** goroutine over
`sess.Events()`.

`Send` sets the Session's "current channel" to a fresh `out`, then runs one turn
(`sess.Run`) in a goroutine. The drain translates each event and routes it to
`out`. On `Done` or `Error` (every turn ends with exactly one of these — normal
completion emits `TurnDone`; stream/cancel/panic paths emit `Error`), the drain
closes `out` and clears current. Session-level events (`SessionStart/End`,
`UserMessage`) are dropped by `translate`, so they never reach a `Send` channel.

```go
// drain (one per Session, started in Start):
for ev := range s.sess.Events() {
    pub, ok := translate(ev)
    if !ok { continue }
    s.mu.Lock(); cur := s.cur; s.mu.Unlock()
    if cur == nil { continue }
    cur <- pub                          // blocks if caller stops draining
    if pub.Kind == Done || pub.Kind == Error {
        s.mu.Lock(); close(s.cur); s.cur = nil; s.mu.Unlock()
    }
}
close(s.drainDone)
```

**Contract:** the caller MUST drain each `Send` channel to completion before the
next `Send` (or `Clear`/`Rollback`/`SwitchModel`). A chat UI does this naturally
(wait for the reply before sending again). Abandoning a `Send` channel blocks
the drain on the unread send and wedges the Session — same hazard the TUI's
single-consumer `drainTurn` has, made explicit here.

`Run` (one-shot) = `Start` → `Send(spec.Prompt)` → forward the channel → `Close`
when it drains.

## Lifecycle

- **Start:** `Build` → (store) `StartSession` → `NewSession` → `sess.Start(meta)`
  → launch drain. On `Build` error: return `(nil, err)`, nothing created.
- **Close:** `sess.End("ok")` → `sess.CloseEvents()` (ends the drain) → wait
  `drainDone` → (store) `EndSession(sess.ID())` → `cleanup()` (closes store, Lua,
  log). `ID()` reads `sess.ID()` live because compaction can roll it.
- **Side effects match the TUI** — the plugin creates `~/.shell3`/project dirs,
  writes logs, opens the DB. By design: it is the same agent.

## What is now in scope (vs the earlier minimal draft)

Because it is the real TUI config, these all just work — no special handling,
no "boundaries":

- SQLite store, persistence, `memory_*` / `history_*` tools
- core memories injected into the system prompt
- real timestamp in the prompt; `RefreshPrompt` for `Clear`
- `compact_history` including its `history_get` drill-back to the originals
- `ID()` is the real store session id (and the future `ResumeID`)

## Out of scope (YAGNI / later)

- **Phase 2 reattach:** `Spec.ResumeID` + seeding a `chat.Session` from stored
  history. Additive; `ID()` already returns the id to persist.
- **`/image` multimodal input** (`ContentParts`): niche; deferred. `Send` is
  string-only for now.
- **`io.Writer`/callback sugar** over the channel.
- Surfacing `SessionStart/End` / `AssistantMessage` events.

## Testing

- `agentsetup.Build`: builds a valid `chat.Config` from a temp `shell3.lua`
  (+ `.env`); errors clearly when config is missing.
- CLI regression: existing `cmd` / `tui` tests pass after the refactor.
- `translate`: table covering all 8 mapped kinds + the dropped ones, incl. Usage
  token fields and the Error→Err string.
- Plugin multi-turn: `fakellm`-driven `chat.Config` through a real `Session` —
  assert Token→Done on turn 1, history carried into turn 2, `ToolCall`+
  `ToolResult` pairing, `Usage`/`Done` token fields, the error path (Error, no
  Done, channel closes), `Clear`/`Rollback` mutate history, `Run` one-shot.
