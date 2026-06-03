# shell3 Style & Antipattern Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Improve code style across the settled codebase — fix self-inflicted comment damage from the recent refactor, tighten error-handling consistency, remove small redundancies, and add a few low-risk readability refactors — without changing externally-observable behavior except where explicitly called out (one security-relevant guard decision).

**Architecture:** A read-only review (4 parallel agents) already produced findings; this plan is the *vetted, calibrated* subset organized into themed, independently-committable passes. Each task is one commit. Behavioral changes are TDD'd; pure comment/refactor changes are gated by the full verification suite. Large structural refactors are deliberately deferred (Task 9) with rationale.

**Tech Stack:** Go 1.26, module `github.com/weatherjean/shell3`. Tools already installed and clean at baseline: `go vet`, `staticcheck`, `gofmt`, `deadcode`.

---

## Context for the executor (read first)

You have **zero prior context**. Key facts:

1. **Recent history.** Four passes already landed on branch `refactor/internalize-pkgs`: (a) moved `pkg/{chat,llm,persona,applog}` → `internal/`, leaving only `pkg/shell3` public; (b) fixed a turn-ordering bug; (c) removed dead code; (d) gofmt'd the tree. This plan cleans up style *after* those.

2. **The facade.** `pkg/shell3` is the **only** public package. Its `Spec`/`Event`/`Kind`/`Session` expose only stdlib types — do not leak `internal/*` types into it.

3. **Concurrency contract (important — do not "fix" as bugs).** `internal/chat.Session` and `pkg/shell3.Session` rely on a documented contract: the caller MUST drain a turn's events to completion before the next `Send`/`Clear`/`Rollback`/`SwitchModel`. Several review findings flagged "data races" on `s.cfg`/`messages` — these are **contract-guarded**, `go test -race` is clean, and they are NOT active bugs. See Task 8 for the *only* sanctioned change here (documentation/optional hardening), and the Dismissed appendix.

4. **THE GATE.** After every task, all of these must be clean before you commit:
   ```bash
   go build ./...
   go vet ./...
   go test -race ./...                       # all ok, no FAIL
   staticcheck ./...                          # empty
   gofmt -l $(git ls-files '*.go')            # empty
   deadcode -test ./...                       # empty
   ```
   If any is non-empty/failing, fix before committing. The tree is clean at baseline, so any new output is yours.

5. **Pre-existing gofmt note.** The tree was just gofmt'd; keep it gofmt-clean. Do not re-introduce drift.

6. **Commit style.** End each commit message with:
   ```
   Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
   ```

7. **Don't chase the Dismissed findings** (appendix at the end) — they were reviewed and rejected with reasons.

---

## Task 0: Branch setup

**Files:** none (git only)

- [ ] **Step 1: Create the working branch**

```bash
cd /Users/weatherjean/CODE/AGENTS/shell3
git checkout refactor/internalize-pkgs
git checkout -b chore/style-cleanup
```

- [ ] **Step 2: Confirm the gate is green at baseline**

```bash
go build ./... && go vet ./... && go test -race ./... 2>&1 | grep -E 'FAIL|panic' ; \
staticcheck ./... ; gofmt -l $(git ls-files '*.go') ; deadcode -test ./...
```
Expected: no `FAIL`/`panic`, and the last three commands print nothing.

---

## Task 1: Fix self-inflicted comment damage (HIGH confidence, verified)

Two comment defects were introduced by the recent refactor passes.

**Files:**
- Modify: `internal/chat/turn.go` (the `RunTurn` doc block, ~line 66)
- Modify: `docs/headless.md:65,67`

- [ ] **Step 1: Merge the duplicate `RunTurn` doc comment**

The Pass-2 edit left the OLD doc paragraph above the NEW one. Current state (two "RunTurn ..." paragraphs):

```go
// RunTurn executes one user→assistant exchange, emitting chat.Events on
// sess.events. The session's event channel is owned by the caller; teardown
// (close) is the caller's responsibility via sess.CloseEvents().
// RunTurn drives one user→assistant turn, emitting stream events on sess.events.
// beforeDone, if non-nil, runs once at turn teardown immediately before the
// single terminal event (turn_done or error) is emitted — Session.Run uses it
// to persist history. The ordering matters: the terminal event is what embedders
// (pkg/shell3, the TUI) treat as "turn finished, safe to mutate session state",
// so any read of sess.messages in beforeDone must complete before it fires, or
// it races a concurrent SetMessages.
func RunTurn(ctx context.Context, cfg TurnConfig, sess *Session, userMsg llm.Message, beforeDone func()) {
```

