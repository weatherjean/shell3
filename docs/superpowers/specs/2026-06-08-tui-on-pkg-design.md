# Design: Wire the TUI onto `pkg/shell3`

**Date:** 2026-06-08
**Branch:** `feat/tui-on-pkg`
**Status:** Approved (Approach A)

## Problem & Goal

Today `pkg/shell3` and `internal/tui` are **siblings**, not parent/child: both sit
directly on `agentsetup.Build → chat.Config → chat.Session`. `pkg/shell3` is a
deliberately narrow, lossy, headless-only facade; the TUI is a *richer* client
that reaches straight into internal `chat.Event` and `internal/llm`.

`pkg/shell3` is the **product** — the surface outside developers build on. The TUI
is the **proof**: if our own flagship interactive client can live entirely behind
the public API, an outside implementer can trust the boundary is real and
complete. The deliverable is an embedding API good enough to attract implementers.

**Success criterion:** `internal/tui` imports **only** `pkg/shell3` plus its own
render packages (`patchapp`/`patchmd`/`patchtui`). Zero imports of `chat` or `llm`.
Nobody outside `pkg/shell3` touches `agentsetup`/`chat`/`llm`.

## Approach

**Approach A — pkg-native types, clean facade.** Every exposed concept gets a
`pkg/shell3` type; the package never leaks `llm.*` or `chat.*`. `pkg/shell3` grows
into a **superset** of the TUI's needs. Rejected alternatives: B (re-export
internal types — cheap now, deletes the encapsulation promise) and C (hybrid —
A with extra ceremony for little gain).

## Target architecture

```
cmd/shell3 ──> internal/tui ──> pkg/shell3 ──> internal/{agentsetup,chat,llm,store,…}
                     └────────> internal/{patchapp,patchmd,patchtui}   (rendering only)
```

- `cmd` builds a `shell3.Spec` and calls `tui.RunInteractive(ctx, spec)` /
  `tui.RunOnce(ctx, spec)`, which call `shell3.Start` internally.
- `cmd` keeps the `SHELL3_HEADLESS`/`SHELL3_OUT` `os.Setenv` calls (consumed only
  by external hook subprocesses). **`pkg/shell3` never mutates global env** — it
  stays a pure library.
- `patchapp`/`patchmd`/`patchtui` and `render.go`'s formatting helpers stay
  internal to `tui` — presentation, not API.

## Public API (`pkg/shell3`)

Additions marked **NEW**.

```go
type Spec struct {
    Prompt           string
    ConfigPath       string
    WorkDir          string
    Agent            string
    Interactive      bool   // NEW: false = headless (today's default → Headless:true)
    OutPath          string // NEW: JSONL audit log path
    ShellInteractive func(ctx context.Context, cmd, workdir string) string // NEW: TTY-release hook; nil = "unavailable"
}

// lifecycle (exists)
func Start(ctx, Spec) (*Session, error)
func Run(ctx, Spec)   (<-chan Event, error)
func (s *Session) Send(ctx, prompt string) <-chan Event
func (s *Session) SendMessage(ctx, Message) <-chan Event // NEW
func (s *Session) Close() error
func (s *Session) ID() string

// conversation control (exists)
func (s *Session) Clear()
func (s *Session) Rollback() bool
func (s *Session) SwitchAgent(name string) error
func (s *Session) AgentNames() []string
func (s *Session) ActiveAgent() string

// introspection (NEW)
func (s *Session) Snapshot() Snapshot                         // /prompt, /info, status bar, welcome, /parameters list
func (s *Session) History() []HistoryEntry                    // /print
func (s *Session) Prune(id string) (summary string, ok bool)  // /prune
func (s *Session) SetParam(name, value string) error          // /parameters set
```

### New pkg-native types

```go
type Event struct {                          // GROWN
    Kind        Kind
    Text        string
    ToolName    string
    ToolCallID  string  // NEW
    ToolInput   string
    ToolOutput  string
    IsCustomTool bool   // NEW
    PromptTokens, CompletionTokens, TotalTokens int
    Err         error
}
// Kind gains: SystemReminder

type Message struct {
    Text        string
    Attachments []Attachment
}
type Attachment struct { Path string } // (carrier for chat.BuildImageMessage)
func ImageMessage(args, workDir string) (Message, error) // wraps chat.BuildImageMessage

type HistoryEntry struct { Role, Content, ToolName, ToolCallID string } // Content already prefix-stripped
type ToolInfo   struct { Name, Description string }
type ParamValue struct { Name, Value, Default, Description string; Enum []string }
type Snapshot struct {
    Agent, Model, ProjectRef, StatusLine string
    ContextWindow int
    SystemPrompt  string
    Tools         []ToolInfo
    Skills        []string
    Params        []ParamValue
}
```

### Implementer-friendly details (deliberate)

- **`HistoryEntry.Content` is prefix-stripped** — pkg hides the internal
  `[tool_call_id=…]\n` storage prefix (see `tui/render.go:stripToolIDPrefix`), so
  `/print`-style features are trivial for any embedder.
- **`Event.IsCustomTool`** is computed inside pkg from the active agent's
  `CustomToolNames`; renderers never need the agent config to color a tool.
- **`Snapshot` is one read-only struct**, not many getters: one allocation per
  call, trivially mockable, can't be half-updated.

## Behaviors that move into `pkg/shell3`

### Audit sink ownership

