# Deferred large refactors

These are genuine readability findings from the 2026-06-03 style/antipattern
review that were **deliberately deferred** out of the style-cleanup pass: each
is a large, higher-risk structural change that needs its own brainstorm/plan
rather than a drive-by edit. Recorded here so they are not lost.

Line numbers are as of branch `chore/style-cleanup` and will drift.

## 1. `RunTurn` is too long (~225 lines)

- **Where:** `internal/chat/turn.go:76` (`func RunTurn`, lines 76–300).
- **What:** One function mixes message assembly, streaming, tool-call
  validation, guard dispatch, handler execution, and history persistence.
- **Proposed shape:** Extract the tool-execution loop into something like
  `executeToolCalls(...) (results, cancelled, err)`.
- **Why deferred:** It is the hottest path and owns the recently-fixed
  terminal-event ordering (the single `turn_done`/`error` event that embedders
  treat as "safe to mutate session state"). A clean extraction needs its own
  tests around that ordering, not a style pass.

## 2. `agentsetup.Build` is too long (~177 lines)

- **Where:** `internal/agentsetup/agentsetup.go:40` (`func Build`, lines 40–216).
- **What:** ~177 lines with several nested closures doing path resolution,
  config loading, client construction, and config assembly inline.
- **Proposed shape:** Split into stages — `resolvePaths → loadConfig →
  buildClients → assembleConfig`.
- **Why deferred:** Worthwhile but invasive; it is the single wiring point for
  the whole agent and touches startup ordering.

## 3. `patchapp.App` is a large multi-concern struct (~24 fields)

- **Where:** `internal/patchapp/app.go:31` (`type App struct`, lines 31–97).
- **What:** ~24 fields spanning input editing, history recall, status bar,
  busy/streaming, quit/exit, terminal lifecycle, paste, and slash registry —
  already grouped by comment but all on one struct.
- **Proposed shape:** Split the input/render/lifecycle sub-state into their own
  types.
- **Why deferred:** A genuine improvement but a big, risky change to the TUI
  core; the existing comment groupings make the current form tolerable.
