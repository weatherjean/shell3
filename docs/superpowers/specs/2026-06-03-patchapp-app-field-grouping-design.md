# patchapp.App: field grouping into sub-state structs — design

**Date:** 2026-06-03
**Status:** approved (brainstorm complete; feeds an implementation plan)
**Scope:** `internal/patchapp/app.go` — the third of three deferred refactors
recorded in `docs/superpowers/notes/refactor-backlog.md`.

## Goal

`App` (`app.go:31–97`) is a ~24-field multi-concern struct that owns input
editing, history recall, status bar, busy/streaming, quit/exit, terminal
lifecycle, paste, and the slash registry — already grouped by comment but all on
one struct. Relocate two cohesive clusters — the **editor** state and the
**terminal/lifecycle** state — into plain unexported sub-structs, cutting `App`
from ~24 fields to ~12. **Pure field-grouping: no externally-observable behavior
change, and no change to locking.** Only field *addresses* change
(`a.cursor` → `a.ed.cursor`).

This is a readability refactor. It is deliberately the most conservative of the
three approaches considered (see "Alternatives not taken"); it adds no methods,
no new locks, and no encapsulation boundary — it organizes fields exactly as the
existing `status statusInfo` field already does.

## Background / constraints

`App` is the top-level TUI controller. It is concurrent: `a.mu` (a
`sync.Mutex`) guards the render state, and the public mutators
(`Print`, `SetStatus`, `SetTokens`, `SetBusy`, `SetContextWindow`, `Refresh`)
are goroutine-safe by locking `a.mu` around their mutations and the `render()`
call. There is a **second, independent** lock, `readMu` (a `sync.RWMutex`),
which gates the stdin `Read` in the input loop so a paused subprocess (nvim,
`!cmd`, a hook) owns the TTY without our reader stealing keystrokes.

**The load-bearing invariant:** `liveFrameLocked()` (`app.go:128–137`) reads
`input`, `cursor`, `busy`, and `status` **together under a single `a.mu`** to
build one consistent frame. Any change that gave those fields separate locks
could tear a frame or reorder lock acquisition (deadlock). Therefore this
refactor changes **no locking whatsoever**: `a.mu` continues to guard exactly
the same fields it does today, merely reached through a sub-struct
(`a.ed.cursor`, `a.term.paused`). `readMu` stays a distinct lock. No lock is
added, removed, renamed, or reordered.

**Precedent:** the struct already contains one such plain field group —
`status statusInfo` (`statusInfo` defined at `status.go:187`). This refactor
extends that exact pattern to two more clusters; `statusInfo` itself is left
untouched.

**Mixed locking in the terminal cluster (must be preserved, not "fixed"):** the
terminal/lifecycle fields do **not** share one lock today, and this refactor
keeps that exactly:

- `readMu` — its own lock (the reader gate).
- `pauseWakeR` / `pauseWakeW` — a self-pipe; assigned once in `Run`
  (`loop.go:34–35`) before the goroutines that read them start, then only
  written to (`Quit`, `Pause`); effectively immutable after `Run` begins.
- `oldTermState` / `paused` — guarded by `a.mu` (set under `a.mu` in
  `Pause`/`Resume`, `lifecycle.go:25–29` / `43–48`; `paused` read under `a.mu`
  in `render()`, `app.go:142`).

Grouping these into one `terminalState` struct groups them by *concern*
(terminal lifecycle), not by lock. That is honest and behavior-preserving —
the mixed locking already exists field-by-field on `App` today; the grouping
only names the cluster and documents the locking. A doc comment on the struct
makes the per-field locking explicit.

**Call sites / API:** `App`'s exported API (`New`, `SetSubmit`, `Quit`,
`Print`, `PrintLine`, `Refresh`, `SetStatus`, `SetContextWindow`, `SetTokens`,
`SetBusy`, `Pause`, `Resume`, `WithReleasedTerminal`, `Run`, and the slash
registration surface) does **not** change. The struct is unexported-field-only;
all field access is within package `patchapp`, so the compiler enforces that
every reference is updated. Methods span `app.go`, `loop.go`, `lifecycle.go`,
`editor.go`, `input.go`, `slash.go`, `render.go`, `status.go`.

