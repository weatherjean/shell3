# shell3 plugin API — design

**Date:** 2026-06-03
**Status:** Approved (design)
**Branch:** fix/prompt-time-refresh-and-dead-guard

## Problem

`pkg/shell3` is over-shaped and under-useful. Today it exposes `New(Options)
(chat.Config, func(), error)`, which:

- does the hard setup (loads `shell3.lua`, builds the OpenAI adapter, assembles
  persona/tools/guards) — good, but
- hands back a raw `chat.Config`, leaking internal types (`persona.Persona`,
  `llm.RequestParams`, the guard/tool wiring) into the public surface, and
- stops short of *running* anything. To actually drive a turn an embedder must
  reimplement the loop in `internal/tui/once.go` (`RunOnce`), which it cannot
  import.

The result: there is no way to use shell3 as a library/plugin without
copying internal code, and the surface that does exist exposes churn-prone
internals as a public contract.

## Goal

Expose shell3 as a "plugin" for any Go app through **one** function: pass a
prompt, the path to a `shell3.lua` config, and a working directory. It either
returns an error (can't start) or streams structured output back to the caller.

## Public surface (the entire exported API of `pkg/shell3`)

```go
package shell3

// Run loads the config at spec.ConfigPath, starts one turn for spec.Prompt,
// and streams events on the returned channel.
//
// A non-nil error means Run failed to START (bad/missing config, unparseable
// Lua, unknown model, missing key) — nothing ran and the channel is nil.
// A nil error means the turn is underway; per-turn failures arrive as
// Event{Kind: Error} on the channel. The channel is closed exactly once when
// the turn completes (after a final Done event).
func Run(ctx context.Context, spec Spec) (<-chan Event, error)

type Spec struct {
    Prompt     string // required
    ConfigPath string // path to shell3.lua; defaults to ~/.shell3/shell3.lua when empty
    WorkDir    string // cwd for tool execution; defaults to os.Getwd() when empty
}

type Event struct {
    Kind       Kind
    Text       string // assistant tokens (Kind == Token)
    ToolName   string // Kind == ToolResult
    ToolOutput string // Kind == ToolResult
    Err        error  // Kind == Error
}

type Kind int

const (
    Token      Kind = iota // streamed assistant text
    ToolResult             // a tool ran; ToolName/ToolOutput set
    Error                  // non-fatal turn error; Err set
    Done                   // turn finished; channel closes right after
)
```

`New`, `Options`, and the `chat.Config` return all become unexported internals.
As a plugin author, `pkg/chat`, `pkg/persona`, and `pkg/llm` disappear from the
import graph.

### Decisions locked during brainstorming

- **Streaming via event channel** (not `io.Writer`, not callback). It is the
  thinnest wrapper over what already flows internally (`chat.Session.Events()`),
  and preserves structure a plugin author wants (tokens vs tool output vs
  errors). A writer can be built on top of a channel; structure can't be
  recovered once flattened.
- **Slim public `Event`** (not a re-export of `chat.Event`). A minimal,
  translated type gives a stable public contract so internals can churn without
  breaking embedders.
- **`Run` is the single entrypoint** (not `Run` + kept `New`). The leaky
  `New`/`chat.Config` surface is removed. Maximum simplification, matching
  "expose a single function."

## Internals & data flow

`Run` reuses existing machinery; this is relocation, not new logic.

```
Run(ctx, spec)
  │
  ├─ buildConfig(spec)         ← today's New() body, now unexported
  │     loads shell3.lua, builds OpenAI adapter, persona, tools, guards
  │     returns (chat.Config, closeLua func, error)
  │     ⟵ if this errors, Run returns (nil, err) — nothing started
  │
  ├─ sess := chat.NewSession(...)
  ├─ tc := turnConfig(cfg)     ← the TurnConfig assembly from once.go, moved here
  │
  └─ go {
        sess.Run(ctx, tc, spec.Prompt)
        sess.CloseEvents()
        closeLua()             ← Lua state torn down when the turn ends
     }
     return translate(sess.Events()), nil
        ↑ adapter goroutine maps chat.Event → shell3.Event, drops internal kinds
```

The current `pkg/shell3.New` body becomes the unexported `buildConfig`, and the
session-driving loop (modeled on `internal/tui/once.go:RunOnce`) is reproduced
inside an unexported `runConfig` that translates `chat.Event` → public `Event`.

**Adjustment (post-spec, during planning):** the CLI's `RunOnce` is left
untouched rather than rewritten to call `Run`. `cmd/shell3/run.go` builds a
*rich* `chat.Config` (store persistence, docs-tool content, `/model` switching,
`RefreshPrompt`) and hands it to `RunOnce`; routing that through `Run(Spec)` —
which intentionally builds a minimal config — would regress those CLI features.
The avoided duplication is only a ~10-line goroutine loop, not worth a
regression. All changes are therefore isolated to `pkg/shell3`.

Headless behavior is always on for embedders (no TTY): `shell_interactive`
returns its "not available" string and the headless system-reminder is injected,
exactly as `once.go` does today.

## Event mapping (`chat.EventKind` → public `Kind`)

| internal `chat.EventKind` | public `Kind` |
|---|---|
| `EventAssistantToken` | `Token` |
| `EventAssistantReasoning` | *dropped* |
| `EventToolResult` | `ToolResult` |
| `EventError` | `Error` |
| `EventTurnDone` | `Done` |
| `EventToolCall` | *dropped* |
| `EventRetry` | *dropped* |
| `EventUsage` | *dropped* |
| `EventSessionStart` / `EventSessionEnd` | *dropped* |
| `EventUserMessage` | *dropped* |
| `EventAssistantMessage` | *dropped* |
| `EventSystemReminder` | *dropped* |

**Reasoning tokens are dropped by default** (approved): a plugin embedding
shell3 usually wants final answer text, not chain-of-thought. This can be
surfaced later as a distinct `Kind` without breaking existing consumers (new
const appended).

## Error handling

- **Failed-to-start** (bad path, unparseable Lua, unknown model, missing key) →
  `Run` returns `(nil, error)`; no goroutine, no channel. The caller's
  `if err != nil` is the "can't start" branch.
- **Mid-turn failure** → delivered as `Event{Kind: Error, Err: ...}` on the
  channel; the turn still drains to `Done` and the channel closes.
- **Cancellation** → `ctx` cancellation propagates through `sess.Run` as today.

## Lifecycle / cleanup

The Lua state is owned entirely by `Run` — closed inside the goroutine after
`sess.CloseEvents()`. There is no caller-facing cleanup func and no leak. The
caller's only obligation is to drain (range over) the channel until it closes.

## Testing

- `TestRun_BadConfig_Errors` — preserves the existing `TestNew_NoAuth_Errors`
  semantics: a config dir with no auth returns a non-nil error and a nil
  channel.
- `TestRun_StreamsToDone` — using `pkg/llm/fakellm`, drive a canned stream and
  assert the public channel yields `Token…` then `Done`, then closes.
- `TestRun_MapsToolResult` — fake a tool call and assert exactly one
  `ToolResult` event with `ToolName` / `ToolOutput` populated.

## Out of scope (YAGNI)

- `io.Writer` / callback convenience wrappers (`RunText`, `OnEvent`). Trivial to
  add on top of the channel later if a concrete need appears.
- Surfacing reasoning, tool-call (pre-result), usage, or retry events. Append
  new `Kind` constants when needed.
- Multi-turn / conversational embedding. `Run` is single-turn by design.
