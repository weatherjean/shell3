# Decouple shell3 Core From Harness — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split shell3 into an embeddable Go library (`pkg/shell3`) and a thin CLI/TUI harness (`cmd/shell3`), so other apps can import the agent loop without inheriting the terminal UI, cobra wiring, or `~/.shell3` filesystem assumptions.

**Architecture:** Core becomes pure: `Agent.Run(ctx, input)` and `Agent.Stream(ctx, input) <-chan Event`. All side effects (TTY, env, FS, stdout) move to the harness, which subscribes to the event stream and renders. Headless JSONL mode and TUI mode become two consumers of the same event stream, not two branches inside core.

**Tech Stack:** Go 1.22+, existing deps (cobra, bubbletea via patchtui, openai-compatible adapters, SQLite via store).

---

## Scope Note

This is a multi-phase refactor. **This document plans Phase 1 in bite-sized TDD steps** and sketches Phases 2–5 at task granularity. Each later phase gets its own detailed plan doc when we reach it. Do not execute Phase 2+ from this doc — re-plan first.

## Self-Review Checklist (run after each phase)

- All existing tests still pass: `go test ./...`
- `go build ./cmd/shell3` succeeds
- Manual smoke: `shell3 "hello"` interactive + `shell3 --out /tmp/x.jsonl "hello"` headless both work
- No regressions in `test/smoke_test.go`, `test/bootstrap_integration_test.go`, `test/usertools_smoke_test.go`

---

## Phase Overview

| Phase | Goal | Risk | Output |
|-------|------|------|--------|
| 1 | Event stream seam inside `chat` | Low | `chat.Event` type, `chat.Session.Events() <-chan Event`, TUI + JSONL both consume it |
| 2 | Decouple `chat` from TUI imports | Medium | `chat` no longer imports `patchapp`/`patchtui`/`patchmd` |
| 3 | Hoist FS/env to config | Low | `SHELL3_HEADLESS`, `SHELL3_OUT`, `os.Getwd`, `os.UserHomeDir` removed from core; injected via `Config` |
| 4 | Move core packages to `pkg/` | Medium | `pkg/shell3/{chat,llm,adapter,config,persona,paths,store,bootstrap}`; `cmd/shell3` consumes them |
| 5 | Public facade + example embed | Low | `pkg/shell3.Agent`, `pkg/shell3.New(cfg)`, godoc, `examples/embed/main.go` |

**Opportunistic cleanup rules (apply every phase):**
- One concern per commit. Decouple ≠ rename ≠ delete.
- Touch it = clean it. Don't touch = leave it.
- Tests green between every commit.
- Kill dead branches the refactor exposes (e.g. divergent headless/TUI render paths).

---

## Phase 1: Event Stream Seam

**Why first:** Current `chat.RunInteractive` does work AND renders. We can't pull TUI out without first defining the events the TUI consumes. This phase introduces the seam *inside* the existing package — no public API change, no package moves, no behavior change. After this phase, render code reads from a channel; before this phase, render code is inlined with logic. Reversible if it goes wrong.

**Files:**
- Create: `internal/chat/event.go` — `Event` types
- Create: `internal/chat/event_test.go` — tests for event emission
- Modify: `internal/chat/chat.go` — emit events from `RunInteractive` loop
- Modify: `internal/chat/turn.go` — emit per-turn events
- Modify: `internal/chat/outsink.go` — consume `Event` instead of inline calls

### Task 1.1: Define `Event` type

**Files:**
- Create: `internal/chat/event.go`
- Create: `internal/chat/event_test.go`

- [ ] **Step 1: Write failing test for Event type structure**

```go
// internal/chat/event_test.go
package chat

import "testing"

func TestEventKindString(t *testing.T) {
	cases := []struct {
		kind EventKind
		want string
	}{
		{EventSessionStart, "session_start"},
		{EventSessionEnd, "session_end"},
		{EventUserMessage, "user_message"},
		{EventAssistantToken, "assistant_token"},
		{EventAssistantMessage, "assistant_message"},
		{EventToolCall, "tool_call"},
		{EventToolResult, "tool_result"},
		{EventError, "error"},
		{EventUsage, "usage"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", c.kind, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/chat/ -run TestEventKindString -v`
