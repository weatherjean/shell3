# RunTurn: extract the tool-execution loop — design

**Date:** 2026-06-03
**Status:** approved (brainstorm complete; feeds an implementation plan)
**Scope:** `internal/chat/turn.go` — the first of three deferred refactors recorded in
`docs/superpowers/notes/refactor-backlog.md`.

## Goal

`RunTurn` is ~225 lines (`turn.go:76–300`) mixing message assembly, streaming,
usage accounting, the round loop, and a ~108-line tool-execution loop. Extract
the **tool-execution loop** into a single helper, `executeToolCalls`, so
`RunTurn` drops to ~120 lines and reads as: assemble → loop { stream → record →
*execute tools* → reminder }. **No externally-observable behavior changes.**

This is a readability refactor, not a redesign. The deeper per-call splits
(decide vs. dispatch) and preamble extraction are explicitly **out of scope**
(see below).

## Background / constraints

The tool-execution loop is the hottest path and has three properties any
extraction MUST preserve:

1. **`allMsgs` is appended to *and* wholesale-reassigned.** `compact_history`
   does `out, allMsgs = handleCompactHistory(...)` (turn.go:210), replacing the
   slice. So `allMsgs` must flow into the helper and back out.
2. **Two non-normal exits with a precedence rule.** A guard `cancel` decision
   makes `RunTurn` emit a cancellation system-reminder + `turn_done`. A
   mid-loop `ctx` cancellation makes `RunTurn` emit `error` and return.
   **Guard-cancel takes precedence over ctx-cancel** in the current post-loop
   ordering (turn.go:277 checks `cancelled` before turn.go:284 checks
   `ctx.Err()`). The helper cannot `return` out of `RunTurn`, so it must signal
   these two outcomes distinctly, preserving that precedence.
3. **Terminal-event ordering stays in `RunTurn`.** The single `terminalEmit`
   closure and the deferred teardown (panic-recover → `beforeDone` →
   `terminalEmit`) — the ordering invariant pinned by
   `TestRun_PersistsHistoryBeforeTurnDone` — remain wholly owned by `RunTurn`.
   The helper only computes results and emits **per-tool** events
   (`emitToolCall`/`emitToolResult`), never the terminal event.

**Test gap.** No test currently calls `RunTurn` directly; the tool loop
(guard-cancel + synthetic stubs, `compact_history`, mid-loop ctx-cancel,
multi-round tool calls) is uncovered. `fakellm` supports multi-round scripts
(one `Script` per stream round) and `TestRun_PersistsHistoryBeforeTurnDone`
demonstrates the `fakellm` → `NewSession` → `sess.Run` → read `sess.Events()`
harness, so this behavior is characterizable.

## Design

### Helper signature (Approach A: result struct + error)

```go
// toolLoopOutcome reports how a turn's tool-execution loop ended.
type toolLoopOutcome struct {
    allMsgs      []llm.Message // updated message slice (compact_history may have replaced it)
    cancelled    bool          // a guard returned a cancel decision
    cancelReason string        // reason text for the cancellation reminder
}

// executeToolCalls runs the assistant's tool calls in order, emitting
// tool_call/tool_result events and appending each tool message to both allMsgs
// and the session. It returns the updated allMsgs plus cancellation state.
//
//   - returned error != nil  → context was cancelled mid-loop; the caller emits
//     an error terminal event and ends the turn.
//   - outcome.cancelled      → a guard cancelled; the caller emits the
//     cancellation reminder and a turn_done terminal event.
//   - otherwise              → normal completion; the caller continues the
//     round loop with outcome.allMsgs.
//
// Guard-cancel takes precedence over ctx-cancel: if a guard cancels, the
// function returns {cancelled:true}, nil without consulting ctx afterward.
func executeToolCalls(
    ctx context.Context,
    cfg TurnConfig,
    sess *Session,
    toolCalls []llm.ToolCall,
    toolSchemas map[string]map[string]any,
    allMsgs []llm.Message,
) (toolLoopOutcome, error)
```

