# Phase 2: Decouple chat From TUI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove `patchapp`/`patchtui`/`patchmd`/`patchwidgets` imports from `internal/chat`. TUI rendering moves into a dedicated `internal/tui` package that consumes `chat.Session.Events()`.

**Architecture:** Public `chat.Session` type with `Events() <-chan Event`. TUI lives in `internal/tui`, owns its render loop, subscribes to the event channel. Turn loop no longer writes patchapp.Event commands — only emits `chat.Event`. `cmd/shell3` wires the two together.

**Tech Stack:** Same as Phase 1 (Go, bubbletea via patchtui).

---

## Scope Note

Phase 2 spans multiple subsystems (chat API surface, TUI extraction, event bridge swap, import cleanup). This document plans **Phase 2a only** in TDD detail and sketches 2b–2d. After 2a completes, **re-plan** before 2b. Do not attempt 2b–2d from this doc.

---

## Sub-Phase Overview

| Sub-Phase | Goal | Risk | Output |
|-----------|------|------|--------|
| 2a | Promote `session` → public `Session`; add `Events()`/`ID()` methods | Low | Public API surface for embedders + TUI extraction |
| 2b | Extract TUI to `internal/tui` package | Medium | `chat.RunInteractive` thinned; TUI owns app construction + render drain |
| 2c | Swap `patchapp.Event` bridge for `chat.Event` in TUI | High | turn.go no longer writes patchapp.Event; TUI consumes chat.Event only |
| 2d | Remove patchapp/patchtui/patchmd imports from chat | Low | `goimports` clean; lib core has zero TUI deps |

---

## Phase 2a: Promote `session` to Public `Session`

**Why first:** Phase 2b+ need to reference the type from outside `internal/chat`. Renaming is mechanical and isolated — every callsite is in the same package today. After this sub-phase, the type is public but nothing else changes. Reversible.

**Files:**
- Modify: `internal/chat/session.go` — rename type + constructor
- Modify: `internal/chat/chat.go` — uses of `*session`, `newSession`
- Modify: `internal/chat/turn.go` — `runTurn(ctx, tc, sess *session, ...)` signature
- Modify: `internal/chat/event.go` — `emit*` helpers take `*session`
- Modify: `internal/chat/chat_test.go`, `internal/chat/tools_test.go`, `internal/chat/session_events_test.go`, `internal/chat/event_*_test.go` — test usages
- Modify (other internal files referencing `*session`): grep before each task

### Task 2a.1: Rename `session` → `Session`, `newSession` → `NewSession`

**Files:** Every `.go` file in `internal/chat/` that mentions `session` (the type, not the field name `Sess*` / variables). Mechanical rename.

- [ ] **Step 1: Inventory references**

Run:
```bash
grep -rn "\bsession\b" internal/chat/ --include="*.go" | grep -v "// " | grep -v "session_id\|session id\|session id\|session-" | wc -l
grep -rn "\bnewSession\b" internal/chat/ --include="*.go"
```

Expected: list of files + counts. Note any "session" usages that are **not** the type (e.g. `cfg.Store.StartSession()`, `sessionID`, log strings, comments) — those should NOT change.

- [ ] **Step 2: Rename type declaration**

In `internal/chat/session.go`:

```go
// Session holds the in-progress conversation history and the event stream.
// Exported so embedders and the TUI harness can subscribe to events and read
// the underlying store session id without going through internal helpers.
type Session struct {
	messages         []llm.Message
	nextToolCallID   int
	reminders        reminderTracker
	lastPromptTokens int
	id               int64
	events           chan Event
}
```

(Move the existing field comments verbatim — only the type name changes.)

Rename `newSession` → `NewSession`:

```go
// NewSession constructs a Session with a buffered event channel.
// bufSize controls back-pressure: too small blocks the turn loop, too large
// hides slow consumers. 256 is the default for interactive use.
func NewSession(bufSize int) *Session {
	return &Session{
		events: make(chan Event, bufSize),
	}
}
```