Expected: FAIL — `undefined: EventKind`

- [ ] **Step 3: Implement `Event` type**

```go
// internal/chat/event.go
package chat

import "time"

// EventKind classifies stream events emitted by a chat session.
type EventKind int

const (
	EventSessionStart EventKind = iota
	EventSessionEnd
	EventUserMessage
	EventAssistantToken
	EventAssistantMessage
	EventToolCall
	EventToolResult
	EventError
	EventUsage
)

func (k EventKind) String() string {
	switch k {
	case EventSessionStart:
		return "session_start"
	case EventSessionEnd:
		return "session_end"
	case EventUserMessage:
		return "user_message"
	case EventAssistantToken:
		return "assistant_token"
	case EventAssistantMessage:
		return "assistant_message"
	case EventToolCall:
		return "tool_call"
	case EventToolResult:
		return "tool_result"
	case EventError:
		return "error"
	case EventUsage:
		return "usage"
	}
	return "unknown"
}

// Event is a single observable occurrence during a chat session. Consumers
// (TUI, JSONL sink, embedders) subscribe to a channel of these.
type Event struct {
	Kind      EventKind
	Time      time.Time
	SessionID int64

	// Populated based on Kind. Unused fields are zero.
	Text       string            // assistant token, user message, error message
	Role       string            // assistant_message
	ToolName   string            // tool_call, tool_result
	ToolInput  string            // tool_call (JSON)
	ToolOutput string            // tool_result
	ToolError  bool              // tool_result
	ToolCallID string            // tool_call, tool_result correlation
	Usage      *EventUsageData   // usage
	Meta       map[string]string // session_start (persona, model, mode)
}

// EventUsageData captures token accounting at the end of a turn.
type EventUsageData struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
```

- [ ] **Step 4: Run test, verify pass**

Run: `go test ./internal/chat/ -run TestEventKindString -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/chat/event.go internal/chat/event_test.go
git commit -m "feat(chat): add Event type for stream consumers"
```

### Task 1.2: Add event channel to `session`

**Files:**
- Modify: `internal/chat/session.go`
- Modify: `internal/chat/chat.go` (initialize channel)
- Create: `internal/chat/session_events_test.go`

- [ ] **Step 1: Read current `session` struct**

Run: `grep -n "type session struct" internal/chat/session.go`

- [ ] **Step 2: Write failing test for event channel**

```go
// internal/chat/session_events_test.go
package chat

import "testing"

func TestSessionEventsChannelBuffered(t *testing.T) {
	s := newSession(16)
	if s.events == nil {
		t.Fatal("session.events is nil")
	}
	if cap(s.events) != 16 {
		t.Errorf("session.events cap = %d, want 16", cap(s.events))
	}
	// Should not block when writing up to cap.
	for i := 0; i < 16; i++ {
		select {
		case s.events <- Event{Kind: EventAssistantToken}:
		default:
			t.Fatalf("event channel blocked at write %d (cap=%d)", i, cap(s.events))
		}
	}
}
```

- [ ] **Step 3: Run test, verify failure**

Run: `go test ./internal/chat/ -run TestSessionEventsChannelBuffered -v`
Expected: FAIL — `undefined: newSession` or missing `events` field

- [ ] **Step 4: Add `events` field and `newSession` constructor**

In `internal/chat/session.go`, add to the `session` struct:

```go
events chan Event
```

Add constructor:

```go
// newSession constructs a session with a buffered event channel.
// bufSize controls back-pressure: too small blocks the turn loop, too large
// hides slow consumers. 256 is the default for interactive use.
func newSession(bufSize int) *session {
	return &session{
		events: make(chan Event, bufSize),
	}
}
```

- [ ] **Step 5: Run test, verify pass**

Run: `go test ./internal/chat/ -run TestSessionEventsChannelBuffered -v`
Expected: PASS

- [ ] **Step 6: Update existing `sess := &session{}` sites in `chat.go`**

Find every `&session{}` literal in `internal/chat/chat.go` and replace with `newSession(256)`. Keep all existing field assignments.

Run: `grep -n "&session{" internal/chat/*.go`
Expected after edit: zero matches in non-test files.

- [ ] **Step 7: Run full chat package tests**

