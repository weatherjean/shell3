# patchapp.App Field-Grouping Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cut `patchapp.App` from ~24 fields to ~12 by relocating the editor and terminal-lifecycle field clusters into two plain unexported structs (`editorState`, `terminalState`), mirroring the existing `status statusInfo` — with no behavior change and no locking change.

**Architecture:** Three phases. **Phase A** (Task 1) adds one characterization test pinning the history-recall state machine — the intricate, currently-uncovered cluster moving into `editorState`. **Phase B** (Tasks 2–3) performs the mechanical field relocation, one struct per task (editor, then terminal), each a self-contained compiling change gated green by the full suite. Task 4 runs the final gate and finishes the branch.

**Tech Stack:** Go 1.26, module `github.com/weatherjean/shell3`. Standard `testing`. Quality tools under `/Users/weatherjean/go/bin`: `staticcheck`, `gofmt`, `deadcode` (plus `go vet`).

---

## Context for the executor (read first)

You have **zero prior context**. Key facts:

1. **Branch.** This is the third of three deferred refactors in `docs/superpowers/notes/refactor-backlog.md` (the first two — `RunTurn` and `agentsetup.Build` — are done and merged to `main`). Create and work on a feature branch off `main`; do NOT commit directly to `main`. Suggested name: `refactor/patchapp-app-fields`.

2. **The design spec** is `docs/superpowers/specs/2026-06-03-patchapp-app-field-grouping-design.md`. Read it. The chosen shape is the most conservative of three approaches considered: plain field-grouping, **no new methods, no new locks, no behavior change**.

3. **THE GATE.** After every task, all of these must be clean before you commit (the tree is clean at baseline; any new output is yours). `staticcheck`/`deadcode`/`gofmt` are under `/Users/weatherjean/go/bin` — add it to `PATH`:
   ```bash
   go build ./...
   go vet ./...
   go test -race ./...                       # all ok, no FAIL/panic
   staticcheck ./...                          # empty
   gofmt -l $(git ls-files '*.go')            # empty
   deadcode -test ./...                       # empty
   ```

4. **The characterization test passes on FIRST write.** Task 1's test documents *current* behavior, so it must PASS against the unmodified `App`. If it fails on first run, your understanding (or the test) is wrong — investigate; do NOT change production code to make it pass.

5. **The load-bearing invariant:** `App.mu` (a `sync.Mutex`) guards the render-state fields — `input`, `cursor`, `busy`, and `status` are read **together** under `a.mu` in `liveFrameLocked()` (`app.go:128–137`). `readMu` (a `sync.RWMutex`) is a **separate** lock gating the stdin read. This refactor moves fields between structs but **must not add, remove, rename, or reorder any lock or lock operation.** Whatever lock guarded a field before guards it after — only the field's address changes (`a.cursor` → `a.ed.cursor`).

6. **This is a pure field-relocation refactor.** It is compiler-enforced: every reference is within package `patchapp` (unexported fields), so `go build` lists every site you must update. The behavior pin is the test suite; the completeness pin is the compiler.

7. **Prefix-collision safety (important for the mechanical replaces).** Some field names are prefixes of others (`input`/`inputPending`; `history`/`historyIdx`/`historyDraft`/`historyInDraft`). This is benign here because **every colliding field moves to the same sub-struct** — e.g. both `a.input` and `a.inputPending` become `a.ed.*`, so even an accidental prefix match yields the correct result. Still, prefer whole-word replacements and let `go build` + `gofmt` + the gate catch any slip. (`a.paused` is NOT a prefix of `a.pauseWakeR/W` — they diverge after `a.pause`.)

8. **Commit style.** End each commit message with:
   ```
   Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
   ```

### Field → destination map (the complete relocation set)

`editorState` (all currently `a.mu`-guarded; refs in `app.go`, `editor.go`):

| current | new |
|---|---|
| `a.input` | `a.ed.input` |
| `a.cursor` | `a.ed.cursor` |
| `a.history` | `a.ed.history` |
| `a.historyIdx` | `a.ed.historyIdx` |
| `a.historyDraft` | `a.ed.historyDraft` |
| `a.historyInDraft` | `a.ed.historyInDraft` |
| `a.inputPending` | `a.ed.inputPending` |
| `a.pasting` | `a.ed.pasting` |
| `a.pasteBuf` | `a.ed.pasteBuf` |