Replace that entire comment block (both paragraphs) with the single merged version, preserving the channel-ownership fact from the old one:

```go
// RunTurn executes one user→assistant turn, emitting chat.Events on sess.events.
// The event channel is owned by the caller; teardown (close) is the caller's
// responsibility via sess.CloseEvents().
//
// beforeDone, if non-nil, runs once at turn teardown immediately before the
// single terminal event (turn_done or error) is emitted — Session.Run uses it
// to persist history. The ordering matters: the terminal event is what embedders
// (pkg/shell3, the TUI) treat as "turn finished, safe to mutate session state",
// so any read of sess.messages in beforeDone must complete before it fires, or
// it races a concurrent SetMessages.
func RunTurn(ctx context.Context, cfg TurnConfig, sess *Session, userMsg llm.Message, beforeDone func()) {
```

- [ ] **Step 2: Fix stale `pkg/chat` links in headless.md**

`docs/headless.md` lines 65 and 67 still link to the pre-move paths. Update:
- `pkg/chat/outsink.go` → `internal/chat/outsink.go` (and the `../pkg/chat/outsink.go` href → `../internal/chat/outsink.go`)
- `pkg/chat/event.go` → `internal/chat/event.go` (and href `../pkg/chat/event.go` → `../internal/chat/event.go`)

Verify no other stale refs remain in shipped docs (the `docs/superpowers/plans/*` files are historical records — leave them):
```bash
grep -rnE 'pkg/(chat|llm|persona|applog)\b' docs/headless.md internal/docs/ internal/scaffold/ README.md
```
Expected after fix: empty.

- [ ] **Step 3: Gate + commit**