Run: `go test ./internal/chat/...`
Expected: PASS (no behavior change yet)

- [ ] **Step 8: Commit**

```bash
git add internal/chat/session.go internal/chat/chat.go internal/chat/session_events_test.go
git commit -m "feat(chat): add buffered event channel to session"
```

### Task 1.3: Emit `EventSessionStart` and `EventSessionEnd`

**Files:**
- Modify: `internal/chat/chat.go`
- Create: `internal/chat/event_emit_test.go`

- [ ] **Step 1: Write failing test that drains channel after a fake session boundary**

```go
// internal/chat/event_emit_test.go
package chat

import (
	"testing"
	"time"
)

func TestEmitSessionStartEnd(t *testing.T) {
	s := newSession(4)
	s.id = 42
	emitSessionStart(s, map[string]string{"persona": "default", "model": "gpt-x"})
	emitSessionEnd(s, "ok")

	got := drainEvents(s, 2, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventSessionStart {
		t.Errorf("event[0].Kind = %v, want EventSessionStart", got[0].Kind)
	}
	if got[0].SessionID != 42 {
		t.Errorf("event[0].SessionID = %d, want 42", got[0].SessionID)
	}
	if got[0].Meta["persona"] != "default" {
		t.Errorf("event[0].Meta[persona] = %q, want default", got[0].Meta["persona"])
	}
	if got[1].Kind != EventSessionEnd {
		t.Errorf("event[1].Kind = %v, want EventSessionEnd", got[1].Kind)
	}
	if got[1].Meta["status"] != "ok" {
		t.Errorf("event[1].Meta[status] = %q, want ok", got[1].Meta["status"])
	}
}

// drainEvents reads up to n events from s.events or returns whatever arrived
// before timeout.
func drainEvents(s *session, n int, timeout time.Duration) []Event {
	out := make([]Event, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev := <-s.events:
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}
```

- [ ] **Step 2: Run test, verify failure**

Run: `go test ./internal/chat/ -run TestEmitSessionStartEnd -v`
Expected: FAIL — `undefined: emitSessionStart, emitSessionEnd`

- [ ] **Step 3: Implement emit helpers**

Add to `internal/chat/event.go`:

```go
// emitSessionStart publishes a session_start event. Non-blocking: if the
// channel is full (slow consumer), the event is dropped and a counter
// would ideally be incremented. For now we drop silently to keep the
// turn loop from stalling.
func emitSessionStart(s *session, meta map[string]string) {
	emit(s, Event{
		Kind:      EventSessionStart,
		Time:      time.Now(),
		SessionID: s.id,
		Meta:      meta,
	})
}

func emitSessionEnd(s *session, status string) {
	emit(s, Event{
		Kind:      EventSessionEnd,
		Time:      time.Now(),
		SessionID: s.id,
		Meta:      map[string]string{"status": status},
	})
}

// emit performs a non-blocking send. Dropped events are not retried.
func emit(s *session, ev Event) {
	if s == nil || s.events == nil {
		return
	}
	select {
	case s.events <- ev:
	default:
	}
}
```

- [ ] **Step 4: Run test, verify pass**

Run: `go test ./internal/chat/ -run TestEmitSessionStartEnd -v`
Expected: PASS

- [ ] **Step 5: Wire into `RunInteractive`**

In `internal/chat/chat.go`, locate the existing `sink.WriteStart(...)` call near line 128 and add immediately after:

```go
emitSessionStart(sess, map[string]string{
	"mode":    cfg.ModeLabel,
	"persona": cfg.Personality.Name,
	"out":     cfg.OutPath,
})
```

Locate the `defer func() { ... sink.WriteEnd(status) ... }()` near line 133 and add inside it, before `sink.WriteEnd`:

```go
emitSessionEnd(sess, status)
```

- [ ] **Step 6: Run full chat tests + smoke build**

Run: `go test ./internal/chat/... && go build ./cmd/shell3`
Expected: PASS, build succeeds

- [ ] **Step 7: Commit**

```bash
git add internal/chat/event.go internal/chat/chat.go internal/chat/event_emit_test.go
git commit -m "feat(chat): emit session_start and session_end events"
```

### Task 1.4: Emit `EventToolCall` and `EventToolResult`