`terminalState` (mixed locking, preserved; refs in `app.go`, `lifecycle.go`, `loop.go`):

| current | new |
|---|---|
| `a.readMu` | `a.term.readMu` |
| `a.pauseWakeR` | `a.term.pauseWakeR` |
| `a.pauseWakeW` | `a.term.pauseWakeW` |
| `a.oldTermState` | `a.term.oldTermState` |
| `a.paused` | `a.term.paused` |

Stays on `App`: `mu, r, status, busy, streamCancel, lastCtrlC, exitFlag, submit, slash, welcome`.

---

## Task 1: Phase A — characterize the history-recall state machine

Add one test pinning the up-arrow history-recall state machine (`historyStepBackLocked`, `syncDraftLocked`, `editor.go:199–244`) — the intricate cluster moving into `editorState`, currently uncovered. It drives the real helpers directly under `a.mu` (white-box, in-package, the same access style the existing tests use).

**Files:**
- Modify: `internal/patchapp/editor_test.go`

- [ ] **Step 1: Append the test**

Add to the END of `internal/patchapp/editor_test.go`. The file is `package patchapp` and already imports `strings`, `testing`, and `patchtui`; this test needs none beyond `testing`, which is present.

```go
// TestHistoryRecall characterizes the up-arrow history-recall state machine
// (historyStepBackLocked) and draft mirroring (syncDraftLocked) — the intricate
// editor cluster with no other coverage. It drives the real helpers directly
// under a.mu, the same white-box style the other patchapp tests use. Pins
// current behavior ahead of moving these fields into editorState.
func TestHistoryRecall(t *testing.T) {
	stepBack := func(a *App) {
		a.mu.Lock()
		a.historyStepBackLocked()
		a.mu.Unlock()
	}

	// Walk newest -> oldest from an empty live line, then clamp at the oldest.
	t.Run("walk and clamp", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.history = []string{"first", "second", "third"}

		stepBack(a)
		if got := string(a.input); got != "third" || a.historyIdx != 1 {
			t.Fatalf("step 1: input=%q idx=%d; want \"third\" idx=1", got, a.historyIdx)
		}
		stepBack(a)
		if got := string(a.input); got != "second" || a.historyIdx != 2 {
			t.Fatalf("step 2: input=%q idx=%d; want \"second\" idx=2", got, a.historyIdx)
		}
		stepBack(a)
		if got := string(a.input); got != "first" || a.historyIdx != 3 {
			t.Fatalf("step 3: input=%q idx=%d; want \"first\" idx=3", got, a.historyIdx)
		}
		stepBack(a) // clamp: no entry older than the oldest
		if got := string(a.input); got != "first" || a.historyIdx != 3 {
			t.Fatalf("clamp: input=%q idx=%d; want \"first\" idx=3 (unchanged)", got, a.historyIdx)
		}
	})

	// Draft-recovery path: input cleared but a non-empty draft remains (the
	// post-Escape state). First step-back restores the draft before history.
	t.Run("draft recovery", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.history = []string{"h1"}
		a.historyDraft = []rune("recovered")
		a.input = a.input[:0] // cleared; draft intact, input != draft

		stepBack(a)
		if got := string(a.input); got != "recovered" || !a.historyInDraft || a.historyIdx != 0 {
			t.Fatalf("recover: input=%q inDraft=%v idx=%d; want \"recovered\" inDraft=true idx=0",
				got, a.historyInDraft, a.historyIdx)
		}
		stepBack(a)
		if got := string(a.input); got != "h1" || a.historyInDraft || a.historyIdx != 1 {
			t.Fatalf("into history: input=%q inDraft=%v idx=%d; want \"h1\" inDraft=false idx=1",
				got, a.historyInDraft, a.historyIdx)
		}
	})

	// syncDraftLocked mirrors live input into the draft, but not while
	// navigating history (historyIdx > 0).
	t.Run("sync draft only in live mode", func(t *testing.T) {
		a := New("test", "", WelcomeInfo{})
		a.input = []rune("abc")
		a.mu.Lock()
		a.syncDraftLocked()
		a.mu.Unlock()
		if got := string(a.historyDraft); got != "abc" {
			t.Fatalf("live sync: draft=%q; want \"abc\"", got)
		}

		a.historyIdx = 1 // now navigating history
		a.input = []rune("changed")
		a.mu.Lock()
		a.syncDraftLocked()
		a.mu.Unlock()
		if got := string(a.historyDraft); got != "abc" {
			t.Fatalf("nav sync: draft=%q; want \"abc\" (unchanged while navigating)", got)
		}
	})
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/patchapp/ -run TestHistoryRecall -v`
Expected: PASS (all three subtests). This characterizes the *current* code. If any subtest fails, do NOT change production code — investigate your understanding of `historyStepBackLocked`/`syncDraftLocked`.

