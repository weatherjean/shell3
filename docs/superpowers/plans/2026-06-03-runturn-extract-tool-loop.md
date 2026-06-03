# RunTurn Tool-Loop Extraction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract `RunTurn`'s ~108-line tool-execution loop into a single `executeToolCalls` helper, shrinking `RunTurn` from ~225 to ~120 lines with no externally-observable behavior change.

**Architecture:** Two phases. **Phase A** (Tasks 1–4) adds `fakellm`-driven characterization tests that pin the current behavior of the tool loop through the public event/message surface — because no test currently drives `RunTurn` directly. **Phase B** (Task 5) performs the pure structural extraction, gated green by those tests. Task 6 runs the full gate and finishes the branch.

**Tech Stack:** Go 1.26, module `github.com/weatherjean/shell3`. Tests use the in-tree `internal/llm/fakellm` scripted client. Quality tools already installed: `go vet`, `staticcheck`, `gofmt`, `deadcode`.

---

## Context for the executor (read first)

You have **zero prior context**. Key facts:

1. **Branch.** Work happens on `refactor/runturn-tool-loop` (already created, with the design spec committed). Do not switch branches.

2. **The design spec** is at `docs/superpowers/specs/2026-06-03-runturn-extract-tool-loop-design.md`. Read it. The chosen helper signature is "Approach A: result struct + error".

3. **THE GATE.** After every task, all of these must be clean before you commit:
   ```bash
   go build ./...
   go vet ./...
   go test -race ./...                       # all ok, no FAIL/panic
   staticcheck ./...                          # empty
   gofmt -l $(git ls-files '*.go')            # empty
   deadcode -test ./...                       # empty
   ```
   The tree is clean at baseline; any new output is yours.

4. **Characterization tests pass on FIRST write.** Phase A tests document *current* behavior, so they must PASS against the unmodified `RunTurn`. If a Phase A test fails on first run, your understanding of current behavior (or the test) is wrong — investigate; do NOT change `RunTurn` to make it pass.

5. **`RunTurn` is in `internal/chat/turn.go`** (the function spans lines 76–300 at baseline). Tool-call IDs are rewritten to sequential decimals ("1", "2", …) at turn.go:147–149 *before* the tool loop — so scripted tool-call IDs do not survive; assert on message **content/Name**, not scripted IDs.

6. **Commit style.** End each commit message with:
   ```
   Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
   ```

---

## Task 1: Phase A — tool round-trip characterization test + shared helpers

Creates a new test file with shared helpers (`stubHandler`, `collectTurn`, `hasKind`, `hasToolMessage`, `msgsContain`) and the first characterization test: a tool call in round 1, plain text in round 2.

**Files:**
- Create: `internal/chat/turn_toolloop_test.go`

- [ ] **Step 1: Write the test file with helpers + round-trip test**

```go
package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// stubHandler is a minimal ToolHandler that returns a fixed output string.
type stubHandler struct {
	name string
	out  string
}

func (h stubHandler) Name() string { return h.name }

func (h stubHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return h.out, nil
}

// collectTurn runs RunTurn in a goroutine and returns every event up to and
// including the terminal turn_done/error event (or fails on timeout).
func collectTurn(t *testing.T, ctx context.Context, cfg TurnConfig, sess *Session, input string) []Event {
	t.Helper()
	go RunTurn(ctx, cfg, sess, llm.Message{Role: llm.RoleUser, Content: input}, nil)
	var out []Event
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			out = append(out, ev)
			if ev.Kind == EventTurnDone || ev.Kind == EventError {
				return out
			}
		case <-deadline:
			t.Fatalf("timed out waiting for terminal event after %d events", len(out))
			return out
		}
	}
}

func hasKind(evs []Event, k EventKind) bool {
	for _, ev := range evs {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

// hasToolMessage reports whether the session has a RoleTool message for the
// named tool whose content contains substr.
func hasToolMessage(sess *Session, name, substr string) bool {
	for _, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == name && strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

func msgsContain(msgs []llm.Message, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// TestRunTurn_ToolRoundTrip characterizes a normal tool round-trip: round 1
// returns one tool call, round 2 returns plain text, the turn ends with
// turn_done, and the tool's result is appended to session history.
func TestRunTurn_ToolRoundTrip(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "x", Name: "echo", RawArgs: `{"v":1}`}},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "all done"},
			{Usage: &llm.Usage{PromptTokens: 6, CompletionTokens: 3, TotalTokens: 9}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
	}

	events := collectTurn(t, context.Background(), cfg, sess, "hi")

	var sawCall, sawResult, sawDone bool
	for _, ev := range events {
		switch ev.Kind {
		case EventToolCall:
			if ev.ToolName == "echo" {
				sawCall = true
			}
		case EventToolResult:
			if ev.ToolName == "echo" && ev.ToolOutput == "echoed" && !ev.ToolError {
				sawResult = true
			}
		case EventTurnDone:
			sawDone = true
		}
	}
	if !sawCall || !sawResult || !sawDone {
		t.Fatalf("round-trip events: call=%v result=%v done=%v", sawCall, sawResult, sawDone)
	}
	if !hasToolMessage(sess, "echo", "echoed") {
		t.Fatalf("expected echo tool message in session, got %+v", sess.messages)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
}
```