**Files:**
- Modify: `internal/chat/turn.go` (or `toolhandler.go` — locate dispatch site)
- Create: `internal/chat/event_tool_test.go`

- [ ] **Step 1: Locate the tool dispatch site**

Run: `grep -n "ToolHandler\|ToolCall\|dispatchTool\|runTool" internal/chat/turn.go internal/chat/toolhandler.go`
Identify the function that (a) receives a tool call from the LLM stream and (b) returns the tool result string.

- [ ] **Step 2: Write failing test for tool event emission**

```go
// internal/chat/event_tool_test.go
package chat

import (
	"testing"
	"time"
)

func TestEmitToolCallAndResult(t *testing.T) {
	s := newSession(4)
	s.id = 7
	emitToolCall(s, "call_1", "bash", `{"cmd":"ls"}`)
	emitToolResult(s, "call_1", "bash", "file1\nfile2\n", false)

	got := drainEvents(s, 2, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventToolCall || got[0].ToolName != "bash" || got[0].ToolCallID != "call_1" {
		t.Errorf("tool_call event mismatch: %+v", got[0])
	}
	if got[0].ToolInput != `{"cmd":"ls"}` {
		t.Errorf("tool_call input = %q", got[0].ToolInput)
	}
	if got[1].Kind != EventToolResult || got[1].ToolOutput != "file1\nfile2\n" || got[1].ToolError {
		t.Errorf("tool_result event mismatch: %+v", got[1])
	}
}
```

- [ ] **Step 3: Run test, verify failure**

Run: `go test ./internal/chat/ -run TestEmitToolCallAndResult -v`
Expected: FAIL — `undefined: emitToolCall, emitToolResult`

- [ ] **Step 4: Implement emit helpers**

Add to `internal/chat/event.go`:

```go
func emitToolCall(s *session, callID, name, input string) {
	emit(s, Event{
		Kind:       EventToolCall,
		Time:       time.Now(),
		SessionID:  s.id,
		ToolName:   name,
		ToolInput:  input,
		ToolCallID: callID,
	})
}

func emitToolResult(s *session, callID, name, output string, isErr bool) {
	emit(s, Event{
		Kind:       EventToolResult,
		Time:       time.Now(),
		SessionID:  s.id,
		ToolName:   name,
		ToolOutput: output,
		ToolError:  isErr,
		ToolCallID: callID,
	})
}
```

- [ ] **Step 5: Run test, verify pass**

Run: `go test ./internal/chat/ -run TestEmitToolCallAndResult -v`
Expected: PASS

- [ ] **Step 6: Wire emit calls into dispatch site**

At the tool dispatch site identified in Step 1, immediately before invoking the handler add:

```go
emitToolCall(sess, callID, toolName, toolInput)
```

Immediately after the handler returns (whether success or error path) add:

```go
emitToolResult(sess, callID, toolName, resultStr, resultErr != nil)
```

(Replace `sess`, `callID`, `toolName`, `toolInput`, `resultStr`, `resultErr` with the actual local variable names at that site. If `sess` is not in scope, propagate it via the call signature — that propagation is part of this step.)

- [ ] **Step 7: Run full chat tests + smoke**

Run: `go test ./internal/chat/... && go build ./cmd/shell3`
Expected: PASS, build succeeds

- [ ] **Step 8: Commit**

```bash
git add internal/chat/event.go internal/chat/turn.go internal/chat/toolhandler.go internal/chat/event_tool_test.go
git commit -m "feat(chat): emit tool_call and tool_result events"
```

### Task 1.5: Emit `EventAssistantToken`, `EventAssistantMessage`, `EventUserMessage`, `EventError`, `EventUsage`

**Files:**
- Modify: `internal/chat/chat.go`, `internal/chat/turn.go`
- Create: `internal/chat/event_stream_test.go`

- [ ] **Step 1: Locate sites that currently render assistant tokens / user input / errors / usage**

Run: `grep -n "patchapp\|AppendAssistant\|AppendUser\|Usage{" internal/chat/chat.go internal/chat/turn.go`

- [ ] **Step 2: Write failing test for token + message emission**