```bash
go build ./... && go vet ./... && go test -race ./... 2>&1 | grep -E 'FAIL|panic'; staticcheck ./...; gofmt -l $(git ls-files '*.go')
git add internal/chat/turn.go docs/headless.md
git commit -m "$(cat <<'EOF'
docs(chat): de-duplicate RunTurn comment; fix stale pkg/chat links

Pass-2 left two RunTurn doc paragraphs; merge into one, keeping the
channel-ownership note. headless.md still linked pkg/chat/{outsink,event}.go
from before the internal/ move — repoint to internal/chat/.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Error-handling consistency in chat handlers (MED — verify each first)

These were flagged as inconsistent error handling. **Verify the actual contract before changing**, because some "ignored" errors are deliberate (zero-value defaults are valid).

**Files:**
- Investigate/modify: `internal/chat/handler_store.go`, `internal/chat/turn.go`

- [ ] **Step 1: Investigate the `handler.Execute` discarded error (turn.go ~line 227)**

```bash
grep -n 'handler.Execute\|out, _ =\|out, err' internal/chat/turn.go
grep -rn 'func.*Execute(ctx context.Context' internal/chat/*.go | grep -v _test
```
Decision criteria: look at 3–4 `Execute` implementations (handler_bash.go, handler_store.go, handler_docs.go, edit_dispatch path). **If** every handler always returns `nil` error and encodes failures in the output string, then discarding is *correct* — add a one-line comment at the call site documenting that and move on. **If** any handler returns a non-nil error that carries real information, capture and log it:
```go
out, err := handler.Execute(ctx, tc.ID, json.RawMessage([]byte(tc.RawArgs)), toolCfg)
if err != nil {
    cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", err)
}
```

- [ ] **Step 2: Make `storeMemoryList`/`storeHistoryGet` arg-parsing consistent**

In `internal/chat/handler_store.go`, `storeMemoryUpsert` and `storeMemorySearch` check `json.Unmarshal` errors and return them; `storeMemoryList` (~line 70) and `storeHistoryGet` (~line 122) discard with `_ = json.Unmarshal(...)`. Confirm the structs they unmarshal into have valid zero-value behavior. If they do, add a short comment (`// empty/invalid args → list all (zero values are valid)`); if a malformed arg should be a tool error, mirror the checked pattern:
```go
if err := json.Unmarshal(args, &a); err != nil {
    return fmt.Sprintf("error: bad arguments: %v", err), nil
}
```
Pick ONE direction and apply it consistently across all four handlers.

- [ ] **Step 3: Gate + commit**

```bash
# full gate
git add internal/chat/
git commit -m "$(cat <<'EOF'
refactor(chat): consistent tool-handler error handling

Document or surface previously-discarded handler/arg-parse errors so the
four store handlers follow one convention instead of mixing checked and
silently-ignored json.Unmarshal.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Guard fail-closed decision (MED, SECURITY-relevant — TDD)

**File:** `internal/luacfg/dispatch.go` (~lines 40–55, the `OnToolCall` Lua-call error path)
**Test:** `internal/luacfg/dispatch_test.go` (or the existing guard test file)

A guard runs Lua to decide whether to **block** dangerous tool calls. If the Lua call itself errors (bug/corrupt guard), current code returns `DecisionAllow` — it **fails open**. For a safety guard, failing open is the dangerous direction.

- [ ] **Step 1: Confirm the current behavior**

```bash
sed -n '35,60p' internal/luacfg/dispatch.go
grep -n 'DecisionAllow\|DecisionBlock\|DecisionCancel' internal/luacfg/*.go | head
```
Locate the `c.L.CallByParam(...)` error return. Confirm it currently returns the allow decision on error.

- [ ] **Step 2: Write the failing test**

In the guard test file (find it via `ls internal/luacfg/*_test.go`), add a test that registers a guard whose Lua body throws (e.g. `on_tool_call = function() error("boom") end`), drives `OnToolCall`, and asserts the decision is **block**, not allow:
```go
func TestOnToolCall_LuaError_FailsClosed(t *testing.T) {
    // load a config whose guard errors at runtime, then:
    d, reason, err := lc.OnToolCall(context.Background(), "bash", map[string]any{"command": "echo hi"})
    if err != nil {
        t.Fatalf("OnToolCall returned hard error: %v", err)
    }
    if d != luacfg.DecisionBlock {
        t.Fatalf("guard error should fail closed (block), got %v (reason=%q)", d, reason)
    }
}
```
(Mirror the existing guard-test setup in that file for loading a config with a throwing guard.)

- [ ] **Step 3: Run it — expect FAIL** (`go test ./internal/luacfg/ -run TestOnToolCall_LuaError_FailsClosed -v`) → fails because current code returns allow.

- [ ] **Step 4: Change the error path to fail closed**

In `dispatch.go`, on the Lua-call error branch, return block with an explanatory reason instead of allow:
```go
if err := c.L.CallByParam(/* ... */); err != nil {
    return DecisionBlock, "guard execution error: " + err.Error(), nil
}
```

- [ ] **Step 5: Run the test — expect PASS**, then the full gate.

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/
git commit -m "$(cat <<'EOF'
fix(luacfg): guards fail closed on Lua error

An on_tool_call guard that errors at runtime previously returned
DecisionAllow, failing open — a safety guard meant to block dangerous calls
would silently permit them. Return DecisionBlock with the error as the
reason instead. Adds a regression test.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Small readability helpers in turn.go (MED, verified — low risk)

**File:** `internal/chat/turn.go`

- [ ] **Step 1: Extract `isToolError` helper**

A long 4-way `strings.HasPrefix` condition classifies tool output as an error (around line 232, in `emitToolResult(...)`). The error markers are string literals defined elsewhere in the same file ("error:", "USER DENIED", "USER CANCELLED", "Tool-call hook failed"). Extract:
```go
// isToolError reports whether a tool's output string represents a failure,
// by its leading marker. Keep in sync with the markers produced in RunTurn's
// tool-execution loop (validation errors, guard block/cancel, hook failures).
func isToolError(out string) bool {
    return strings.HasPrefix(out, "error:") ||
        strings.HasPrefix(out, "USER DENIED") ||
        strings.HasPrefix(out, "USER CANCELLED") ||
        strings.HasPrefix(out, "Tool-call hook failed")
}
```
Replace the inline condition at the `emitToolResult` call with `isToolError(out)`.

- [ ] **Step 2: Type the guard-decision constants**

The `guardAllow/guardBlock/guardCancel` ints (top of `tools.go`, ~lines 14–20) are bare `int` with a comment that they must match `luacfg.Decision`. Give them a named type so they're self-documenting (keep the values; this is internal, no API impact):
```go
type guardDecision = int // mirrors luacfg.Decision values (0=allow, 1=block, 2=cancel)

const (
    guardAllow  guardDecision = 0
    guardBlock  guardDecision = 1
    guardCancel guardDecision = 2
)
```
(Use a type alias `= int` so existing `int` comparisons keep compiling; verify the build.)

- [ ] **Step 3: Gate + commit**

```bash
git add internal/chat/turn.go internal/chat/tools.go
git commit -m "$(cat <<'EOF'
refactor(chat): extract isToolError; name guard-decision constants

