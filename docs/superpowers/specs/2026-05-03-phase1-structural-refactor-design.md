# Phase 1: Structural Refactor Design

**Date:** 2026-05-03  
**Scope:** Built-in tool dispatch interface, Config decomposition, targeted hardening  
**Excludes:** Logger interface (Phase 2), constants centralization (Phase 2), codex/openai adapter rework (deferred)

---

## Problem Statement

`chat/tools.go` (448 lines) is a monolithic switch statement with no interface contract — each built-in tool handler is an untestable private function. `chat.Config` (11 fields) couples LLM, hooks, store, personality, tools, models, paths, and callbacks into a single struct, making unit testing turn/tool logic impossible without constructing the full stack.

---

## Approach: Interface-First, Incremental Wiring

Define interfaces alongside existing code (no behaviour change), then migrate callers one tool at a time, then split Config. Existing tests stay green at every step.

---

## Section 1: ToolHandler Interface

New file: `internal/chat/toolhandler.go`

```go
type ToolHandler interface {
    Name() string
    Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error)
}
```

- `id`: tool call ID (needed for prune/history lookups)
- `cfg`: stripped-down config (see Section 2)
- Each built-in tool becomes a struct implementing this interface
- Handlers live in `internal/chat/` — one file per handler or grouped by concern:
  - `handler_bash.go`
  - `handler_edit.go`
  - `handler_store.go` (memory, history, compact)
  - `handler_docs.go`
  - `handler_prune.go`
- The existing `switch` in `tools.go` becomes a map lookup: `map[string]ToolHandler`
- Handlers injected at construction time (explicit, no global registry)
- User tool dispatch (`dispatchUserTool()`) is untouched — separate execution path

---

## Section 2: Config Split

`chat.Config` stays as the top-level assembly struct for `RunInteractive`. Internally it constructs two focused sub-structs:

**`TurnConfig`** — passed to `runTurn()`:
```go
type TurnConfig struct {
    LLM         LLMClient
    Hooks        *hooks.Runner
    Session      *Session
    Personality  *persona.Persona
    Handlers     []ToolHandler
    UserTools    []usertools.Tool
    Secrets      *secrets.Store
    Params       llm.RequestParams
}
```

**`ToolConfig`** — passed to `ToolHandler.Execute()`:
```go
type ToolConfig struct {
    Store    *store.Store
    WorkDir  string
    Secrets  *secrets.Store
    Session  *Session
}
```

No external API change — decomposition is internal to the `chat` package.

---

## Section 3: Hardening (same files, bundled in Phase 1)

### Panic Stack Capture (`turn.go`)
```go
defer func() {
    if r := recover(); r != nil {
        stack := debug.Stack()
        err := fmt.Errorf("panic: %v\n%s", r, stack)
        cfg.Hooks.OnError(ctx, err)
        ch <- patchapp.TurnErrEvent{Err: err}
    }
}()
```

### `looksLikeError()` Migration
- Moves into `handler_bash.go` (only bash uses it for result classification)
- Kept as-is in Phase 1; Phase 2 replaces it with explicit error return from `Execute()`

### Bash Context Threading (`handler_bash.go`)
- Remove `bashTimeout = 30 * time.Second` constant
- Use `ctx` from `Execute()` directly — turn context already carries cancellation
- Callers set timeouts before calling `Execute()` if needed

### OpenAI `io.ReadAll` Discards
- Minimal touch: add comment explaining intentional discard (diagnostic buffering, not critical path)
- No structural adapter changes

---

## Section 4: Testing

- New unit tests added immediately after each handler is extracted
- Table-driven tests per `ToolHandler` implementation
- `TurnConfig` constructed with mocks — now possible without full `chat.Config`
- Constraint: existing tests must remain green throughout all steps

---

## Implementation Order

1. Define `ToolHandler` interface and `ToolConfig` struct (no wiring yet)
2. Implement `handler_bash.go` — extract bash handler, wire into map, delete from switch
3. Implement `handler_edit.go` — same pattern
4. Implement `handler_store.go` — memory, history, compact
5. Implement `handler_docs.go`
6. Implement `handler_prune.go`
7. Delete tools.go switch, replace with map dispatch
8. Construct `TurnConfig` in `RunInteractive`, thread through `runTurn`
9. Fix panic recovery in `turn.go`
10. Fix bash context threading
11. Add openAI io.ReadAll comments
12. Run all tests, add new unit tests per handler

---

## Out of Scope (Phase 2 Backlog)

See `docs/superpowers/specs/phase2-backlog.md`.

- Structured logging interface
- Magic constants centralization (`minPruneBytes`, `hookTimeout`, `ChunkSize`, etc.)
- `looksLikeError()` heuristic replacement
- Codex/OpenAI adapter structural rework
- Store session end error handling
- Hook failure visibility