```go
// internal/chat/event_stream_test.go
package chat

import (
	"testing"
	"time"
)

func TestEmitAssistantTokenAndMessage(t *testing.T) {
	s := newSession(8)
	emitAssistantToken(s, "Hel")
	emitAssistantToken(s, "lo")
	emitAssistantMessage(s, "Hello")
	got := drainEvents(s, 3, 100*time.Millisecond)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Kind != EventAssistantToken || got[0].Text != "Hel" {
		t.Errorf("event[0]: %+v", got[0])
	}
	if got[1].Kind != EventAssistantToken || got[1].Text != "lo" {
		t.Errorf("event[1]: %+v", got[1])
	}
	if got[2].Kind != EventAssistantMessage || got[2].Text != "Hello" {
		t.Errorf("event[2]: %+v", got[2])
	}
}

func TestEmitUserMessage(t *testing.T) {
	s := newSession(2)
	emitUserMessage(s, "hi")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventUserMessage || got[0].Text != "hi" {
		t.Fatalf("user_message event mismatch: %+v", got)
	}
}

func TestEmitError(t *testing.T) {
	s := newSession(2)
	emitError(s, "boom")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventError || got[0].Text != "boom" {
		t.Fatalf("error event mismatch: %+v", got)
	}
}

func TestEmitUsage(t *testing.T) {
	s := newSession(2)
	emitUsage(s, 100, 50, 150)
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventUsage {
		t.Fatalf("usage event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.PromptTokens != 100 || got[0].Usage.CompletionTokens != 50 || got[0].Usage.TotalTokens != 150 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}
```

- [ ] **Step 3: Run test, verify failure**

Run: `go test ./internal/chat/ -run "TestEmitAssistant|TestEmitUser|TestEmitError|TestEmitUsage" -v`
Expected: FAIL — undefined helpers

- [ ] **Step 4: Implement emit helpers**

Append to `internal/chat/event.go`:

```go
func emitAssistantToken(s *session, text string) {
	emit(s, Event{Kind: EventAssistantToken, Time: time.Now(), SessionID: s.id, Text: text})
}

func emitAssistantMessage(s *session, text string) {
	emit(s, Event{Kind: EventAssistantMessage, Time: time.Now(), SessionID: s.id, Role: "assistant", Text: text})
}

func emitUserMessage(s *session, text string) {
	emit(s, Event{Kind: EventUserMessage, Time: time.Now(), SessionID: s.id, Role: "user", Text: text})
}

func emitError(s *session, text string) {
	emit(s, Event{Kind: EventError, Time: time.Now(), SessionID: s.id, Text: text})
}

func emitUsage(s *session, prompt, completion, total int) {
	emit(s, Event{
		Kind:      EventUsage,
		Time:      time.Now(),
		SessionID: s.id,
		Usage:     &EventUsageData{PromptTokens: prompt, CompletionTokens: completion, TotalTokens: total},
	})
}
```

- [ ] **Step 5: Run test, verify pass**

Run: `go test ./internal/chat/ -run "TestEmitAssistant|TestEmitUser|TestEmitError|TestEmitUsage" -v`
Expected: PASS

- [ ] **Step 6: Wire emit calls at existing sites**

For each render call located in Step 1, insert a paired `emit*` call alongside (do not remove the existing render — Phase 2 will swap render for subscription). Specifically:
- Wherever the LLM stream produces a token delta and appends to TUI, call `emitAssistantToken(sess, delta)`.
- Wherever a full assistant message is committed, call `emitAssistantMessage(sess, fullText)`.
- Wherever a user input is accepted, call `emitUserMessage(sess, input)`.
- Wherever an error is printed (e.g. `fmt.Fprintln(os.Stderr, "error:", v.Err)` at `chat.go:763`), call `emitError(sess, v.Err.Error())`.
- Wherever final usage is recorded for a turn, call `emitUsage(sess, p, c, t)`.

- [ ] **Step 7: Run full chat tests + smoke**

Run: `go test ./internal/chat/... && go build ./cmd/shell3`
Expected: PASS, build succeeds

- [ ] **Step 8: Commit**

```bash
git add internal/chat/event.go internal/chat/chat.go internal/chat/turn.go internal/chat/event_stream_test.go
git commit -m "feat(chat): emit token/message/error/usage events"
```

### Task 1.6: Refactor `outsink` to consume `Event`