- [ ] **Step 2: Run the test — expect PASS (it documents current behavior)**

Run: `go test ./internal/chat/ -run TestRunTurn_ToolRoundTrip -v`
Expected: PASS. (If it fails, investigate current behavior — do not edit RunTurn.)

- [ ] **Step 3: Gate**

Run the full GATE (see Context #3). All clean.

- [ ] **Step 4: Commit**

```bash
git add internal/chat/turn_toolloop_test.go
git commit -m "$(cat <<'EOF'
test(chat): characterize RunTurn tool round-trip

Adds fakellm-driven test scaffolding (stubHandler, collectTurn helpers) and
pins the normal tool-call → result → second-round → turn_done flow, ahead of
extracting the tool-execution loop. No production code change.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Phase A — guard-cancel + synthetic-stub characterization test

Pins the behavior where a guard `cancel` decision ends the turn with a reminder + `turn_done` (not error) and fills synthetic "not executed" tool messages for unreached calls.

**Files:**
- Modify: `internal/chat/turn_toolloop_test.go`

- [ ] **Step 1: Append the test**

```go
// TestRunTurn_GuardCancel_StubsRemainingCalls characterizes a guard cancel:
// round 1 returns two tool calls, the guard cancels, and the turn ends with a
// cancellation reminder + turn_done (not error). The first call gets a real
// "USER CANCELLED" result; the unreached second call gets a synthetic stub.
func TestRunTurn_GuardCancel_StubsRemainingCalls(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "echo", RawArgs: `{}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(ctx context.Context, tool string, params map[string]any) (int, string, error) {
			return guardCancel, "nope", nil
		},
	}

	events := collectTurn(t, context.Background(), cfg, sess, "hi")

	if hasKind(events, EventError) {
		t.Fatalf("guard cancel should not emit error; events=%+v", events)
	}
	if !hasKind(events, EventTurnDone) {
		t.Fatalf("guard cancel should still emit turn_done")
	}
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "turn cancelled by user") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("expected cancellation system reminder")
	}
	if !hasToolMessage(sess, "echo", "USER CANCELLED") {
		t.Fatalf("expected USER CANCELLED tool message for the first call")
	}
	if !hasToolMessage(sess, "echo", "Not executed") {
		t.Fatalf("expected synthetic stub tool message for the unreached call")
	}
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/chat/ -run TestRunTurn_GuardCancel_StubsRemainingCalls -v`
Expected: PASS.

- [ ] **Step 3: Gate + commit**

Run the full GATE, then:

```bash
git add internal/chat/turn_toolloop_test.go
git commit -m "$(cat <<'EOF'
test(chat): characterize guard-cancel synthetic stubs

Pins that a guard cancel ends the turn with a reminder + turn_done and fills
"Not executed" stub tool messages for unreached calls, ahead of extraction.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Phase A — mid-loop context-cancel characterization test

Pins that a context cancellation observed at the top of a tool-loop iteration ends the turn with an `error` terminal event (not `turn_done`).

**Files:**
- Modify: `internal/chat/turn_toolloop_test.go`

- [ ] **Step 1: Append the test**

```go
// TestRunTurn_MidLoopCtxCancel_EmitsError characterizes mid-loop cancellation:
// the guard cancels the context during the first call, so the second
// iteration's top-of-loop ctx check trips and the turn ends with error (not
// turn_done). The guard returns allow — the abort comes from ctx, not the guard.
func TestRunTurn_MidLoopCtxCancel_EmitsError(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "echo", RawArgs: `{}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	ctx, cancel := context.WithCancel(context.Background())
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(c context.Context, tool string, params map[string]any) (int, string, error) {
			cancel() // cancel during the first call; the next iteration's ctx check trips
			return guardAllow, "", nil
		},
	}

	events := collectTurn(t, ctx, cfg, sess, "hi")

	if !hasKind(events, EventError) {
		t.Fatalf("mid-loop ctx cancel should emit error; events=%+v", events)
	}
	if hasKind(events, EventTurnDone) {
		t.Fatalf("mid-loop ctx cancel should not emit turn_done")
	}
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/chat/ -run TestRunTurn_MidLoopCtxCancel_EmitsError -v`
Expected: PASS.

- [ ] **Step 3: Gate + commit**

Run the full GATE, then:

```bash
git add internal/chat/turn_toolloop_test.go
git commit -m "$(cat <<'EOF'
test(chat): characterize mid-loop ctx cancellation

Pins that a context cancel observed inside the tool loop ends the turn with an
error terminal event (not turn_done), ahead of extraction.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Phase A — compact_history allMsgs-replacement characterization test

Pins the trickiest preserved behavior: `compact_history` wholesale-replaces `allMsgs`, so the next round's prompt reflects compaction (carries the summary, drops the pre-compaction user text).

**Files:**
- Modify: `internal/chat/turn_toolloop_test.go`

- [ ] **Step 1: Append the test**

```go
// TestRunTurn_CompactHistory_ReplacesAllMsgs characterizes the compact_history
// path: it replaces allMsgs in place, so the second round's prompt carries the
// compact summary and not the pre-compaction user text. Runs with no Store
// (handleCompactHistory skips store rolling when st == nil).
func TestRunTurn_CompactHistory_ReplacesAllMsgs(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "c", Name: "compact_history", RawArgs: `{"summary":"did stuff"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "continued"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	events := collectTurn(t, context.Background(), cfg, sess, "hello there")

	if !hasKind(events, EventTurnDone) {
		t.Fatalf("compact_history turn should complete with turn_done; events=%+v", events)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	round2 := fake.Calls[1].Msgs
	if !msgsContain(round2, "did stuff") {
		t.Fatalf("round 2 prompt missing compact summary: %+v", round2)
	}
	if msgsContain(round2, "hello there") {
		t.Fatalf("round 2 prompt still contains pre-compaction user text: %+v", round2)
	}
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/chat/ -run TestRunTurn_CompactHistory_ReplacesAllMsgs -v`
Expected: PASS.

- [ ] **Step 3: Run the whole Phase A suite together — expect PASS**

Run: `go test ./internal/chat/ -run TestRunTurn -v`
Expected: all four `TestRunTurn_*` PASS.

- [ ] **Step 4: Gate + commit**

Run the full GATE, then:

```bash
git add internal/chat/turn_toolloop_test.go
git commit -m "$(cat <<'EOF'
test(chat): characterize compact_history allMsgs replacement

Pins that compact_history replaces allMsgs so the next round's prompt reflects
compaction (carries the summary, drops the prior user text) — the trickiest
behavior to preserve through the extraction.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Phase B — extract `executeToolCalls`

Move the tool-execution loop (turn.go lines 168–288 at baseline: the `// Execute tool calls.` block through the post-loop `ctx.Err()` check) into a new `executeToolCalls` helper, and replace the call site. The reminder block that follows (currently ~290–298) stays in `RunTurn`.

**Files:**
- Modify: `internal/chat/turn.go`
- Verify (no change): `internal/chat/turn_toolloop_test.go`

- [ ] **Step 1: Add the `toolLoopOutcome` type and `executeToolCalls` function**

Insert this immediately **after** `RunTurn`'s closing brace (before `// streamOnce …`) in `turn.go`:

```go
// toolLoopOutcome reports how a turn's tool-execution loop ended.
type toolLoopOutcome struct {
	allMsgs      []llm.Message // updated slice (compact_history may have replaced it)
	cancelled    bool          // a guard returned a cancel decision
	cancelReason string        // reason text for the cancellation reminder
}

// executeToolCalls runs the assistant's tool calls in order, emitting
// tool_call/tool_result events and appending each tool message to both allMsgs
// and the session. It returns the updated allMsgs plus cancellation state.
//
//   - a non-nil error means the context was cancelled mid-loop; the caller
//     emits an error terminal event and ends the turn.
//   - outcome.cancelled means a guard cancelled; the caller emits the
//     cancellation reminder and a turn_done terminal event.
//   - otherwise the loop completed normally; outcome.allMsgs carries the
//     updated message slice for the next round.
//
// Guard-cancel takes precedence over ctx-cancel: on a guard cancel it returns
// {cancelled:true}, nil without consulting ctx afterward.
func executeToolCalls(ctx context.Context, cfg TurnConfig, sess *Session, toolCalls []llm.ToolCall, toolSchemas map[string]map[string]any, allMsgs []llm.Message) (toolLoopOutcome, error) {
	var cancelled bool
	var cancelReason string
	for idx, tc := range toolCalls {
		if ctx.Err() != nil {
			return toolLoopOutcome{}, ctx.Err()
		}

		emitToolCall(sess, tc.ID, tc.Name, tc.RawArgs)
		var decision int
		var hookReason string
		var hookErr error
		if cfg.ToolGuard != nil {
			decision, hookReason, hookErr = cfg.ToolGuard(ctx, tc.Name, parseRawArgs(tc.RawArgs))
		}
		var out string
		if hookErr != nil {
			out = fmt.Sprintf("Tool-call hook failed (the on_tool_call hook script itself errored, not the user): %v. Do not retry the same call without adjusting your approach.", hookErr)
		} else if decision == guardCancel {
			if hookReason == "" {
				hookReason = "user cancelled"
			}
			cancelled = true
			cancelReason = hookReason
			out = fmt.Sprintf("USER CANCELLED the turn before this %s call ran. Reason: %s. Subsequent tool calls in this turn were not executed.", tc.Name, hookReason)
		} else if decision == guardBlock {
			if hookReason == "" {
				hookReason = "no reason given"
			}
			out = fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, hookReason)
		} else if schema, ok := toolSchemas[tc.Name]; ok {
			if err := validateToolArgs(schema, json.RawMessage([]byte(tc.RawArgs))); err != nil {
				out = fmt.Sprintf("error: invalid tool arguments: %v", err)
			}
		}
		if out != "" {
			// Hook blocked or validation failed — out already carries the
			// reason text; nothing more to do here. The tool_result event
			// emitted below carries the error string with ToolError=true.
		} else if tc.Name == "compact_history" {
			out, allMsgs = handleCompactHistory(tc.RawArgs, cfg.Store, sess, allMsgs, cfg.Log)
		} else if tc.Name == "shell_interactive" {
			command := ParseBashArgs(tc.RawArgs)
			if cfg.ShellInteractive != nil {
				out = cfg.ShellInteractive(ctx, command, cfg.WorkDir)
			} else {
				out = "error: interactive TTY not available"
			}
		} else if cfg.CustomToolNames[tc.Name] {
			out = dispatchCustomTool(ctx, Config{CustomTool: cfg.CustomTool}, tc.Name, tc.RawArgs)
		} else if handler, ok := cfg.Handlers[tc.Name]; ok {
			toolCfg := ToolConfig{
				Store:    cfg.Store,
				WorkDir:  cfg.WorkDir,
				AllMsgs:  allMsgs,
				SessMsgs: sess.messages,
			}
			var herr error
			out, herr = handler.Execute(ctx, tc.ID, json.RawMessage([]byte(tc.RawArgs)), toolCfg)
			if herr != nil {
				// Most handlers encode failures in their output string and
				// return a nil error; a non-nil error is a genuine handler
				// fault (e.g. bash_bg failing to spawn). Log it, and if the
				// handler left no output, surface the error to the model as a
				// tool error rather than emitting an empty result.
				cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", herr)
				if out == "" {
					out = "error: " + herr.Error()
				}
			}
		} else {
			out = fmt.Sprintf("error: unknown tool %q", tc.Name)
		}

		emitToolResult(sess, tc.ID, tc.Name, out, isToolError(out))
		// Prepend the tool_call_id so the model has a stable handle it
		// can pass to prune_tool_result. Without this the id only lives
		// in structured metadata, which the model cannot reliably echo.
		content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, out)
		toolMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		allMsgs = append(allMsgs, toolMsg)
		sess.append(toolMsg)

		if cancelled {
			// Append synthetic results for any tool_calls we never reached
			// so the assistant message's tool_calls list has matching
			// tool_call_id results in history. Without this the next turn
			// 400s on providers that strictly validate the pairing.
			for _, rem := range toolCalls[idx+1:] {
				stub := llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf("[tool_call_id=%s]\nNot executed — turn cancelled by user.", rem.ID),
					ToolCallID: rem.ID,
					Name:       rem.Name,
				}
				allMsgs = append(allMsgs, stub)
				sess.append(stub)
			}
			return toolLoopOutcome{allMsgs: allMsgs, cancelled: true, cancelReason: cancelReason}, nil
		}
	}

	if ctx.Err() != nil {
		return toolLoopOutcome{}, ctx.Err()
	}
	return toolLoopOutcome{allMsgs: allMsgs}, nil
}
```

- [ ] **Step 2: Replace the inline tool loop in `RunTurn` with the call site**

In `RunTurn`, delete the entire block from the `// Execute tool calls.` comment through the post-loop `if ctx.Err()` check — i.e. replace this exact current text:

```go
		// Execute tool calls.
		var cancelled bool
		var cancelReason string
		for idx, tc := range toolCalls {
			if ctx.Err() != nil {
				msg := ctx.Err().Error()
				terminalEmit = func() { emitError(sess, msg) }
				return
			}

			emitToolCall(sess, tc.ID, tc.Name, tc.RawArgs)
			var decision int
			var hookReason string
			var hookErr error
			if cfg.ToolGuard != nil {
				decision, hookReason, hookErr = cfg.ToolGuard(ctx, tc.Name, parseRawArgs(tc.RawArgs))
			}
			var out string
			if hookErr != nil {
				out = fmt.Sprintf("Tool-call hook failed (the on_tool_call hook script itself errored, not the user): %v. Do not retry the same call without adjusting your approach.", hookErr)
			} else if decision == guardCancel {
				if hookReason == "" {
					hookReason = "user cancelled"
				}
				cancelled = true
				cancelReason = hookReason
				out = fmt.Sprintf("USER CANCELLED the turn before this %s call ran. Reason: %s. Subsequent tool calls in this turn were not executed.", tc.Name, hookReason)
			} else if decision == guardBlock {
				if hookReason == "" {
					hookReason = "no reason given"
				}
				out = fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, hookReason)
			} else if schema, ok := toolSchemas[tc.Name]; ok {
				if err := validateToolArgs(schema, json.RawMessage([]byte(tc.RawArgs))); err != nil {
					out = fmt.Sprintf("error: invalid tool arguments: %v", err)
				}
			}
			if out != "" {
				// Hook blocked or validation failed — out already carries the
				// reason text; nothing more to do here. The tool_result event
				// emitted below carries the error string with ToolError=true.
			} else if tc.Name == "compact_history" {
				out, allMsgs = handleCompactHistory(tc.RawArgs, cfg.Store, sess, allMsgs, cfg.Log)
			} else if tc.Name == "shell_interactive" {
				command := ParseBashArgs(tc.RawArgs)
				if cfg.ShellInteractive != nil {
					out = cfg.ShellInteractive(ctx, command, cfg.WorkDir)
				} else {
					out = "error: interactive TTY not available"
				}
			} else if cfg.CustomToolNames[tc.Name] {
				out = dispatchCustomTool(ctx, Config{CustomTool: cfg.CustomTool}, tc.Name, tc.RawArgs)
			} else if handler, ok := cfg.Handlers[tc.Name]; ok {
				toolCfg := ToolConfig{
					Store:    cfg.Store,
					WorkDir:  cfg.WorkDir,
					AllMsgs:  allMsgs,
					SessMsgs: sess.messages,
				}
				var herr error
				out, herr = handler.Execute(ctx, tc.ID, json.RawMessage([]byte(tc.RawArgs)), toolCfg)
				if herr != nil {
					// Most handlers encode failures in their output string and
					// return a nil error; a non-nil error is a genuine handler
					// fault (e.g. bash_bg failing to spawn). Log it, and if the
					// handler left no output, surface the error to the model as a
					// tool error rather than emitting an empty result.
					cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", herr)
					if out == "" {
						out = "error: " + herr.Error()
					}
				}
			} else {
				out = fmt.Sprintf("error: unknown tool %q", tc.Name)
			}

			emitToolResult(sess, tc.ID, tc.Name, out, isToolError(out))
			// Prepend the tool_call_id so the model has a stable handle it
			// can pass to prune_tool_result. Without this the id only lives
			// in structured metadata, which the model cannot reliably echo.
			content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, out)
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			allMsgs = append(allMsgs, toolMsg)
			sess.append(toolMsg)

			if cancelled {
				// Append synthetic results for any tool_calls we never reached
				// so the assistant message's tool_calls list has matching
				// tool_call_id results in history. Without this the next turn
				// 400s on providers that strictly validate the pairing.
				for _, rem := range toolCalls[idx+1:] {
					stub := llm.Message{
						Role:       llm.RoleTool,
						Content:    fmt.Sprintf("[tool_call_id=%s]\nNot executed — turn cancelled by user.", rem.ID),
						ToolCallID: rem.ID,
						Name:       rem.Name,
					}
					allMsgs = append(allMsgs, stub)
					sess.append(stub)
				}
				break
			}
		}

		if cancelled {
			emitSystemReminder(sess, "[turn cancelled by user: "+cancelReason+"]")
			u := totalUsage
			terminalEmit = func() { emitTurnDone(sess, u.PromptTokens, u.CompletionTokens, u.TotalTokens) }
			return
		}

		if ctx.Err() != nil {
			msg := ctx.Err().Error()
			terminalEmit = func() { emitError(sess, msg) }
			return
		}
```

with this:

```go
		// Execute tool calls.
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
```

The reminder block that immediately follows (the `// After all tool results are appended …` comment and its `if reminder := sess.reminders.check(...)` block) is **unchanged** — leave it in place.

- [ ] **Step 3: Build — expect success**

Run: `go build ./...`
Expected: builds clean. (`err` is now declared in the call-site `:=`; the earlier `streamOnce` result already uses its own `err` in a separate scope, so there is no redeclaration — if the compiler reports `err redeclared` or `declared and not used`, re-read Step 2's replacement boundaries.)

- [ ] **Step 4: Run the Phase A characterization suite — expect PASS (behavior preserved)**

Run: `go test ./internal/chat/ -run TestRunTurn -v`
Expected: all four `TestRunTurn_*` still PASS — proving the extraction preserved behavior.

- [ ] **Step 5: Gate**

Run the full GATE (see Context #3). All clean — pay attention to `deadcode` (the new helper must be reachable from `RunTurn`) and `staticcheck`.

- [ ] **Step 6: Commit**

```bash
git add internal/chat/turn.go
git commit -m "$(cat <<'EOF'
refactor(chat): extract executeToolCalls from RunTurn

Move the ~110-line tool-execution loop into executeToolCalls, returning a
toolLoopOutcome (updated allMsgs + cancel state) and an error for mid-loop ctx
cancellation. RunTurn drops to ~120 lines and reads as assemble → loop {
stream → record → execute tools → reminder }. Guard-cancel still precedes
ctx-cancel; terminal-event ordering stays in RunTurn. Pure structural change —
the Phase A characterization tests pass unchanged.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Final verification + finish the branch

**Files:** none (verification + git)

- [ ] **Step 1: Full gate on the final tree**

Run all GATE commands once more; confirm every one is clean and `go test -race ./...` shows no FAIL/panic across all packages (not just `internal/chat`).

- [ ] **Step 2: Confirm RunTurn shrank**

Run: `awk '/^func RunTurn/{s=NR} s&&/^}/{print NR-s+1" lines"; exit}' internal/chat/turn.go`
Expected: roughly ~120 lines (down from ~225).

- [ ] **Step 3: Finish**

Invoke the **superpowers:finishing-a-development-branch** skill to verify tests, present integration options (merge to `main` / PR / keep / discard), and execute the choice.

---

## Self-review checklist (done during authoring)

- **Spec coverage:** Phase A tests cover all four behaviors the spec calls out (round-trip, guard-cancel + stubs, mid-loop ctx cancel, compact_history allMsgs replacement). Phase B implements the exact Approach-A signature from the spec. Terminal-event ordering and guard-cancel precedence are explicitly preserved (Task 5 Steps 1–2). Out-of-scope items (deeper splits, preamble) are not touched.
- **No placeholders:** every test and the full extracted function are shown verbatim; every command has an expected result.
- **Type/name consistency:** `toolLoopOutcome{allMsgs,cancelled,cancelReason}`, `executeToolCalls(ctx,cfg,sess,toolCalls,toolSchemas,allMsgs)`, `stubHandler`, `collectTurn`, `hasKind`, `hasToolMessage`, `msgsContain` are used identically across tasks. Event kinds (`EventToolCall/Result/TurnDone/Error/SystemReminder`) and guard constants (`guardAllow/guardBlock/guardCancel`) match the codebase.
- **Behavior preservation:** the extracted function body is a verbatim move of the loop logic plus a final `ctx.Err()` check that subsumes the old post-loop check; guard-cancel returns before that check, preserving precedence.