**Existing tests** (`internal/patchapp/*_test.go`) are white-box helper tests:
`parseInput`/`processInput` (`input_test.go`), `wrapToWidth`/
`wrapCommittedLines`/`TestBusySetTokensAppliesImmediately` (`render_test.go`),
`renderUserMessage` (`editor_test.go`), `dispatchSlash` (`slash_test.go`), and
input-box rendering (`inputbox_test.go`). The intricate cluster moving into the
new `editorState` with **no** current coverage is the **history-recall state
machine** (`historyIdx` / `historyDraft` / `historyInDraft`, documented at
`app.go:54–64`).

## Design

### `editorState` (new, plain struct, all `a.mu`-guarded)

Holds everything in editor.go's domain. Every field here is guarded by `a.mu`
today and remains so (now as `a.ed.*`):

```go
// editorState is the user-input cluster: the live line, cursor, the up-arrow
// history-recall state machine, the incomplete-UTF8 carry, and bracketed-paste
// buffering. All fields are guarded by App.mu (unchanged from when they lived
// directly on App).
type editorState struct {
	input  []rune
	cursor int

	history        []string
	historyIdx     int
	historyDraft   []rune
	historyInDraft bool

	inputPending []byte // incomplete UTF-8 / control bytes carried between reads

	pasting  bool
	pasteBuf []rune
}
```

### `terminalState` (new, plain struct, mixed locking — documented)

Holds the terminal-lifecycle cluster. The struct carries `readMu` itself (moved
verbatim) and a doc comment spelling out the per-field locking, which is
unchanged:

```go
// terminalState is the terminal/stdin-lifecycle cluster. Its fields do NOT
// share one lock — the locking is unchanged from when they lived on App:
//   - readMu: its own lock, gating the stdin Read so a paused subprocess owns
//     the TTY.
//   - pauseWakeR/W: a self-pipe assigned once in Run before its readers start,
//     then only written; effectively immutable afterward.
//   - oldTermState, paused: guarded by App.mu (set in Pause/Resume; paused read
//     in render()).
type terminalState struct {
	readMu sync.RWMutex

	pauseWakeR *os.File
	pauseWakeW *os.File

	oldTermState *term.State
	paused       bool
}
```

### `App` after grouping (~12 fields)

```go
type App struct {
	mu sync.Mutex

	r *patchtui.Renderer

	ed   editorState
	term terminalState

	status statusInfo // unchanged

	// Busy/streaming. busy is read in liveFrameLocked alongside ed state, so it
	// stays on App with the render path rather than in a side struct.
	busy         bool
	streamCancel context.CancelFunc

	// Quit/exit state.
	lastCtrlC time.Time
	exitFlag  bool

	submit  SubmitFunc
	slash   map[string]*SlashCommand
	welcome WelcomeInfo
}
```

`busy`/`streamCancel` and `lastCtrlC`/`exitFlag` stay top-level on `App`: `busy`
is part of the `a.mu`-guarded render path (read in `liveFrameLocked`), and
splitting two two-field clusters into further structs adds churn without
clarity (YAGNI).

### Mechanical change set

Pure relocation, compiler-enforced:

- Define `editorState` and `terminalState` (placement: alongside the `App` type
  in `app.go`, mirroring where `statusInfo` lives near its users).
- Replace the corresponding `App` fields with `ed editorState` and
  `term terminalState`.
- Rewrite every reference across the package: `a.input`→`a.ed.input`,
  `a.cursor`→`a.ed.cursor`, `a.history*`→`a.ed.history*`,
  `a.inputPending`→`a.ed.inputPending`, `a.pasting`/`a.pasteBuf`→`a.ed.*`;
  `a.readMu`→`a.term.readMu`, `a.pauseWakeR/W`→`a.term.pauseWakeR/W`,
  `a.oldTermState`→`a.term.oldTermState`, `a.paused`→`a.term.paused`.
