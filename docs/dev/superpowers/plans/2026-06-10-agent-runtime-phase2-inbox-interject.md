# Agent Runtime Phase 2: Inbox + Interject + Turn-Loop Drain — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Messages can be sent to a session at any time: mid-turn they are injected at the next round boundary as a `user interjected` system reminder (Claude-Code-style steering); while idle they queue and are drained at the start of the next turn. The TUI gains typing-while-busy + Enter-to-steer.

**Architecture:** `internal/chat.Session` gains a mutex-guarded inbox drained by the turn loop at the two existing reminder-injection sites (turn start, after each tool round). `pkg/shell3.Session.Interject(text)` is the never-fails public entry. `patchapp` un-gates editing keys while busy and routes Enter-while-busy to a new `onInterject` callback; `internal/tui` wires it to `Session.Interject`. The host-notification `Wake` bus is phase 5 — until then, idle interjects surface at the next `Send`.

**Tech Stack:** Go 1.25, fakellm. Branch: `agent-runtime`. Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md`.

**Conventions:** `go test -race -count=1`, `make lint`, one commit per task, doc comments state concurrency contracts.

---

### Task 1: internal/chat — inbox primitive + turn-loop drain

**Files:**
- Modify: `internal/chat/session.go` (inbox field + methods)
- Modify: `internal/chat/turn.go` (drain at the two injection sites)
- Test: `internal/chat/inbox_test.go` (new)

- [ ] **Step 1: Write the failing tests** (`internal/chat/inbox_test.go`)

```go
package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestInterject_IdleQueuesForNextTurn: an interject pushed while no turn is
// running is injected at the start of the next turn — visible to the model in
// the user message and surfaced as a SystemReminder event.
func TestInterject_IdleQueuesForNextTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("actually use repo B")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "actually use repo B") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("queued interject should surface as a system-reminder event; events=%+v", events)
	}
	// The model-visible injection lands on the turn's user message copy, not
	// on the session's persisted history.
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "user interjected") {
			t.Fatalf("interject reminder leaked into persisted history: %q", m.Content)
		}
	}
}

// TestInterject_MidTurnInjectsNextRound: an interject pushed while a tool round
// is executing is delivered before the next LLM round.
func TestInterject_MidTurnInjectsNextRound(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.Interject("stop, wrong file")
				return "echoed", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	// Order: tool_result for echo, THEN the interject reminder, THEN tokens.
	events := c.all()
	toolIdx, remIdx := -1, -1
	for i, ev := range events {
		if ev.Kind == EventToolResult && toolIdx == -1 {
			toolIdx = i
		}
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "stop, wrong file") {
			remIdx = i
		}
	}
	if toolIdx == -1 || remIdx == -1 || remIdx < toolIdx {
		t.Fatalf("interject must inject after the tool round (tool=%d, reminder=%d)", toolIdx, remIdx)
	}
}
```

(Add `"encoding/json"` to imports. `funcHandler` exists in toolhandler.go; `newCollectorSession` is the existing test helper.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/chat -run TestInterject -v`
Expected: FAIL — `sess.Interject undefined`.

- [ ] **Step 3: Implement the inbox on chat.Session** (session.go)

```go
// inbox is the cross-goroutine message queue for a session: Interject pushes
// from any goroutine; the turn loop drains on the turn goroutine at round
// boundaries. Guarded by inboxMu — the only Session state touched off the
// turn goroutine.
//
// Add to the Session struct:
	inboxMu sync.Mutex
	inbox   []string

// Interject queues text for delivery to the model: mid-turn at the next round
// boundary, otherwise at the start of the next turn. Safe to call from any
// goroutine at any time; it never fails and never blocks on a running turn.
func (s *Session) Interject(text string) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inbox = append(s.inbox, text)
}

// drainInbox removes and returns all queued interjections. Called only from
// the turn goroutine.
func (s *Session) drainInbox() []string {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	items := s.inbox
	s.inbox = nil
	return items
}

// interjectReminder formats queued interjections as one system-reminder block.
// Returns "" when items is empty.
func interjectReminder(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\nuser interjected mid-task — adjust course accordingly:\n")
	for _, it := range items {
		b.WriteString("- " + it + "\n")
	}
	b.WriteString("</system-reminder>")
	return b.String()
}
```

(`sync` import needed in session.go.)

- [ ] **Step 4: Drain at both injection sites in RunTurn** (turn.go)

Site 1 — turn start, immediately after the existing `sess.reminders.check` block (mirrors its mechanics exactly: `injectReminder` mutates allMsgs only, then emit):

```go
	if reminder := interjectReminder(sess.drainInbox()); reminder != "" {
		allMsgs = injectReminder(allMsgs, reminder)
		emitSystemReminder(sess, reminder)
	}
```