**Why:** outsink (JSONL writer) currently has its own typed method per event (`WriteStart`, `WriteEnd`, etc.). Make it consume `Event` from the channel. Removes a parallel path and proves the event stream is rich enough to drive real output.

**Files:**
- Modify: `internal/chat/outsink.go`
- Modify: `internal/chat/outsink_test.go`
- Modify: `internal/chat/chat.go` (replace direct `sink.Write*` calls with channel subscription)

- [ ] **Step 1: Read current `outsink.go`**

Run: `cat internal/chat/outsink.go`

- [ ] **Step 2: Read current `outsink_test.go`**

Run: `cat internal/chat/outsink_test.go`

- [ ] **Step 3: Write failing test for `sink.WriteEvent(Event)`**

Append to `internal/chat/outsink_test.go`:

```go
func TestSinkWriteEventToolCall(t *testing.T) {
	var buf bytes.Buffer
	s := &sink{w: &buf}
	s.WriteEvent(Event{
		Kind:       EventToolCall,
		Time:       time.Unix(1700000000, 0).UTC(),
		ToolName:   "bash",
		ToolInput:  `{"cmd":"ls"}`,
		ToolCallID: "c1",
	})
	got := buf.String()
	if !strings.Contains(got, `"kind":"tool_call"`) {
		t.Errorf("missing kind: %s", got)
	}
	if !strings.Contains(got, `"tool":"bash"`) {
		t.Errorf("missing tool: %s", got)
	}
	if !strings.Contains(got, `"call_id":"c1"`) {
		t.Errorf("missing call_id: %s", got)
	}
}
```

(If existing test file does not have `bytes`/`strings`/`time` imported, add them.)

- [ ] **Step 4: Run test, verify failure**

Run: `go test ./internal/chat/ -run TestSinkWriteEventToolCall -v`
Expected: FAIL — `WriteEvent undefined`

- [ ] **Step 5: Implement `sink.WriteEvent(Event)`**

Add to `internal/chat/outsink.go`:

```go
// WriteEvent serializes a single Event as one JSONL line. Unknown kinds
// are written with their string form; consumers must be forward-compatible.
func (s *sink) WriteEvent(ev Event) {
	if s == nil || s.w == nil {
		return
	}
	rec := map[string]any{
		"ts":         ev.Time.UTC().Format(time.RFC3339Nano),
		"kind":       ev.Kind.String(),
		"session_id": ev.SessionID,
	}
	if ev.Text != "" {
		rec["text"] = ev.Text
	}
	if ev.ToolName != "" {
		rec["tool"] = ev.ToolName
	}
	if ev.ToolInput != "" {
		rec["input"] = ev.ToolInput
	}
	if ev.ToolOutput != "" {
		rec["output"] = ev.ToolOutput
	}
	if ev.ToolCallID != "" {
		rec["call_id"] = ev.ToolCallID
	}
	if ev.ToolError {
		rec["tool_error"] = true
	}
	if ev.Usage != nil {
		rec["usage"] = ev.Usage
	}
	if len(ev.Meta) > 0 {
		rec["meta"] = ev.Meta
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	_, _ = s.w.Write(append(b, '\n'))
}
```

- [ ] **Step 6: Run test, verify pass**

Run: `go test ./internal/chat/ -run TestSinkWriteEventToolCall -v`
Expected: PASS

- [ ] **Step 7: Bridge channel → sink in `RunInteractive` with shutdown ordering**

Required ordering on shutdown:
1. Last `emit*` call finishes (all in main goroutine of `RunInteractive` / turn loop)
2. `close(sess.events)` — signals drain to exit its `range`
3. Drain goroutine finishes its last `sink.WriteEvent`
4. `sinkCleanup()` closes the underlying file

Use a `sync.WaitGroup` to enforce step 3 before step 4.

In `internal/chat/chat.go`, after the `sink, sinkCleanup, openErr := openSink(...)` block and the existing `sink.WriteStart(...)` call, add:

```go
var drainWG sync.WaitGroup
if sink != nil {
	drainWG.Add(1)
	go func() {
		defer drainWG.Done()
		for ev := range sess.events {
			sink.WriteEvent(ev)
		}
	}()
}
```

(Add `"sync"` to imports if missing.)