Fold the repeated 4-way HasPrefix tool-error check into one helper that
documents the markers it must track, and give the guard decision ints a
named alias. No behavior change.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: store.go correctness & efficiency (MED — verify, then fix)

**File:** `internal/store/store.go`
**Test:** `internal/store/store_test.go`

- [ ] **Step 1: Make `MemoryUpsert` a single atomic statement**

Around lines 161–171 it does `DELETE FROM memories WHERE key = ?` then a separate `INSERT`. Confirm, then replace with one statement (SQLite supports `INSERT ... ON CONFLICT` given a unique index on `key`, or `INSERT OR REPLACE`). First check the schema for a unique constraint on `key`:
```bash
grep -n 'memories' internal/store/store.go | head
```
If `key` is unique, use upsert:
```go
_, err := s.db.Exec(
    `INSERT INTO memories(key, value, updated_at) VALUES(?,?,?)
     ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
    key, value, now)
```
Add/extend a test asserting a second upsert of the same key overwrites (not duplicates). Run it red→green if the current behavior differs; otherwise it's a refactor guarded by existing tests.

- [ ] **Step 2: Evaluate the `HistorySearchExpr` N+1 (perf, ~lines 451–463)**

The search loop issues one query per result to compute each row's chunk index. Confirm:
```bash
sed -n '440,470p' internal/store/store.go
```
**Decision:** if the per-row query is genuinely inside the results loop, fold the chunk-index computation into the main search query (window function `ROW_NUMBER()`/a JOIN) so it's one round-trip. If this proves intricate, it is acceptable to **defer** with a `// TODO(perf): fold chunk-index lookup into the search query to avoid N+1` and note it in the task's commit message — do NOT leave it silently. Keep existing search tests green either way.

- [ ] **Step 3: Gate + commit** (message documents whichever path you took for Step 2).

---

## Task 6: Missing/weak doc comments on exported identifiers (LOW — quick wins)

Add doc comments where genuinely-exported identifiers lack them. **Verify each is actually exported and undocumented first** (`go doc <pkg> <ident>`); skip any that already have docs. Go needs only ONE package doc per package — do not add a second.

**Files (verify each):**
- `internal/applog/logger.go` — package doc comment (the package has none; add `// Package applog provides rotating file + stderr structured logging.` above `package applog` in exactly one file).
- `internal/patchapp/events.go` — doc the sealed `Event` interface (why sealed, that `event()` prevents external impls).
- `internal/patchapp/app.go` — doc `SubmitFunc` (called on Enter with non-empty input; must be goroutine-safe).
- `internal/patchtui/renderer.go` — doc `CursorMarker` (zero-width marker; one per frame; last wins).
- `internal/chat/handler_bash.go` — doc the exported bash timeout/output-size constants.

- [ ] **Step 1:** For each file above, run `go doc ./<pkg>` to confirm the identifier is exported and undocumented, then add a concise doc comment (one or two lines, starting with the identifier name).
- [ ] **Step 2: Gate + commit** as `docs: add missing doc comments on exported identifiers`.

---

## Task 7: TUI drainTurn readability (MED — optional, low risk)

**File:** `internal/tui/interactive.go`

The `drainTurn` event loop (`for ev := range ch { switch ev.Kind { ... } }`) has 3–4-level nesting inside several cases (notably the fenced-code streaming path). Extract each `case` body into a small method/closure on a per-turn render-state struct so the switch is ~2 levels deep.