The JSONL audit log moves **into** `Session`. When `Spec.OutPath != ""`, `Start`
opens the `chat.OutSink` and `route` writes every **internal** `chat.Event`
(lossless: keeps `ToolCallID`, system reminders, full untruncated content)
*before* translating to the public `Event`. Lifecycle: `WriteStart` on `Start`
(label = `spec.Prompt` if non-empty, else `"(interactive)"`; mode/model from the
active agent), `WriteEnd` + cleanup on `Close`. The TUI and `RunOnce` stop opening
their own sinks. The audit log stays complete even though the public `Event` is a
lossy projection.

### Interactive-bash wiring

`Spec.ShellInteractive` is stored on `Session` and passed into `NewTurnConfig`
(in `turnConfig()`) to replace the hardcoded `"interactive TTY not available"`
stub. `agentsetup.Build` is **unchanged** — the callback lives entirely in pkg. The TUI supplies a closure that
calls `app.WithReleasedTerminal(...)`. Ordering note: the closure captures a
`var app *patchapp.App` declared **before** `shell3.Start`; `app` is assigned
**after** `Start` returns (using `Snapshot()` for welcome/status info). This is
safe because `ShellInteractive` is only invoked *during a turn* (`Send`), long
after `app` is assigned.

### Headless/env

`cmd/run.go` continues to compute `headless` and call `os.Setenv` for hooks, then
passes `Interactive: !headless` and `OutPath` into the `Spec`. `pkg/shell3` maps
`Spec.Interactive` → `agentsetup.Options.Headless` (inverted) and never sets env.

## Event translation (grown `translate`)

| internal `chat.Event` | public `Event` |
|---|---|
| `EventAssistantToken` | `Token` |
| `EventAssistantReasoning` | `Reasoning` |
| `EventToolCall` | `ToolCall` (+ `ToolCallID`, `IsCustomTool`) |
| `EventToolResult` | `ToolResult` (+ `ToolCallID`) |
| `EventSystemReminder` | `SystemReminder` (**NEW** — was dropped) |
| `EventUsage` | `Usage` |
| `EventTurnDone` | `Done` |
| `EventRetry` | `Retry` |
| `EventError` | `Error` |
| lifecycle / user / assistant-message | dropped (`ok=false`) |

`IsCustomTool` is resolved by `route`/`translate` against the session's current
`CustomToolNames`.

## TUI rewrite (consume pkg)

- `interactive.go`: `RunInteractive(ctx, spec shell3.Spec)`. Build `app` ref →
  `shell3.Start` (with `ShellInteractive` closure) → `Snapshot()` → `patchapp.New`.
  Per user message: `ch := sess.Send(ctx, msg)` (or `SendMessage` for `/image`),
  drained on a background goroutine by the render sink. Busy-gate (`SetBusy`)
  maps onto pkg's single-turn-at-a-time contract.
- Slash commands call `Session` methods: `/clear`→`Clear`, `/rollback`→`Rollback`,
  `/agent`→`SwitchAgent`/`AgentNames`/`ActiveAgent`, `/prune`→`Prune`,
  `/print`→`History`, `/prompt`+`/info`→`Snapshot`, `/parameters`→`Snapshot`+`SetParam`,
  `/image`→`ImageMessage`+`SendMessage`, `/usage`→local tally from `Event` tokens.
- `render.go`: `renderToolCallHeader` reads `ev.ToolCallID`/`ev.IsCustomTool` from
  the public `Event` instead of `cfg`. Drop `chat`/`llm` imports.
- `once.go`: `RunOnce(ctx, spec shell3.Spec)` drives `shell3.Run`/a one-shot
  `Send`, printing public events. Drop `chat` import.

## Testing

- `pkg/shell3`: extend `shell3_test.go` (fakellm-backed) — new `translate` cases
  (`ToolCallID`, `SystemReminder`, `IsCustomTool`), `Snapshot`, `History`
  (prefix-stripping), `Prune`, `SetParam`, `SendMessage`, audit-sink output.
- `internal/tui`: migrate `interactive_test.go` (~670 lines) and `render_test.go`
  to pkg types. **Behavior must stay identical** — this is the riskiest churn.
- Gate: `go build ./...`, `go test ./...`, `go vet ./...` all clean.

## Implementation phases (sequential; hard dependencies)

1. **Grow `pkg/shell3`** — types, `Event` fields, `SystemReminder` kind, `Spec`
   knobs, `SendMessage`/`ImageMessage`, `Snapshot`/`History`/`Prune`/`SetParam`,
   audit sink, `ShellInteractive` stored on `Session` + used in `turnConfig()`.
   `agentsetup` unchanged. Backward-compatible; pkg tests pass. (Largest phase.)
2. **Rewrite `internal/tui`** — `interactive.go`, `once.go`, `render.go` consume
   pkg; drop `chat`+`llm` imports; migrate tui tests.
3. **Rewire `cmd/shell3/run.go`** — build `Spec`, call `tui` with `Spec`; keep
   headless/env logic.
4. **Verify** — full build + `go test ./...` + `go vet`; fix fallout; confirm the
   import-boundary success criterion with `go list`/`grep`.

## Out of scope / YAGNI

- No `example_test.go` in pkg — the TUI *is* the example.
- No new slash commands or behavior changes; strict feature parity.
- No changes to `patchapp`/`patchmd`/`patchtui` internals beyond what the new
  call sites require.
</content>
</invoke>