- `New` (`app.go:101–110`) needs no change for the new structs — their zero
  values are correct (as the fields' zero values are today); it keeps
  initializing `r`, `status`, and `welcome`.
- No method signatures change; no lock operations change.

## Testing

**Phase A — characterize first (added before the grouping, must pass against
current code).** One test for the currently-uncovered, intricate cluster moving
into `editorState`: the up/down-arrow history-recall state machine
(`historyStepBackLocked`, the `keyUp`/`keyDown` cases in `processInput`, and the
draft restore at `editor.go:103–108`).

`TestHistoryRecall` exercises the real state-machine helpers directly (white-box,
in-package — the same access style the existing tests use): construct an `App`,
seed `a.history`, set the relevant fields, then call `a.historyStepBackLocked()`
/ `a.syncDraftLocked()` under `a.mu` and assert the observable result on the
input line and the recall indices. Driving the helpers directly (rather than
synthesizing arrow-key escape sequences) avoids fragile dependence on cursor
position and terminal parsing while pinning exactly the cluster that moves.
Behaviors pinned (all verified against `editor.go:199–244`):

- From an empty live line, the first step-back jumps to the newest history entry
  (`historyIdx == 1`); successive step-backs walk to older entries
  (`historyIdx` 2, 3, …) and **clamp** at the oldest (a further step is a no-op).
- The draft-recovery path: when the live input was cleared but a non-empty draft
  remains (the post-`Escape` state, `input != draft`), the first step-back
  restores the draft (`historyInDraft == true`) before stepping into history.
- `syncDraftLocked` mirrors live input into the draft in live mode
  (`historyIdx == 0 && !historyInDraft`) but leaves the draft untouched while
  navigating history.

Note: the Down arrow only moves the cursor within multiline input — it does
**not** step forward through history (return-to-draft is via `Escape`); the test
does not assert a Down-forward behavior that does not exist.

The test is written in Phase A against the **current** field paths (`a.input`,
`a.history`, `a.historyIdx`, …) so it compiles and passes against the
unmodified, flat `App`, proving today's behavior. In Phase B those field paths
are rewritten mechanically along with every other reference in the package
(`a.input` → `a.ed.input`, etc.); the test's **assertions never change** — only
the addresses do. This is the same characterize-then-extract discipline used for
the two prior refactors, adapted to a field-rename: the pin is the behavior, and
the rename carries the test along with the production code.

**Phase B — group, gated green.** Introduce the two structs, replace the `App`
fields, and rewrite all references (production code + the Phase A test); the full
suite (existing tests + `TestHistoryRecall`) plus the full gate must stay clean.
The grouping preserves logic verbatim; no locking changes.

The locking invariant (single `a.mu` over the render-state set; `readMu`
independent) is not observable through a unit assertion; it is held by
`go test -race`, the existing concurrent tests, and code review.

## Alternatives not taken

- **Deeper decomposition with owned behavior** (an `Editor` type with
  `Insert`/`Backspace` methods, a `Terminal` type owning `readMu` + self-pipe +
  raw mode that `App` composes and delegates to). Genuine encapsulation and the
  long-term "right" shape, but its value comes entirely from *changing the
  locking model* on the hottest concurrent path — exactly the risk the backlog
  deferred. Rejected for this pass; could be a future effort.
- **A third `turnState`/control struct** for `busy`/`streamCancel`/`lastCtrlC`/
  `exitFlag`. Rejected (YAGNI): `busy` belongs with the render path, and the
  remaining two-field clusters don't earn their own type.

## Out of scope

- The other two backlog refactors (`RunTurn` and `agentsetup.Build` — both
  done).
- `statusInfo` and the render/input/slash helper functions — untouched except
  for the mechanical field-path rewrite where they read `App` fields.
- Any behavior or locking change. The concurrency model is preserved exactly.

## Success criteria

- `App` drops from ~24 fields to ~12 top-level fields; `editorState` and
  `terminalState` group the editor and terminal-lifecycle clusters as plain
  structs, mirroring the existing `status statusInfo`.
- The history-recall characterization test exists and passes before and after
  the grouping; the full existing suite passes.
- Full gate clean (`go build`, `go vet`, `go test -race`, `staticcheck`,
  `gofmt -l`, `deadcode`); no externally-observable behavior change; `App`'s
  exported API is untouched; no locking change.