Refactor the shutdown defers. The current defer order in `RunInteractive` is (in source order, so LIFO runs bottom-to-top):
- `defer sinkCleanup()` — runs LAST
- `defer func() { ... emit + sink.WriteEnd(status) ... }()` — runs before sinkCleanup

Change shutdown to a single explicit cleanup block. Replace the two defers with:

```go
defer func() {
	status := "ok"
	if runErr != nil {
		status = "error"
	}
	emitSessionEnd(sess, status)
	close(sess.events) // signals drain goroutine to exit
	drainWG.Wait()     // wait for drain to finish writing
	if sink != nil {
		sink.WriteEnd(status)
	}
	sinkCleanup()
}()
```

Remove the original `defer sinkCleanup()` and the original status defer once this block replaces them.

**Producer-after-close guard:** Every `emit*` helper already has `select { case s.events <- ev: default: }` — a non-blocking send. But sending on a closed channel still panics. To make emit safe after close, change `emit` in `internal/chat/event.go` to recover:

```go
func emit(s *session, ev Event) {
	if s == nil || s.events == nil {
		return
	}
	defer func() { _ = recover() }() // tolerate send-after-close during teardown races
	select {
	case s.events <- ev:
	default:
	}
}
```

This is the only known place we tolerate a recover. Document above with the comment shown.

- [ ] **Step 8: Verify existing JSONL smoke test still passes**

Run: `go test ./test/... -run Smoke -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/chat/outsink.go internal/chat/outsink_test.go internal/chat/chat.go
git commit -m "feat(chat): outsink consumes Event stream"
```

### Task 1.7: Phase 1 verification

- [ ] **Step 1: Full test suite**

Run: `go test ./...`
Expected: all PASS

- [ ] **Step 2: Build**

Run: `go build ./cmd/shell3`
Expected: success

- [ ] **Step 3: Manual interactive smoke**

Run: `./shell3 "say hi"` (with provider configured)
Expected: response renders; no regressions

- [ ] **Step 4: Manual headless smoke**

Run: `./shell3 --out /tmp/shell3-smoke.jsonl "say hi"`
Run: `cat /tmp/shell3-smoke.jsonl`
Expected: JSONL lines including `session_start`, `assistant_token`, `assistant_message`, `session_end`

- [ ] **Step 5: Commit verification artifact (optional)**

If anything was tweaked, commit. Otherwise skip.

---

## Phase 2: Decouple `chat` From TUI Imports (sketch — re-plan before executing)

**Goal:** `chat` package no longer imports `patchapp`, `patchtui`, `patchmd`. The TUI moves to its own package that constructs a `chat.Session`, calls `Session.Run(ctx, input)`, and renders from `Session.Events()`.

**Tasks (to be expanded into TDD steps):**
- Introduce `chat.Session` public type wrapping current internal `session`. Expose `Events() <-chan Event`, `Send(ctx, input) error`.
- Move `patchapp.New(...)` construction out of `chat.RunInteractive` into new `internal/tui` package.
- Move `cfg.Hooks.SetReleaser(app)` out of chat — TUI registers releaser, not core.
- Extract `RunInteractive` body: pure session loop stays in `chat`; TUI rendering moves to `internal/tui.Run(cfg, sess)`.
- Replace direct `os.Stderr` writes in `chat.go:763` with `emitError` (already added in Phase 1) + TUI/JSONL render of error events.
- Drop `patchapp`, `patchtui`, `patchmd`, `patchwidgets` imports from `internal/chat/*.go`. Verify with `goimports` + `go list -deps`.

**Risks:**
- `chat.Hooks.SetReleaser(app)` couples hooks to TUI. May need a `Releaser` interface in `hooks` package with a noop default.
- Model picker (`model_picker.go`) likely TUI-only — move to `internal/tui` with chat exposing a `ModelSwitch(ctx, choice) error` hook.

---

## Phase 3: Hoist FS/Env Out of Core (sketch)

**Goal:** Remove `os.Getenv("SHELL3_HEADLESS")`, `os.Setenv(...)`, `os.Getwd`, `os.UserHomeDir`, `os.Stdin/Stdout/Stderr` from any package destined for `pkg/`. All become `Config` fields populated by the harness.