The helper contains the current body of lines 168–275 verbatim in logic: the
per-call `ctx.Err()` guard, `emitToolCall`, guard dispatch, arg validation, the
by-name execution chain (`compact_history` / `shell_interactive` / custom tool /
handler / unknown), `emitToolResult`, the `[tool_call_id=…]` tool-message
append, and the synthetic-stub fill on cancel. On a mid-loop `ctx.Err()` it
returns `toolLoopOutcome{}, ctx.Err()`. On guard-cancel it appends the stubs,
breaks, and returns `{allMsgs, cancelled:true, cancelReason}, nil`.

### What `RunTurn`'s round loop becomes

Replacing lines 168–288, the call site reads:

```go
outcome, err := executeToolCalls(ctx, cfg, sess, toolCalls, toolSchemas, allMsgs)
if err != nil {
    msg := err.Error()
    terminalEmit = func() { emitError(sess, msg) }
    return
}
allMsgs = outcome.allMsgs
if outcome.cancelled {
    emitSystemReminder(sess, "[turn cancelled by user: "+outcome.cancelReason+"]")
    u := totalUsage
    terminalEmit = func() { emitTurnDone(sess, u.PromptTokens, u.CompletionTokens, u.TotalTokens) }
    return
}
// reminder check before next round (unchanged)
```

The helper's final `ctx.Err()` check subsumes the old post-loop check
(turn.go:284–288); because guard-cancel returns early with a nil error, its
precedence over ctx-cancel is preserved.

### File placement

`executeToolCalls` and `toolLoopOutcome` live in `turn.go`, alongside
`streamOnce` and `isToolError` (cohesive turn logic). No new file.

## Testing

**Phase A — characterize first (lands before any extraction).** Add
`fakellm`-driven tests that pin current `RunTurn`/`Session.Run` behavior:

1. **Tool round-trip:** round 1 scripts one tool call (a real handler, e.g.
   `memory_upsert` or a stub handler), round 2 scripts plain text → assert the
   `tool_call`/`tool_result` events fire, the tool message is appended, and
   `turn_done` ends the turn.
2. **Guard-cancel + synthetic stubs:** a `ToolGuard` returning cancel on a turn
   with ≥2 tool calls → assert the cancellation reminder, that unreached calls
   get "Not executed — turn cancelled" stub tool messages with matching
   `tool_call_id`s, and `turn_done` (not error).
3. **Mid-loop ctx cancel:** cancel the context so the loop's `ctx.Err()` check
   trips → assert an `error` terminal event.
4. **compact_history:** (if cheaply scriptable) a `compact_history` call →
   assert `allMsgs` replacement does not corrupt the subsequent round.

These tests assert behavior through the public event/message surface, so they
remain valid before *and* after the extraction.

**Phase B — extract, gated green.** Extract `executeToolCalls`; Phase A tests
plus the full gate (`go build`, `go vet`, `go test -race`, `staticcheck`,
`gofmt -l`, `deadcode`) must stay clean. The extraction is a pure structural
move; the Phase A tests prove behavior preservation across the cut.

## Out of scope (deferred / not in this pass)

- Splitting each call's body into `classifyGuard` (decision) + `executeTool`
  (dispatch). A reasonable future step, but more new surface than this pass wants.
- Extracting the message-assembly preamble (lines 96–118) or round bookkeeping.
- The other two backlog refactors (`agentsetup.Build`, `patchapp.App`).
- Any behavior change, including the `compact_history`/`allMsgs` coupling, which
  is preserved exactly.

## Success criteria

- `RunTurn` shrinks to ~120 lines; the tool loop lives in `executeToolCalls`.
- Phase A characterization tests exist and pass before and after the extraction.
- Full gate clean; no externally-observable behavior change.
