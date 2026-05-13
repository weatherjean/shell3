# Phase 2b: Extract TUI to internal/tui — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development. Dispatch one subagent per task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move all TUI-only code out of `internal/chat` into a new `internal/tui` package. After this phase, `chat.RunInteractive` and `chat.RunOnce` no longer exist; callers use `tui.Run` and `tui.RunOnce`. `internal/chat` still imports `patchapp`/`patchtui` for `turn.go`'s render-event bridge and outsink ANSI stripping — Phase 2c removes those.

**Architecture:** Whole-file moves where possible. Files with mixed concerns (chat.go) get split. Exports added to chat only where a moved symbol needs to reach back. No partial rewrites — preserve behavior, change locations.

**Tech Stack:** Same as before.

---

## File Move Map

| Current location | New location | Notes |
|------------------|--------------|-------|
| `internal/chat/model_picker.go` | `internal/tui/model_picker.go` | Pure TUI; package rename only |
| `internal/chat/edit_dispatch.go` | `internal/tui/edit_dispatch.go` | Pure render formatting |
| `internal/chat/chat.go:RunInteractive,drainTurn,launchTurn,registerSlashCommands,slashTarget,toolNames,currentParamValue,pruneLastTurn` | `internal/tui/interactive.go` | Big extract |
| `internal/chat/chat.go:RunOnce` | `internal/tui/once.go` | Headless CLI |
| `internal/chat/chat.go` (remains) | `internal/chat/chat.go` | Config, LLMClient, ModelChoice, contextWindowFor, NewHandlers, openSink, outSink, logOrNoop, splitStatus |

## Symbols Exported From chat

These become public (capitalize first letter, no other change) so `internal/tui` can call them:

- `splitStatus` → `SplitStatus`
- `contextWindowFor` → `ContextWindowFor`
- `logOrNoop` → `LogOrNoop`
- `openSink` → `OpenSink`
- `outSink` → `OutSink` (struct), `OutSink.WriteEvent`, `OutSink.WriteChatEvent`, `OutSink.WriteStart`, `OutSink.WriteEnd` (already capitalized)
- `runTurn` → `RunTurn` (called from tui's launchTurn goroutine)
- `pruneLastTurn` → `PruneLastTurn` (slash command)

Stays unexported (consumed only by moved code, becomes tui-internal after move):
- `toolNames`, `currentParamValue`, `slashTarget`, `drainTurn`, `launchTurn`, `registerSlashCommands` — these move whole; in `tui` they remain lowercase

---

## Task 2b.1: Create internal/tui Skeleton + Move model_picker.go

**Subagent prompt template:**
> Move `internal/chat/model_picker.go` to `internal/tui/model_picker.go`. Update package declaration from `package chat` to `package tui`. The file references `ModelChoice` (currently exported from chat). Add `import "github.com/weatherjean/shell3/internal/chat"` and change `ModelChoice` references to `chat.ModelChoice`. The `terminalReleaser` interface and `pickModel` function stay private. After move, `internal/chat/model_picker.go` no longer exists. Update callers: only `internal/chat/chat.go` calls `pickModel` (inside `registerSlashCommands`). Leave that callsite broken — Task 2b.3 fixes it. Run `go build ./internal/tui` to confirm tui package compiles. Run `go build ./internal/chat` — expect failure at the `pickModel` callsite. Report the exact build error so 2b.3 knows what to repair. Commit: `refactor(tui): move model_picker from chat to tui`.

**Acceptance:**
- [ ] `internal/tui/model_picker.go` exists, package `tui`
- [ ] `internal/chat/model_picker.go` deleted
- [ ] `go build ./internal/tui` succeeds
- [ ] `go build ./internal/chat` fails ONLY at `pickModel` callsite (caller in chat.go)
- [ ] Test suite NOT expected to pass until 2b.3

## Task 2b.2: Move edit_dispatch.go

**Subagent prompt template:**
> Move `internal/chat/edit_dispatch.go` to `internal/tui/edit_dispatch.go`. Change package declaration to `package tui`. The file's exported symbols: search for `^func [A-Z]` in the file. If `colorizeEditOutput` (or similar) is called from chat code, leave the callsite broken — fixed in 2b.3. Imports: `edittool` (chat path), `patchtui`. Both fine in tui package. Run `go build ./internal/tui`. Run `grep -n "colorizeEditOutput\|summarizeEditArgs\|handleEditTool" internal/chat/` to find callers. Report all broken callsites. Commit: `refactor(tui): move edit_dispatch from chat to tui`.

**Note:** `handleEditTool` may be called from `internal/chat/handler_edit.go` — that handler is a chat-side tool handler, not TUI render. If `handleEditTool` is logic (does the edit) and only the *formatting* parts (`colorizeEditOutput`) are TUI, split: logic stays in chat, formatting moves to tui. Inspect `edit_dispatch.go` carefully before moving. If split is needed, do the split and report.

**Acceptance:**
- [ ] Pure-render parts of edit_dispatch in `internal/tui/`
- [ ] Pure-logic parts (if any) stay in `internal/chat/`
- [ ] `go build ./internal/tui` succeeds
- [ ] Broken callsites in chat documented for 2b.3

## Task 2b.3: Export Required chat Symbols

**Subagent prompt template:**
> In `internal/chat/`, rename these private symbols to exported (capital first letter), updating ALL callsites within `internal/chat/`:
>
> - `splitStatus` → `SplitStatus`
> - `contextWindowFor` → `ContextWindowFor`
> - `logOrNoop` → `LogOrNoop`
> - `openSink` → `OpenSink`
> - `outSink` (struct) → `OutSink`
> - `runTurn` → `RunTurn`
> - `pruneLastTurn` → `PruneLastTurn`
>
> Do NOT rename anything else. Do NOT move files. Mechanical rename only.
>
> Use this command first to confirm zero collisions with existing exports:
> ```bash
> for s in SplitStatus ContextWindowFor LogOrNoop OpenSink OutSink RunTurn PruneLastTurn; do
>   grep -rn "\\b$s\\b" internal/chat/ --include="*.go" | grep -v "_test.go" && echo "COLLISION: $s"
> done
> ```
> If any collision found, stop and report.
>
> Approach: use `perl -i -pe` with explicit patterns per symbol. Word-boundary `\b` works in perl, not BSD sed. Example:
> ```bash
> perl -i -pe 's/\bsplitStatus\b/SplitStatus/g' internal/chat/*.go
> ```
>
> After rename:
> - `go test ./internal/chat/...` must PASS
> - `go build ./internal/chat` must succeed
> - `go build ./internal/tui` will still fail (Task 2b.4 moves files that need these symbols) — that's OK
>
> Commit: `refactor(chat): export symbols needed by tui package`.

**Acceptance:**
- [ ] All 7 symbols capitalized
- [ ] All chat-internal callsites updated
- [ ] `go test ./internal/chat/...` passes
- [ ] No other code changed

## Task 2b.4: Move RunInteractive + drainTurn + Slash Commands

**Subagent prompt template:**
> Extract from `internal/chat/chat.go` into `internal/tui/interactive.go`:
>
> - `RunInteractive` function (currently at line 99-ish, ~200 lines)
> - `drainTurn` function
> - `slashTarget` interface
> - `registerSlashCommands` function and any helpers it uses (`toolNames`, `currentParamValue`)
>
> Place them in `internal/tui/interactive.go` with `package tui`. Add imports as needed (chat, patchapp, patchtui, patchmd, llm, hooks, persona, store, usertools, applog, context, fmt, sync, time, os, etc — copy from chat.go's import block).
>
> Update references inside the moved code:
> - `Config` → `chat.Config`
> - `Session` → `chat.Session`
> - `NewSession` → `chat.NewSession`
> - `Event`, `EventKind`, `EventSessionStart` etc → `chat.Event`, `chat.EventKind`, `chat.EventSessionStart` etc
> - `emitSessionStart`, `emitSessionEnd` (private) — these can't be called from tui. Replace with public alternative: chat needs to expose a way to emit session boundaries from outside. **Solution**: add `chat.EmitSessionStart(s *chat.Session, meta map[string]string)` and `chat.EmitSessionEnd(s *chat.Session, status string)` as exported wrappers around the private emit helpers. Add these to `internal/chat/event.go`.
> - `NewHandlers` → `chat.NewHandlers`
> - `TurnConfig` → `chat.TurnConfig`
> - `runTurn` → `chat.RunTurn` (already exported in 2b.3)
> - `pickModel` → `pickModel` (private in tui package, accessible since same package)
> - `SplitStatus`, `ContextWindowFor`, `LogOrNoop`, `OpenSink`, `OutSink`, `PruneLastTurn` → `chat.SplitStatus`, etc.
> - `pruneLastTurn` callsite → `chat.PruneLastTurn`
> - `Session.events` field access (in shutdown defer `close(sess.events)`) → use `Session.Events()` for read, but close is a write op needing the raw channel. **Solution**: add `chat.CloseSessionEvents(s *chat.Session)` exported helper in `internal/chat/session.go` that closes the channel.
> - `Session.id` field access — already covered by `Session.ID()` accessor.
>
> Delete the moved functions from `internal/chat/chat.go`. Leave Config, LLMClient, ModelChoice, NewHandlers, RunOnce (Task 2b.5 handles), openSink/outSink (already-exported `OpenSink`/`OutSink`), logOrNoop (now `LogOrNoop`), splitStatus (now `SplitStatus`), contextWindowFor (now `ContextWindowFor`), runTurn (now `RunTurn`).
>
> After moves:
> - `go build ./internal/chat` succeeds
> - `go build ./internal/tui` succeeds
> - `go build ./cmd/shell3` FAILS — `cmd/shell3/run.go` still calls `chat.RunInteractive` which no longer exists. That's expected. Task 2b.6 fixes cmd.
> - `go test ./internal/chat/...` passes
>
> Commit: `refactor(tui): move RunInteractive + drainTurn + slash commands to tui package`.

**Acceptance:**
- [ ] `internal/tui/interactive.go` contains the four moved symbols
- [ ] `internal/chat/chat.go` no longer contains them
- [ ] `chat.EmitSessionStart`, `chat.EmitSessionEnd`, `chat.CloseSessionEvents` added
- [ ] `go build ./internal/chat` + `go build ./internal/tui` succeed
- [ ] `go test ./internal/chat/...` passes

## Task 2b.5: Move RunOnce

**Subagent prompt template:**
> Extract `RunOnce` from `internal/chat/chat.go` into `internal/tui/once.go` (package `tui`). Rename to `tui.RunOnce` (no change to public name; just lives in tui now). Update references the same way as Task 2b.4 — chat-prefix unexported helpers, use `chat.RunTurn` etc.
>
> `RunOnce` uses `patchapp.Event`, `patchapp.ChunkEvent`, `patchapp.AppendEvent`, `patchapp.TurnErrEvent`, `patchapp.TurnDoneEvent`, `patchapp.TTYExecEvent`, `os.Stderr`, `fmt.Print`. All fine in tui package.
>
> After move:
> - `go build ./internal/chat` succeeds
> - `go build ./internal/tui` succeeds
> - `go build ./cmd/shell3` still fails at any `chat.RunOnce` callsite — task 2b.6 fixes.
>
> Commit: `refactor(tui): move RunOnce to tui package`.

**Acceptance:**
- [ ] `internal/tui/once.go` contains `RunOnce`
- [ ] `internal/chat/chat.go` no longer contains `RunOnce`
- [ ] chat builds, tui builds

## Task 2b.6: Update cmd/shell3 Callers

**Subagent prompt template:**
> In `cmd/shell3/`, find every call to `chat.RunInteractive` or `chat.RunOnce`:
> ```bash
> grep -rn "chat.RunInteractive\|chat.RunOnce" cmd/shell3/
> ```
> Replace each with `tui.Run` and `tui.RunOnce` respectively. Add `"github.com/weatherjean/shell3/internal/tui"` to imports. Keep the existing `chat` import (still needed for `chat.Config`, etc.).
>
> **Naming nit:** task 2b.4 placed the function in `internal/tui/interactive.go` but you might find it named `RunInteractive` or `Run`. Use whatever name the file uses — verify with `grep -n "^func " internal/tui/interactive.go`.
>
> After update:
> - `go build ./...` succeeds
> - `go test ./...` PASSES (full suite)
>
> Commit: `refactor(cmd): call tui.Run/tui.RunOnce instead of chat.*`.

**Acceptance:**
- [ ] All callsites updated
- [ ] Full build clean
- [ ] Full test suite passes

## Task 2b.7: Phase 2b Verification

**Subagent prompt template:**
> Verify Phase 2b end state. Run:
>
> 1. `go test ./...` — expect all PASS
> 2. `go build ./cmd/shell3` — expect success
> 3. `go vet ./...` — expect no errors
> 4. `grep -l "patchapp\|patchtui\|patchmd\|patchwidgets" internal/chat/*.go` — expected output (residual coupling, fixed in 2c):
>    - `internal/chat/chat.go` — likely zero matches after moves
>    - `internal/chat/turn.go` — still imports patchapp (writes patchapp.Event); patchtui (ANSI color codes)
>    - `internal/chat/outsink.go` — still imports patchapp (legacy WriteEvent path) + patchtui (StripANSI)
>    - `internal/chat/*_test.go` — may still reference
> 5. Confirm new files exist:
>    - `internal/tui/interactive.go`
>    - `internal/tui/once.go`
>    - `internal/tui/model_picker.go`
>    - `internal/tui/edit_dispatch.go` (if 2b.2 moved it whole; else partial)
>
> Report results. No commit needed unless cleanup warranted.

**Acceptance:**
- [ ] All tests pass
- [ ] Build clean
- [ ] go vet clean
- [ ] Residual TUI imports limited to `turn.go`, `outsink.go`, tests — these are 2c's job

---

## Self-Review

- ✅ Spec coverage: TUI orchestration code (RunInteractive, drainTurn, slash, model picker, edit dispatch render, RunOnce) all moves to `internal/tui`. Phase 2b goal achieved modulo turn.go/outsink residual (deferred to 2c).
- ✅ Placeholder scan: each subagent prompt has explicit commands and acceptance criteria. No "TBD" or "handle edge cases".
- ✅ Type consistency: capitalization renames listed explicitly per symbol. New chat exports (`EmitSessionStart`, `EmitSessionEnd`, `CloseSessionEvents`) named consistently.
- ⚠️ Risk: Task 2b.4 is the largest task (~200 lines moved + ~15 reference updates). May need to split into 2b.4a (move RunInteractive+drainTurn only) and 2b.4b (move slash commands) if subagent struggles. Watch the subagent's report carefully.
- ⚠️ Risk: edit_dispatch.go may have mixed logic+render. Task 2b.2 instructs subagent to inspect and decide. If split needed, subagent reports and we plan the split inline.