Site 2 — after each tool round, immediately after the existing post-round `sess.reminders.check` block (same mechanics: append to the last tool message in allMsgs only, then emit):

```go
		if reminder := interjectReminder(sess.drainInbox()); reminder != "" {
			allMsgs[len(allMsgs)-1].Content += "\n\n" + reminder
			emitSystemReminder(sess, reminder)
		}
```

NOTE: site 2 must run BEFORE the pendingMedia block (so the reminder lands on the last *tool* message, same constraint the reminder code documents).

- [ ] **Step 5: Run the tests**

Run: `go test -race -count=1 ./internal/chat -run TestInterject -v` → PASS, then the package: `go test -race -count=1 ./internal/chat` → PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/chat && git commit -m "feat(chat): session inbox — interjections drain into turn at round boundaries"
```

---

### Task 2: pkg/shell3 — public Session.Interject

**Files:**
- Modify: `pkg/shell3/shell3.go`
- Test: `pkg/shell3/shell3_test.go`

- [ ] **Step 1: Write the failing tests** (append to shell3_test.go)

```go
// TestSession_InterjectMidTurn: Interject during a running turn surfaces as a
// SystemReminder event in that same turn, after the tool round.
func TestSession_InterjectMidTurn(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "poke", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	var s *Session
	cfg := chat.Config{
		LLM:             client,
		CustomToolNames: map[string]bool{"poke": true},
		CustomTool: func(ctx context.Context, name, args string) (string, error) {
			s.Interject("change of plans")
			return "ok", nil
		},
	}
	s = newTestSession(t, client, cfg)
	defer s.Close()

	var sawReminder bool
	for ev := range s.Send(context.Background(), "go") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "change of plans") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatal("mid-turn Interject should surface as a SystemReminder event in the same turn")
	}
}

// TestSession_InterjectWhileIdle: Interject between turns is delivered at the
// start of the next Send.
func TestSession_InterjectWhileIdle(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("remember the deadline")
	var sawReminder bool
	for ev := range s.Send(context.Background(), "hi") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "remember the deadline") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatal("idle Interject should be injected at the start of the next turn")
	}
}
```

(Check newTestSession's signature — if it doesn't accept a full cfg with CustomTool, follow the file's existing pattern for building a Session from a custom chat.Config.)

- [ ] **Step 2: Verify failure**

Run: `go test ./pkg/shell3 -run TestSession_Interject -v` → FAIL (`s.Interject undefined`).

- [ ] **Step 3: Implement** (shell3.go, near Send)

```go
// Interject delivers text to the session outside the Send contract: during a
// running turn it is injected at the next round boundary as a system reminder
// ("user interjected …"), letting the model course-correct mid-task; while
// idle it queues and is drained at the start of the next turn. Interject never
// fails, never blocks on a running turn, and is safe to call from any
// goroutine — it is the chat-message path for front-ends (the TUI's
// Enter-while-busy, a bot's incoming message), while Send remains the strict
// turn-starting call.
func (s *Session) Interject(text string) {
	s.sess.Interject(text)
}
```

- [ ] **Step 4: Run tests, lint, commit**

Run: `go test -race -count=1 ./pkg/shell3 && make lint` → PASS.

```bash
git add pkg/shell3 && git commit -m "feat(pkg): Session.Interject — never-fails steering input for front-ends"
```

---

### Task 3: patchapp — typing while busy + Enter-to-interject

**Files:**
- Modify: `internal/patchapp/app.go` (onInterject callback + SetInterject)
- Modify: `internal/patchapp/editor.go` (un-gate editing keys; handleEnter busy branch)
- Test: `internal/patchapp/interject_test.go` (new; follow the key-simulation patterns of editor_test.go / input_test.go)

Behavior spec (exact):
- **Editing keys work while busy:** character insert, paste insertion, backspace, left/right cursor movement, alt+enter newline. Remove `!a.busy` from those branches in `processInput`/paste handling (keep the mutex locking exactly as-is).
- **Still gated while busy:** up/down history navigation, Tab (agent switch), slash command execution, `!` shell execution. Esc keeps its current cancel-the-turn behavior.
- **Enter while busy:** in `handleEnter`, when `a.busy`:
  - empty input → no-op (current behavior);
  - input starting with `/` or `!` → `a.PrintLine(dim "[busy — commands run after the turn finishes]")`, keep the input intact (do NOT clear);
  - otherwise → call `a.onInterject(text)` if non-nil, clear the input (and draft), echo `dim "[steering: <text>]"` via PrintLine. If `onInterject` is nil, fall back to the gated no-op.
- **New API** (app.go, next to SetTab):

```go
// SetInterject registers the callback fired when Enter is pressed while busy
// with plain text in the editor (mid-turn steering). The callback runs on the
// input goroutine and must not block; nil restores the historical
// swallow-input-while-busy behavior.
func (a *App) SetInterject(fn func(text string)) { a.onInterject = fn }
```

- [ ] **Step 1: Write failing tests first** (interject_test.go): simulate (per existing test helpers) typing "abc" while busy → editor contains "abc"; Enter while busy with onInterject registered → callback receives "abc", editor cleared; Enter while busy with "/clear" in the editor → callback NOT called, input preserved; Enter while busy with no callback → input preserved (nothing happens). Look at editor_test.go for how to construct an App and feed processInput bytes — reuse those helpers verbatim.

- [ ] **Step 2: Implement per the behavior spec.** Keep the per-key `a.mu` locking discipline identical — only the `!a.busy` conditions change. Update the stale busy-gate comments in editor.go (handleEnter's and handleTab's notes) to describe the new contract: editing is always allowed; submission while busy becomes interjection; slash//!/Tab/history remain gated. IMPORTANT: the busy-gate comment in internal/tui/interactive.go (~line 200) cites `App.handleEnter` early-returning while busy as the reason slash handlers can't race a turn — slash commands remain gated so that invariant holds; extend that comment to say plain-text Enter now routes to Interject, which is concurrency-safe by design.

- [ ] **Step 3: Run** `go test -race -count=1 ./internal/patchapp` → PASS (existing tests for the old swallow behavior may need updating ONLY where they asserted editing-while-busy is impossible — translate intent, e.g. "typed chars are dropped while busy" becomes "typed chars are kept while busy").

- [ ] **Step 4: Commit**

```bash
git add internal/patchapp && git commit -m "feat(patchapp): type while busy; Enter-while-busy fires onInterject (slash/!/Tab stay gated)"
```

---

### Task 4: internal/tui — wire interject to the session

**Files:**
- Modify: `internal/tui/interactive.go` (session interface + wiring)
- Test: `internal/tui/interactive_test.go` (fakeSession)

- [ ] **Step 1: Extend the `session` interface** (interactive.go ~line 24) with `Interject(text string)`, and add to fakeSession in interactive_test.go:

```go
func (f *fakeSession) Interject(text string) { f.interjections = append(f.interjections, text) }
```
(plus the `interjections []string` field).

- [ ] **Step 2: Wire it** where the App is configured (next to `app.SetSubmit`/`SetTab` in RunInteractive's setup):

```go
	app.SetInterject(func(text string) {
		sess.Interject(text)
	})