Rename receivers:

```go
func (s *Session) append(m llm.Message) { ... }
func (s *Session) allocToolCallID() string { ... }
```

- [ ] **Step 3: Update `internal/chat/event.go`**

Change every `*session` parameter to `*Session`:

```go
func emit(s *Session, ev Event) { ... }
func emitSessionStart(s *Session, meta map[string]string) { ... }
func emitSessionEnd(s *Session, status string) { ... }
func emitToolCall(s *Session, callID, name, input string) { ... }
func emitToolResult(s *Session, callID, name, output string, isErr bool) { ... }
func emitAssistantToken(s *Session, text string) { ... }
func emitAssistantMessage(s *Session, text string) { ... }
func emitUserMessage(s *Session, text string) { ... }
func emitError(s *Session, text string) { ... }
func emitUsage(s *Session, prompt, completion, total int) { ... }
```

- [ ] **Step 4: Update `internal/chat/turn.go`**

Change `sess *session` → `sess *Session` in `runTurn`, `streamOnce`, `saveHistory`, and any other helpers. Use grep to find them:

```bash
grep -n "\*session\b" internal/chat/turn.go
```

Replace each with `*Session`.

- [ ] **Step 5: Update `internal/chat/chat.go`**

```bash
grep -n "\*session\b\|&session{\|newSession\b" internal/chat/chat.go
```

For each match:
- `*session` → `*Session`
- `newSession(256)` → `NewSession(256)`
- `&session{}` → `NewSession(256)` (none should remain after Phase 1, but verify)

Same edit in `internal/chat/session.go` for the `nextToolCallID` allocator if it references `*session`.

- [ ] **Step 6: Update remaining internal/chat files**

```bash
grep -rn "\*session\b\|newSession\b" internal/chat/ --include="*.go"
```

Files that may still reference: `handler_*.go`, `tools.go`, `toolhandler.go`, `validate.go`, `image.go`, `edit_dispatch.go`. For each match, replace `*session` → `*Session` and `newSession` → `NewSession`.

- [ ] **Step 7: Update test files**

```bash
grep -rn "\*session\b\|&session{\|newSession\b" internal/chat/ --include="*_test.go"
```

Replace per the same rules. Test files that explicitly use `&session{}` (chat_test.go, tools_test.go from Phase 1 grep) become `&Session{}` or `NewSession(0)` — prefer the latter to ensure a non-nil channel exists. If the test does not exercise events, `&Session{}` is fine because emit guards on `s.events == nil`.

- [ ] **Step 8: Run all tests**