- [ ] **Step 3: Gate + commit**

Run the full GATE (Context #3), then:

```bash
git add internal/patchapp/editor_test.go
git commit -m "$(cat <<'EOF'
test(patchapp): characterize history-recall state machine

Pin historyStepBackLocked (draft -> newest -> older -> clamp; draft recovery)
and syncDraftLocked (mirror in live mode only) before relocating these fields
into editorState. White-box, drives the real helpers under a.mu.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Phase B (1/2) — extract `editorState`

Introduce the `editorState` struct, replace the nine editor fields on `App` with a single `ed editorState`, and rewrite every reference (`app.go`, `editor.go`, and the Task 1 test) per the field→destination map. Pure relocation; no logic, no locking change.

**Files:**
- Modify: `internal/patchapp/app.go`
- Modify: `internal/patchapp/editor.go`
- Modify: `internal/patchapp/editor_test.go`

- [ ] **Step 1: Add the `editorState` type**

In `internal/patchapp/app.go`, immediately ABOVE the `type App struct {` declaration, add:

```go
// editorState is the user-input cluster: the live line, cursor, the up-arrow
// history-recall state machine, the incomplete-UTF8 carry, and bracketed-paste
// buffering. All fields are guarded by App.mu (unchanged from when they lived
// directly on App).
type editorState struct {
	input  []rune
	cursor int

	// Message history for up-arrow recall. history[0] is oldest.
	// historyDraft always mirrors live input (updated on every keystroke);
	// Escape clears input but leaves historyDraft intact so it can be
	// recovered. historyInDraft is true when the user has pressed Up and is
	// viewing the saved draft (one step before entering the history list).
	// historyIdx > 0 means the user is viewing a history entry (1 = most
	// recent); historyIdx is 0 in both live and in-draft modes.
	history        []string
	historyIdx     int
	historyDraft   []rune
	historyInDraft bool

	// Incomplete UTF-8/control-sequence bytes carried between terminal reads.
	inputPending []byte

	// Bracketed paste state.
	pasting  bool
	pasteBuf []rune
}
```

- [ ] **Step 2: Replace the editor fields in `App` with `ed editorState`**

In the `App` struct in `app.go`, DELETE these field lines and their comments (currently `app.go:50–64` and `app.go:77–83`):

```go
	// User input state.
	input  []rune
	cursor int

	// Message history for up-arrow recall. history[0] is oldest.
	// historyDraft always mirrors live input (updated on every keystroke);
	// Escape clears input but leaves historyDraft intact so it can be
	// recovered. historyInDraft is true when the user has pressed Up and is
	// viewing the saved draft (one step before entering the history list).
	// historyIdx > 0 means the user is viewing a history entry (1 = most
	// recent); historyIdx is 0 in both live and in-draft modes.
	history        []string
	historyIdx     int
	historyDraft   []rune
	historyInDraft bool
```

and

```go
	// Incomplete UTF-8/control-sequence bytes carried between terminal reads.
	inputPending []byte

	// Bracketed paste state.
	pasting  bool
	pasteBuf []rune
```

In their place (keep the struct tidy; put `ed` near the top with the other state, e.g. just below the `r *patchtui.Renderer` field) add:

```go
	// User input state (live line, cursor, history recall, paste, UTF-8 carry).
	ed editorState
```

- [ ] **Step 3: Rewrite editor field references in `app.go`**

`app.go` references `a.input` and `a.cursor` only inside `liveFrameLocked` (`app.go:128–137`). Update that function so it reads:

```go
// liveFrameLocked builds the current live frame. Caller must hold a.mu.
func (a *App) liveFrameLocked() []string {
	w, _ := patchtui.Size()
	return buildFrame(w, frameState{
		input:  a.ed.input,
		cursor: a.ed.cursor,
		busy:   a.busy,
		status: a.status,
	})
}
```

- [ ] **Step 4: Rewrite editor field references in `editor.go`**

In `internal/patchapp/editor.go`, replace every occurrence (whole-word) of the editor fields with their `a.ed.*` form per the map: `a.input`→`a.ed.input`, `a.cursor`→`a.ed.cursor`, `a.history`→`a.ed.history`, `a.historyIdx`→`a.ed.historyIdx`, `a.historyDraft`→`a.ed.historyDraft`, `a.historyInDraft`→`a.ed.historyInDraft`, `a.inputPending`→`a.ed.inputPending`, `a.pasting`→`a.ed.pasting`, `a.pasteBuf`→`a.ed.pasteBuf`.

Do NOT touch `a.busy`, `a.streamCancel`, `a.lastCtrlC`, `a.status`, `a.exitFlag`, `a.mu`, `a.slash`, `a.submit`, `a.render()`, or method calls like `a.insertChar`/`a.syncDraftLocked`/`a.historyStepBackLocked`/`a.handleCtrlC`/`a.handleEnter`/`a.dispatchSlash`/`a.Print`/`a.WithReleasedTerminal` — only the bare field accesses listed above.

- [ ] **Step 5: Rewrite editor field references in the Task 1 test**

In `internal/patchapp/editor_test.go` (the `TestHistoryRecall` you added), apply the same replacements: `a.history`→`a.ed.history`, `a.input`→`a.ed.input`, `a.historyDraft`→`a.ed.historyDraft`, `a.historyIdx`→`a.ed.historyIdx`, `a.historyInDraft`→`a.ed.historyInDraft`. The method calls `a.historyStepBackLocked()` / `a.syncDraftLocked()` and `a.mu.Lock/Unlock` stay unchanged. The assertions (the expected values) do NOT change — only field addresses.

- [ ] **Step 6: Build — expect success**

Run: `go build ./...`
Expected: builds clean. If you see `a.input undefined (type *App has no field input)`, you missed a reference — the error names the file:line; fix it. (Do not silence by re-adding the field.)

- [ ] **Step 7: Run the patchapp suite — expect PASS (behavior preserved)**

Run: `go test ./internal/patchapp/ -v`
Expected: all tests PASS, including `TestHistoryRecall` (now reading `a.ed.*`) — proving the relocation preserved behavior.

- [ ] **Step 8: Gate**

Run the full GATE (Context #3). All clean — `gofmt -l` must be empty (re-run `gofmt -w` on the touched files if needed), `staticcheck` and `deadcode` empty.

- [ ] **Step 9: Commit**

```bash
git add internal/patchapp/app.go internal/patchapp/editor.go internal/patchapp/editor_test.go
git commit -m "$(cat <<'EOF'
refactor(patchapp): group editor fields into editorState

Relocate the nine input/cursor/history/paste/UTF8-carry fields off App into a
plain editorState struct (a.ed), mirroring the existing status statusInfo. Pure
field move guarded by App.mu as before; no logic or locking change. The
history-recall characterization test moves to the new field paths unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Phase B (2/2) — extract `terminalState`

Introduce the `terminalState` struct, replace the five terminal/lifecycle fields on `App` with a single `term terminalState`, and rewrite every reference (`app.go`, `lifecycle.go`, `loop.go`). The locking is mixed and **preserved exactly** — a doc comment makes it explicit.

**Files:**
- Modify: `internal/patchapp/app.go`
- Modify: `internal/patchapp/lifecycle.go`
- Modify: `internal/patchapp/loop.go`

- [ ] **Step 1: Add the `terminalState` type**

In `internal/patchapp/app.go`, ABOVE the `type App struct {` declaration (e.g. just below the `editorState` type added in Task 2), add:

```go
// terminalState is the terminal/stdin-lifecycle cluster. Its fields do NOT
// share one lock — the locking is unchanged from when they lived on App:
//   - readMu: its own lock, gating the stdin Read so a paused subprocess owns
//     the TTY without our reader stealing keystrokes / DSR replies.
//   - pauseWakeR/W: a self-pipe assigned once in Run before its readers start,
//     then only written (Quit, Pause); effectively immutable afterward. Used to
//     interrupt the input loop's Poll when Pause is called from another
//     goroutine (SetReadDeadline is unreliable for terminals).
//   - oldTermState, paused: guarded by App.mu (set in Pause/Resume; paused is
//     read in render()).
type terminalState struct {
	readMu sync.RWMutex

	pauseWakeR *os.File
	pauseWakeW *os.File

	oldTermState *term.State
	paused       bool
}
```

- [ ] **Step 2: Replace the terminal fields in `App` with `term terminalState`**

In the `App` struct in `app.go`, DELETE these field lines and their comments (the `readMu` block at `app.go:34–39`, the `pauseWake` block at `app.go:41–46`, and the "Terminal lifecycle" block at `app.go:84–86`):

```go
	// readMu gates the stdin Read in the input loop. Held read-locked only
	// while a Read is in progress; Pause acquires it write-locked to keep
	// the reader out while a subprocess (nvim, !cmd, hook) owns the TTY.
	// Without this gate, our Read steals keystrokes and DSR replies meant
	// for the subprocess.
	readMu sync.RWMutex

	// pauseWake is a self-pipe used to interrupt the input loop's Poll when
	// Pause is called from another goroutine. os.Stdin.SetReadDeadline is
	// unreliable for terminals, so we multiplex stdin with this pipe via
	// unix.Poll and wake by writing a byte. nil before Run starts.
	pauseWakeR *os.File
	pauseWakeW *os.File
```

and

```go
	// Terminal lifecycle.
	oldTermState *term.State
	paused       bool // set during Pause/Resume
```

In their place add (placement: near the other lifecycle/sync state, e.g. just below the `ed editorState` field):

```go
	// Terminal/stdin lifecycle (own readMu lock + self-pipe + raw-mode state).
	term terminalState
```

- [ ] **Step 3: Fix the `app.go` imports**

After Step 2, `app.go` no longer references `sync.RWMutex`, `*os.File`, or `*term.State` at top level — they moved into `terminalState`, which still lives in `app.go`, so the imports are still needed. **Leave the import block unchanged.** (`sync` is still used by `a.mu sync.Mutex` and `terminalState.readMu`; `os` and `golang.org/x/term` are used by `terminalState`.) Confirm with `go build` in Step 6; if `go build` reports an unused import, remove exactly that import — but it should not.

- [ ] **Step 4: Rewrite terminal field references in `app.go`**

`app.go` references two terminal fields:
- `Quit` (`app.go:119–126`) uses `a.pauseWakeW` → change both occurrences to `a.term.pauseWakeW`:
  ```go
  	if a.term.pauseWakeW != nil {
  		_, _ = a.term.pauseWakeW.Write([]byte{0})
  	}
  ```
- `render` (`app.go:141–146`) reads `a.paused` → change to `a.term.paused`:
  ```go
  func (a *App) render() {
  	if a.term.paused {
  		return
  	}
  	a.r.Render(a.liveFrameLocked())
  }
  ```

- [ ] **Step 5: Rewrite terminal field references in `lifecycle.go`**

In `internal/patchapp/lifecycle.go`, replace (whole-word): `a.pauseWakeW`→`a.term.pauseWakeW` (line ~20), `a.readMu`→`a.term.readMu` (lines ~23, ~50), `a.oldTermState`→`a.term.oldTermState` (lines ~26, ~44), `a.paused`→`a.term.paused` (lines ~27, ~45). Leave `a.mu`, `a.r`, `a.render()` unchanged.

- [ ] **Step 6: Rewrite terminal field references in `loop.go`**

In `internal/patchapp/loop.go`, replace (whole-word): `a.oldTermState`→`a.term.oldTermState` (line ~24), `a.pauseWakeR`→`a.term.pauseWakeR` (line ~34), `a.pauseWakeW`→`a.term.pauseWakeW` (line ~35), `a.readMu`→`a.term.readMu` (lines ~71, ~78, ~87, ~91). Leave `a.mu`, `a.r`, `a.exitFlag`, `a.busy`, `a.lastCtrlC`, `a.status`, `a.render()`, `a.processInput`, `a.welcome`, `a.tickerLoop`, `a.winchLoop` unchanged.

- [ ] **Step 7: Build — expect success**

Run: `go build ./...`
Expected: builds clean. If an `a.readMu undefined` (or similar) error appears, you missed a site — fix at the named file:line.

- [ ] **Step 8: Run the patchapp suite + the whole repo — expect PASS**

Run: `go test ./internal/patchapp/ -v` then `go test ./...`
Expected: all PASS. The terminal cluster has no direct unit test (it drives the real TTY), so its safety net is compilation + `go test -race` + the unchanged concurrency tests.

- [ ] **Step 9: Gate**

Run the full GATE (Context #3). All clean. Watch `staticcheck` for any newly-unused import in `app.go` (Step 3) and `deadcode` for reachability.

- [ ] **Step 10: Commit**

```bash
git add internal/patchapp/app.go internal/patchapp/lifecycle.go internal/patchapp/loop.go
git commit -m "$(cat <<'EOF'
refactor(patchapp): group terminal-lifecycle fields into terminalState

Relocate readMu, the pause self-pipe, oldTermState, and paused off App into a
plain terminalState struct (a.term). The fields keep their original, mixed
locking (readMu is its own lock; oldTermState/paused stay under App.mu; the
self-pipe is set once in Run) — documented on the struct. No behavior change.
App is now ~12 fields, down from ~24.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Final verification + finish the branch

**Files:** none (verification + git)

- [ ] **Step 1: Full gate on the final tree**

Run all GATE commands once more; confirm each is clean and `go test -race ./...` shows no FAIL/panic across ALL packages.

- [ ] **Step 2: Confirm App shrank**

Run: `awk '/^type App struct/{f=1} f{print} /^}/{if(f)exit}' internal/patchapp/app.go | grep -cE '^\t[a-z]'`
Expected: ~12 (top-level App fields), down from ~24. (Approximate; the goal is a clear reduction with `ed editorState` and `term terminalState` present.)

- [ ] **Step 3: Confirm no behavior/locking drift**

Run: `git diff main -- internal/patchapp/ | grep -E '^\+' | grep -E '\.Lock\(|\.Unlock\(|\.RLock\(|\.RUnlock\(' | sort | uniq -c`
Then: `git diff main -- internal/patchapp/ | grep -E '^\-' | grep -E '\.Lock\(|\.Unlock\(|\.RLock\(|\.RUnlock\(' | sort | uniq -c`
Expected: the added and removed lock-operation lines differ **only** by the receiver path inside them (e.g. `a.readMu.RLock()` → `a.term.readMu.RLock()`); the count and kind of lock operations must match. Any net added/removed lock is a bug — the refactor must not change locking.

- [ ] **Step 4: Finish**

Invoke the **superpowers:finishing-a-development-branch** skill to verify tests, present integration options (merge to `main` / PR / keep / discard), and execute the choice.

---

## Self-review checklist (done during authoring)

- **Spec coverage:** Phase A covers the one currently-uncovered observable cluster the spec names (history-recall state machine via `historyStepBackLocked`/`syncDraftLocked`). Phase B implements the exact two-struct grouping from the spec (`editorState`, `terminalState`), preserving the single `a.mu` over render state and the independent `readMu`, with the mixed-locking terminal cluster documented. `busy`/`streamCancel`/`lastCtrlC`/`exitFlag` and `status statusInfo` stay on `App` as specified. `App`'s exported API is untouched.
- **No placeholders:** the characterization test is shown verbatim; every relocation is enumerated in the field→destination map and the per-file steps; the cross-cutting functions (`liveFrameLocked`, `render`, `Quit`) and both new struct definitions are shown verbatim; every command has an expected result.
- **Type/name consistency:** `editorState`/`ed` and `terminalState`/`term`, and every field name (`input, cursor, history, historyIdx, historyDraft, historyInDraft, inputPending, pasting, pasteBuf`; `readMu, pauseWakeR, pauseWakeW, oldTermState, paused`) are used identically across the struct definitions, the field→destination map, and the per-file rewrite steps.
- **Behavior/locking preservation:** every step is a verbatim field-address move; Task 4 Step 3 explicitly diffs lock operations to prove none were added or removed; the test assertions are unchanged across the Phase A → Phase B field rename.
- **Prefix-collision safety:** documented in Context #7 — all colliding field names move to the same sub-struct, so replacements are safe; `a.paused` vs `a.pauseWakeR/W` do not collide.