```
(The dim "[steering: …]" echo is printed by patchapp at the capture site — do not double-echo here.)

- [ ] **Step 3: Test** — follow the existing slash-command test pattern (fakeSession + registerSlashCommands or the app harness): assert that the wiring passes text through to fakeSession.interjections. If the existing tests construct the full interactive loop awkwardly, a focused test on the wiring closure is acceptable.

- [ ] **Step 4: Run** `go test -race -count=1 ./internal/tui ./internal/patchapp ./pkg/shell3` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/tui && git commit -m "feat(tui): Enter-while-busy steers the running turn via Session.Interject"
```

---

### Task 5: Phase close-out

**Files:**
- Modify: `CHANGELOG.md`, `pkg/shell3/example_test.go` (extend ExampleStart or add a line to ExampleNewRuntime showing Interject)

- [ ] **Step 1: CHANGELOG** under Unreleased/Added:

```markdown
- Mid-turn steering: `Session.Interject` queues messages from any goroutine —
  injected into a running turn at the next round boundary as a
  `user interjected` reminder, or at the start of the next turn when idle.
  In the TUI you can now type while the agent works and press Enter to steer.
```

- [ ] **Step 2: Full verification**: `make lint && go test -race ./... && make build` → green.

- [ ] **Step 3: Manual smoke**: run `./shell3` interactively IF a config exists; start a long-ish turn ("count to 30 with bash sleep 1 between numbers"), type "stop counting, say done instead" + Enter mid-turn; verify the dim steering echo and that the model adjusts. Report observations (this is observational, not blocking).

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "docs: changelog + example for mid-turn steering; phase 2 complete"
```

---

## Self-review notes

- Spec coverage: inbox unification seam ✓ (Task 1), Interject never-fails ✓ (Task 2), TUI steering + gated slash ✓ (Tasks 3-4). Wake bus + parts on Interject explicitly deferred (phases 5 and 4).
- Judgment calls: history-nav/Tab/slash stay busy-gated (concurrency contract of slash handlers depends on it — interactive.go's busy-gate comment); interject echo lives in patchapp (single render goroutine), not tui.
- Risk: editor_test.go may pin the old swallow-while-busy behavior — translate those assertions, don't delete.