**Tasks:**
- Audit: `grep -rn "os.Getenv\|os.Setenv\|os.Getwd\|os.UserHomeDir\|os.Std" internal/{chat,llm,adapter,config,persona,paths,store,bootstrap}/`. List every site.
- `hooks/hooks.go:79–82`: replace `os.Stdout`/`os.Stderr` with `io.Writer` fields on `hooks.Runner`. Default to `io.Discard` for embed; harness passes `os.Stdout`.
- `chat/chat.go:364–366` (bash handler stdio): inject `Stdin/Stdout/Stderr io.ReadWriter` via Config. For headless, harness pipes; for TUI, harness pipes through PTY.
- `chat/chat.go:73` headless flag: already a Config field — remove the `os.Setenv("SHELL3_HEADLESS", "1")` from `cmd/shell3/run.go:76` once hooks consume the Config field instead of the env var.
- Replace `os.Getwd` calls in core with `Config.WorkDir` (already a field — find leakage).
- Replace `os.UserHomeDir` in core with `paths.Paths` struct passed in.

**Risks:**
- `usertools` and `skills` packages read FS for tool definitions. May need `tools.Loader` interface so embedders can supply tools without FS scan.

---

## Phase 4: Move Core Packages to `pkg/` (sketch)

**Goal:** Code Go expects external consumers to import is under `pkg/`. Harness-only code stays in `internal/`.

**Move plan (per-package commit each):**
1. `mv internal/llm pkg/llm` — pure types, lowest blast radius
2. `mv internal/adapter pkg/adapter` — depends only on llm
3. `mv internal/store pkg/store` — SQLite layer
4. `mv internal/persona pkg/persona`
5. `mv internal/paths pkg/paths`
6. `mv internal/config pkg/config`
7. `mv internal/bootstrap pkg/bootstrap`
8. `mv internal/chat pkg/chat` — last; biggest fan-in

**Stays in `internal/`:** `patchapp`, `patchtui`, `patchmd`, `patchwidgets`, `applog`, `hooks` (until proven embed-safe), `scaffold`, `ref`, `bgjobs`, `edittool`, `usertools`, `skills`.

**Per-move recipe:**
- `git mv` the directory
- Find-replace `github.com/weatherjean/shell3/internal/X` → `github.com/weatherjean/shell3/pkg/X` across the repo
- `go build ./... && go test ./...`
- Commit `refactor: move X to pkg/`

---

## Phase 5: Public Facade + Example (sketch)

**Goal:** A single import surface for embedders.

**Tasks:**
- Create `pkg/shell3/shell3.go` exposing:
  - `type Config struct { ... }` — superset of needed knobs, no FS paths required
  - `type Agent struct { ... }` — wraps `chat.Session`
  - `func New(cfg Config) (*Agent, error)`
  - `func (*Agent) Run(ctx context.Context, input string) (Response, error)` — blocking, returns final assistant message + usage
  - `func (*Agent) Stream(ctx context.Context, input string) (<-chan chat.Event, error)`
  - `func (*Agent) Close() error`
- Create `examples/embed/main.go` — 30 lines, demonstrates `New`, `Stream`, prints tokens
- Write `pkg/shell3/doc.go` with package godoc + minimal usage example
- Update root README.md with "Embedding" section pointing at example

**Tasks (post-phase, optional):**
- Tag `v0.1.0`
- Set up `go vet ./pkg/...` in CI
- Add `pkg/shell3` to a downstream consumer to validate API

---

## Self-Review

- ✅ Spec coverage: Phases cover (1) event stream, (2) TUI decouple, (3) FS/env hoist, (4) package move, (5) facade. User asked for "decouple TUI from harness" + "opportunistic cleanup" — both addressed (cleanup rules at top, dead-path deletion called out in Phase 2).
- ✅ Placeholder scan: Phase 1 tasks have concrete code, files, commands, expected output. Phases 2–5 are explicitly marked sketches requiring re-plan.
- ✅ Type consistency: `EventKind` enum, `Event` struct fields, `emit*` helper names consistent across Phase 1 tasks.
- ⚠️ Open question for Phase 1 execution: Task 1.5 Step 6 says "insert paired emit call alongside" — exact line numbers depend on current `chat.go` / `turn.go` state. Engineer must grep and judge, not blind-apply.