- [ ] **Step 1:** Read `drainTurn` fully (`sed -n '172,320p' internal/tui/interactive.go`). Identify the cases with the deepest bodies (assistant token/fence handling, tool result).
- [ ] **Step 2:** Extract those bodies into named helpers that take the event + the existing local render state (streamBuf, reasoningBuf, inFence, etc.). This is a **pure refactor** — no behavior change; the existing TUI tests + manual `make build && ./shell3` smoke must be unaffected.
- [ ] **Step 3: Gate + commit** as `refactor(tui): flatten drainTurn event dispatch into helpers`.
- [ ] **Step 4 (manual):** `make build && ./shell3` and run one real turn to confirm streaming/rendering is visually unchanged (the gate can't catch render regressions).

---

## Task 8: pkg/shell3 public-API contract hardening (LOW — document, optionally guard)

**File:** `pkg/shell3/shell3.go`

The review flagged `SwitchModel`/`Send`/`turnConfig` "races" on `s.cfg`. These are **contract-guarded** (drain before next op; `-race` is clean) and are NOT bugs. The sanctioned change is to make the contract harder to get wrong:

- [ ] **Step 1:** Confirm/strengthen doc comments on `Send`, `Clear`, `Rollback`, `SwitchModel` and the `Session` type stating the single-turn-at-a-time contract explicitly (the caller MUST drain the `Send` channel before any other method). Much of this exists; ensure each method's doc says it.
- [ ] **Step 2 (optional, only if cheap and clean):** Guard the `s.cfg` field mutations in `SwitchModel` and the reads in `turnConfig()` with the existing `s.mu`, so a contract-violating caller degrades to a stale-but-safe read instead of a race. Keep it minimal; do not restructure. If it adds noticeable complexity, skip it and rely on the documented contract.
- [ ] **Step 3:** Verify `Run` guarantees `Close` on ctx cancellation: read `Run` (lines ~284–299). The deferred `s.Close()` runs when the turn channel drains, and the turn ends (with an error event) on cancellation, so the range exits → Close runs. Confirm this by reading; if a path can leak, add a test. Likely **no change needed** — document the reasoning in the commit if so.
- [ ] **Step 4: Gate + commit** as `docs(shell3): make the single-turn-at-a-time contract explicit`.

---

## Task 9: DEFERRED large refactors (do NOT do now — recorded for later)

These are real readability findings but are **large, higher-risk structural changes** that don't fit a style pass and need their own brainstorm/plan. Record them; do not execute here.

- **`RunTurn` is ~200 lines** mixing message assembly, streaming, validation, guard dispatch, handler execution, and persistence. A clean extraction of the tool-execution loop into `executeToolCalls(...) (results, cancelled, err)` would help — but it touches the hottest path and the (recently-fixed) terminal-event ordering. Needs careful, separately-tested work.
- **`agentsetup.Build` is ~170 lines** with three nested closures. Splitting into `resolvePaths → loadConfig → buildClients → assembleConfig` stages is worthwhile but invasive.
- **`patchapp.App` is a 31-field god struct.** Splitting input/render/lifecycle sub-state is a genuine improvement but a big, risky change to the TUI core.

**Action:** add a short `docs/superpowers/notes/refactor-backlog.md` listing these three with file/line pointers, so they're not lost. Commit as `docs: record deferred large refactors`.

---

## Dismissed findings (reviewed and rejected — do not implement)

| Finding | Why dismissed |
|---|---|
| `pkg/shell3` `SwitchModel`/`turnConfig` "data races" rated HIGH | Contract-guarded (drain-before-next-op); `go test -race` clean. Documentation only (Task 8). |
| `internal/chat` "drain goroutine races messages" | The drain goroutine never touches `messages`; misread. |
| `store.go` `defer tx.Rollback()` "unconventional" | This is the idiomatic Go pattern; rollback-after-commit is a no-op. Correct as-is. |
| `internal/llm/params.go` "missing package doc" | A package needs exactly one doc comment; `types.go` already has it. |
| `cmd/shell3/widgets.go` `os.Exit` "in non-main code" | It's in the `main` package of a CLI command; emitting an exit code is correct there. |
| `paths.go` `/tmp` "hardcoded, use os.TempDir()" | Intentional and documented ("so the OS clears it on reboot"); `os.TempDir()` changes that semantic. This tool targets Unix. Leave it. |
| `client.go` explicit `stream.Close()` + deferred close "redundant" | The explicit close orders stream teardown before the `c.tap` traffic snapshot; the defer is the early-return safety net. Intentional. At most, add a one-line comment — not required. |
| `event.go` `emit()` broad `recover()` | Deliberate, documented send-on-closed guard for teardown races. Working as designed. |
| Various micro-optimizations (memoize widget filter, cache `strings.Fields`, sample `Size()` once) | Not hot enough to matter for a terminal UI; premature. Skip unless profiling says otherwise. |

---

## Self-review checklist (done during authoring)

- **Coverage:** every non-dismissed finding from the 4-agent review maps to a task (Tasks 1–8) or the deferred backlog (Task 9).
- **No placeholders:** confirmed-mechanical fixes include exact code; judgment calls include the exact investigation command + decision criteria + the likely fix.
- **Consistency:** `isToolError`, `guardDecision`, `DecisionBlock` names match their defining tasks.
- **Risk ordering:** safest/highest-value first (comment fixes, error consistency, the one security fix), structural refactors deferred.