Run: `go test ./...`
Expected: ALL PASS. If any package outside `internal/chat` fails to compile, that means it referenced the old name (it shouldn't — chat's types weren't exported). If a test fails, check whether you accidentally renamed a string literal or a non-type `session` usage (e.g. `cfg.Store.StartSession()`).

- [ ] **Step 9: Build**

Run: `go build ./cmd/shell3`
Expected: success.

- [ ] **Step 10: Commit**

```bash
git add internal/chat/
git commit -m "refactor(chat): promote session to public Session"
```

### Task 2a.2: Add `Session.Events()` and `Session.ID()` exported methods

**Files:**
- Modify: `internal/chat/session.go`
- Create: `internal/chat/session_public_test.go`

- [ ] **Step 1: Write failing test for Events() and ID()**

```go
// internal/chat/session_public_test.go
package chat

import (
	"testing"
	"time"
)

func TestSessionEventsAccessor(t *testing.T) {
	s := NewSession(4)
	ch := s.Events()
	if ch == nil {
		t.Fatal("Events() returned nil")
	}
	emitAssistantToken(s, "hi")
	select {
	case ev := <-ch:
		if ev.Kind != EventAssistantToken || ev.Text != "hi" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Events() channel did not receive emitted event")
	}
}

func TestSessionIDAccessor(t *testing.T) {
	s := NewSession(1)
	if got := s.ID(); got != 0 {
		t.Errorf("default ID = %d, want 0", got)
	}
	s.id = 99
	if got := s.ID(); got != 99 {
		t.Errorf("ID() = %d, want 99", got)
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/chat/ -run "TestSessionEventsAccessor|TestSessionIDAccessor" -v`
Expected: FAIL — `s.Events undefined`, `s.ID undefined`.

- [ ] **Step 3: Implement accessors**

Add to `internal/chat/session.go` (after `NewSession`):

```go
// Events returns the read-only event channel for this session. Consumers
// (TUI, JSONL sink, embedders) range over this channel until the session
// closes. The channel is closed exactly once during teardown.
func (s *Session) Events() <-chan Event {
	return s.events
}

// ID returns the store session id (0 if no store is configured).
func (s *Session) ID() int64 {
	return s.id
}
```

- [ ] **Step 4: Run test, verify pass**

Run: `go test ./internal/chat/ -run "TestSessionEventsAccessor|TestSessionIDAccessor" -v`
Expected: PASS.

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: ALL PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/session.go internal/chat/session_public_test.go
git commit -m "feat(chat): expose Session.Events() and Session.ID()"
```

### Task 2a.3: Phase 2a verification

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: ALL PASS.

- [ ] **Step 2: Build**

Run: `go build ./cmd/shell3`
Expected: success.

- [ ] **Step 3: Verify exported API surface**

Run:
```bash
go doc ./internal/chat Session
go doc ./internal/chat NewSession
go doc ./internal/chat Event
go doc ./internal/chat EventKind
```

Expected: each prints a non-empty docstring. (Even though the package is `internal/`, `go doc` still works for verification of what *would* be exposed if moved.)

- [ ] **Step 4: Confirm Phase 2a contract**

Mental check before proceeding:
- `Session` and `NewSession` are exported. ✓
- `Session.Events()` returns `<-chan Event`. ✓
- `Session.ID()` returns `int64`. ✓
- `chat` package still imports `patchapp`/`patchtui`/`patchmd`/`patchwidgets` (Phase 2b+ removes them). ✓
- All existing behavior identical. ✓

---

## Phase 2b: Extract TUI to `internal/tui` (sketch — re-plan before executing)

**Goal:** Move TUI construction (`patchapp.New`, `cfg.Hooks.SetReleaser(app)`, drainTurn render loop) out of `chat.RunInteractive` into a new `internal/tui` package. `chat.RunInteractive` becomes thin: build Session, run turn loop, return. TUI orchestrator lives in `internal/tui`.

**Sketch tasks:**
- Create `internal/tui/tui.go` with `Run(ctx, sess *chat.Session, opts Options) error`
- Move `patchapp.New(...)`, `app.SetContextWindow(...)`, `cfg.Hooks.SetReleaser(app)` into `tui.Run`
- Move `drainTurn` from `chat.go` into `internal/tui/drain.go`
- `chat.RunInteractive` becomes a back-compat wrapper that calls `tui.Run(ctx, sess, opts)` — keeps existing callers working
- Move `registerSlashCommands` to `internal/tui` (it references `app slashTarget`, a TUI concept)
- Move `model_picker.go` to `internal/tui`
- Move `image.go`'s TUI rendering pieces to `internal/tui` (keep base64 conversion in chat)

**Risks:**
- `drainTurn` currently has access to `lastUsage` shared state and `sink` — both need to live in tui.Run's scope or get passed in.
- `cfg.Hooks.SetReleaser(app)` couples hooks to TUI. May need a `Releaser` interface in `hooks` package with a noop default; TUI provides the real implementation.
- Slash command dispatch is intertwined with TUI input — extract carefully.

---

## Phase 2c: Swap patchapp.Event Bridge for chat.Event (sketch)

**Goal:** `turn.go` and tool handlers stop writing to `chan<- patchapp.Event`. TUI's drain loop in `internal/tui/drain.go` consumes `chat.Session.Events()` directly and translates kinds to render operations.

**Sketch tasks:**
- Audit every `ch <- patchapp.XEvent{...}` in `internal/chat/`. List all sites.
- For each `patchapp.AppendEvent` site in `turn.go`: replace with appropriate `emit*` call OR with a new `chat.Event` kind (e.g. `EventToolRenderHeader`). Decide per site whether the rendering metadata belongs in `chat.Event` or stays in TUI.
- `patchapp.TTYExecEvent` is a request/reply (TUI must execute the PTY command and send the result back). Replace with: TUI subscribes to `EventToolCall` of name `shell_interactive`, executes PTY, returns result via a side-channel callback in `chat.Config` (e.g. `ShellInteractive func(ctx, cmd, workdir) string`).
- `patchapp.UsageEvent` already paired with `EventUsage` in Phase 1 — drop the patchapp version.
- `patchapp.TurnDoneEvent` — add `EventTurnDone` if needed by TUI, or derive from `EventUsage` + state.
- `patchapp.ChunkEvent` already paired with `EventAssistantToken` — drop patchapp version.
- `patchapp.ReasoningChunkEvent` — add `EventAssistantReasoning` chat event.
- TUI's drain loop translates each `chat.Event` kind to its `patchapp` render method call.
- Remove `ch chan<- patchapp.Event` parameter from `runTurn`, `streamOnce`, and tool dispatch.

**Risks:**
- `TTYExecEvent` interactive bash uses a `ReplyC chan string` — translating to a callback that blocks the turn loop changes goroutine topology. Verify no deadlocks.
- Slash commands (`registerSlashCommands`) currently emit patchapp events directly. After moving to TUI in 2b they continue working, but ensure they don't reach back into `chat` for rendering.
- Render order matters (header line before body, dimLines, color codes). All TUI formatting moves to TUI; `chat` only emits semantic events.

---

## Phase 2d: Remove patchapp/patchtui/patchmd Imports From chat (sketch)

**Goal:** `goimports` on `internal/chat/*.go` shows zero patchapp/patchtui/patchmd/patchwidgets imports.

**Sketch tasks:**
- Grep imports: `grep -rn "patchapp\|patchtui\|patchmd\|patchwidgets" internal/chat/`
- Each remaining usage: either move the helper to TUI, inline the trivial bit (e.g. ANSI strip for sink output), or replace with semantic chat.Event field.
- `outsink.go` uses `patchtui.StripANSI` — relocate to `internal/applog` or inline as a small private helper in `outsink.go`. JSONL output should not depend on TUI.
- `chat.go` uses `patchapp.WelcomeInfo` only for welcome banner — TUI builds the welcome from `Session` metadata and `Config` fields.
- Verify with `go list -deps ./internal/chat | grep patch` — empty after this phase.

**Risks:**
- Cyclic-import risk if `internal/tui` imports `chat` and a `chat` helper accidentally back-imports `tui`. Validate dependency direction.

---

## Self-Review

- ✅ Spec coverage: Phase 2 goal (chat decoupled from TUI imports) is covered by 2a (expose Session API) → 2b (extract TUI) → 2c (swap event bridge) → 2d (delete imports).
- ✅ Placeholder scan: Phase 2a has concrete code in every step. 2b–2d explicitly marked sketches requiring re-plan.
- ✅ Type consistency: `Session` capital S throughout 2a. `Events()` returns `<-chan Event`. `ID()` returns `int64`. `NewSession(int) *Session`.
- ⚠️ Task 2a.1 Step 1 grep includes `grep -v "session-"` and similar — engineer must read each match carefully, not blind-replace. Rename is mechanical but the patterns "session" appears in (struct field `id` for store session, log strings, `StartSession()` method on store) must NOT be changed.
