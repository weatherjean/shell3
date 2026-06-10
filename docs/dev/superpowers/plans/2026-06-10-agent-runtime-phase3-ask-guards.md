# Agent Runtime Phase 3: Guard `ask` + Host `Approve` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Lua guards can return `{action="ask"}`; the tool call suspends while a host-registered approver decides. No approver → safe deny. The TUI gets an inline y/N approval prompt; approval requests and verdicts land in the audit JSONL.

**Architecture:** `luacfg.Decision` gains `DecisionAsk` (3). `internal/chat` gains `guardAsk`, an `ApprovalRequest` type, `Approve` on Config/TurnConfig, two new audit-only event kinds, and the ask branch in `executeToolCalls` (deny-without-approver, ctx-cancellable). `pkg/shell3` threads `Approve` via `Spec` and `SessionOpts`. `patchapp` gets `RequestApproval` (blocking ask answered by y/n/Esc on the input goroutine); `internal/tui` registers it as the approver.

**Tech Stack:** Go 1.25, fakellm. Branch: `agent-runtime`. Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md`.

**Conventions:** TDD, `go test -race -count=1`, `make lint`, one commit per task, doc comments state contracts.

---

### Task 1: luacfg — `DecisionAsk`

**Files:**
- Modify: `internal/luacfg/dispatch.go`
- Test: `internal/luacfg/guard_test.go`

- [ ] **Step 1: Failing test** (append to guard_test.go, following its existing guard-eval test pattern — find the helper that loads a config with an `on_tool_call` guard and calls `OnToolCallFor`):

```go
// TestGuard_AskDecision: a guard returning action="ask" yields DecisionAsk
// with the reason passed through.
func TestGuard_AskDecision(t *testing.T) {
	c := loadConfig(t, `
shell3.model("m", { base_url = "http://x", api_key = "k", model = "mm" })
shell3.agent({ name = "a", model = "m", prompt = "p",
  on_tool_call = { function(call) return { action = "ask", reason = "needs a human" } end } })
`)
	defer c.Close()
	a := c.FirstAgent()
	d, reason, err := c.OnToolCallFor(a, context.Background(), "bash", map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if d != DecisionAsk || reason != "needs a human" {
		t.Fatalf("got (%v, %q), want (DecisionAsk, \"needs a human\")", d, reason)
	}
}
```
(Adapt the config-loading helper name to the file's actual one.)

- [ ] **Step 2: Verify failure** — `go test ./internal/luacfg -run TestGuard_AskDecision -v` → FAIL (`undefined: DecisionAsk`).

- [ ] **Step 3: Implement** (dispatch.go): add to the Decision const block:

```go
	// DecisionAsk suspends the call pending host approval: the front-end's
	// approver (Approve in chat.TurnConfig) decides allow or deny. With no
	// approver registered the engine treats ask as block (fail closed).
	DecisionAsk
```
and extend `parseAction`:

```go
	case "ask":
		return DecisionAsk
```

- [ ] **Step 4:** `go test -race -count=1 ./internal/luacfg` → PASS. Commit:

```bash
git add internal/luacfg && git commit -m "feat(luacfg): guards can return action=\"ask\" (DecisionAsk)"
```

---

### Task 2: internal/chat — ask branch, Approve hook, audit events

**Files:**
- Modify: `internal/chat/tools.go` (guardAsk const), `internal/chat/toolhandler.go` (ApprovalRequest, TurnConfig.Approve), `internal/chat/chat.go` (Config.Approve + NewTurnConfig copy), `internal/chat/event.go` (two kinds + emits), `internal/chat/turn.go` (ask branch)
- Test: `internal/chat/approve_test.go` (new)

- [ ] **Step 1: Failing tests** (`internal/chat/approve_test.go`):

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

// askGuard returns a ToolGuard that answers guardAsk with the given reason.
func askGuard(reason string) func(context.Context, string, map[string]any) (int, string, error) {
	return func(context.Context, string, map[string]any) (int, string, error) {
		return guardAsk, reason, nil
	}
}

func askTurnCfg(approve func(context.Context, ApprovalRequest) bool) TurnConfig {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{"x":1}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	return TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t", Name: "code"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		ToolGuard:   askGuard("risky"),
		Approve:     approve,
		Log:         LogOrNoop(nil),
	}
}

// TestAsk_ApprovedExecutesTool: approver true → the tool runs normally.
func TestAsk_ApprovedExecutesTool(t *testing.T) {
	var got ApprovalRequest
	cfg := askTurnCfg(func(_ context.Context, req ApprovalRequest) bool {
		got = req
		return true
	})
	events, sess := collectTurn(t, context.Background(), cfg, "go")
	if !hasToolMessage(sess, "echo", "echoed") {
		t.Fatalf("approved call should execute; events=%+v", events)
	}
	if got.Tool != "echo" || got.Reason != "risky" || got.Agent != "code" || !strings.Contains(got.RawArgs, `"x":1`) {
		t.Fatalf("approval request not populated: %+v", got)
	}
}

// TestAsk_DeniedBlocksTool: approver false → denial recorded, tool not run.
func TestAsk_DeniedBlocksTool(t *testing.T) {
	cfg := askTurnCfg(func(context.Context, ApprovalRequest) bool { return false })
	_, sess := collectTurn(t, context.Background(), cfg, "go")
	if hasToolMessage(sess, "echo", "echoed") {
		t.Fatal("denied call must not execute")
	}
	if !hasToolMessage(sess, "echo", "USER DENIED") {
		t.Fatal("denial should produce the USER DENIED tool message")
	}
}

// TestAsk_NoApproverFailsClosed: nil Approve → deny with an explanatory reason.
func TestAsk_NoApproverFailsClosed(t *testing.T) {
	cfg := askTurnCfg(nil)
	_, sess := collectTurn(t, context.Background(), cfg, "go")
	if hasToolMessage(sess, "echo", "echoed") {
		t.Fatal("ask without approver must not execute")
	}
	if !hasToolMessage(sess, "echo", "no approver") {
		t.Fatal("denial reason should mention the missing approver")
	}
}

// TestAsk_AuditEventsEmitted: approval request + decision events are emitted
// for the sink (audit), in order, around the verdict.
func TestAsk_AuditEventsEmitted(t *testing.T) {
	cfg := askTurnCfg(func(context.Context, ApprovalRequest) bool { return true })
	events, _ := collectTurn(t, context.Background(), cfg, "go")
	reqIdx, decIdx := -1, -1
	for i, ev := range events {
		if ev.Kind == EventApprovalRequest {
			reqIdx = i
		}
		if ev.Kind == EventApprovalDecision {
			decIdx = i
			if ev.Text != "allow" {
				t.Fatalf("decision event text = %q, want allow", ev.Text)
			}
		}
	}
	if reqIdx == -1 || decIdx == -1 || decIdx < reqIdx {
		t.Fatalf("want request then decision events; got req=%d dec=%d", reqIdx, decIdx)
	}
}
```

- [ ] **Step 2: Verify failure** — `go test ./internal/chat -run TestAsk -v` → FAIL.

- [ ] **Step 3: Implement.**

`tools.go` — extend the guard const block (keep the luacfg-sync comment updated):

```go
	guardAsk guardDecision = 3 // suspend pending host approval (Approve hook)
```

`toolhandler.go` — the request type and the hook on TurnConfig:

```go
// ApprovalRequest describes one suspended tool call awaiting a human verdict
// (a guard returned ask). Hosts render it (buttons, y/N prompt) and answer
// allow (true) or deny (false).
type ApprovalRequest struct {
	// Tool is the tool name; RawArgs its raw JSON arguments.
	Tool    string
	RawArgs string
	// Reason is the guard's stated reason for asking ("" if none given).
	Reason string
	// Agent is the active agent's name.
	Agent string
}
```

TurnConfig gains (documented like its siblings):

```go
	// Approve resolves guard "ask" verdicts: it blocks the turn goroutine
	// until the host answers (ctx-cancellable — treat cancellation as deny).
	// Nil fails closed: ask degrades to a deny with an explanatory reason.
	Approve func(ctx context.Context, req ApprovalRequest) bool
```

`chat.go` — same field on Config (same doc), copied in NewTurnConfig.

`event.go` — two kinds appended to the const block + String() cases ("approval_request", "approval_decision") + emit helpers (Text carries the verdict for decisions; ToolName/ToolInput carry the call for requests):

```go
	// EventApprovalRequest fires when a guard answers ask and the call
	// suspends awaiting the host's verdict. ToolName/ToolInput identify the
	// call; Text holds the guard's reason. Audit-only: no public mapping.
	EventApprovalRequest
	// EventApprovalDecision fires when the verdict arrives. Text is "allow"
	// or "deny"; ToolName identifies the call. Audit-only.
	EventApprovalDecision

func emitApprovalRequest(s *Session, name, input, reason string) {
	s.sink(Event{Kind: EventApprovalRequest, Time: time.Now(), ToolName: name, ToolInput: input, Text: reason})
}

func emitApprovalDecision(s *Session, name, verdict string) {
	s.sink(Event{Kind: EventApprovalDecision, Time: time.Now(), ToolName: name, Text: verdict})
}
```
(Match the existing emit helpers' field conventions — read two of them first.)

`turn.go` — in `executeToolCalls`, the guard-decision ladder gains an ask branch between cancel and block (read the current ladder; insert so precedence is: hookErr, cancel, ask, block, validate). The ask branch:

```go
		} else if decision == guardAsk {
			if hookReason == "" {
				hookReason = "guard requested approval"
			}
			emitApprovalRequest(sess, tc.Name, tc.RawArgs, hookReason)
			approved := false
			if cfg.Approve != nil {
				approved = cfg.Approve(ctx, ApprovalRequest{
					Tool: tc.Name, RawArgs: tc.RawArgs, Reason: hookReason,
					Agent: cfg.Personality.Name,
				})
				verdict := "deny"
				if approved {
					verdict = "allow"
				}
				emitApprovalDecision(sess, tc.Name, verdict)
			} else {
				emitApprovalDecision(sess, tc.Name, "deny (no approver)")
			}
			if !approved {
				reason := hookReason
				if cfg.Approve == nil {
					reason = "approval required but no approver is available in this front-end"
				}
				res = errResult(fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, reason))
			} else {
				handled = false // approved: fall through to validation + dispatch
			}
		}
```
CAREFUL: the existing ladder sets `handled := true` up front and flips to false at the end; integrate the approved case so validation still runs for approved calls (structure however the current code reads cleanest — behavior is what the tests pin). Note ctx cancellation during Approve: the approver receives ctx; on cancel it returns false (deny) and the existing post-iteration `ctx.Err()` check ends the turn — add a brief comment.

- [ ] **Step 4:** `go test -race -count=1 ./internal/chat` → PASS (all four new + existing). `make lint`.

- [ ] **Step 5: Commit**

```bash
git add internal/chat && git commit -m "feat(chat): guard ask verdict — Approve hook with fail-closed default and audit events"
```

---

### Task 3: pkg/shell3 — thread Approve through Spec and SessionOpts

**Files:**
- Modify: `pkg/shell3/shell3.go` (public ApprovalRequest + Spec.Approve), `pkg/shell3/runtime.go` (SessionOpts.Approve + plumbing)
- Test: `pkg/shell3/shell3_test.go`

- [ ] **Step 1: Failing test:**

```go
// TestSession_ApproveThreading: a Spec/SessionOpts approver receives ask
// verdicts and its answer controls execution.
func TestSession_ApproveThreading(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "bash", RawArgs: `{"command":"true"}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	var asked []ApprovalRequest
	cfg := chat.Config{
		LLM: client,
		ToolGuard: func(context.Context, string, map[string]any) (int, string, error) {
			return 3, "scripted ask", nil // 3 = ask; pinned by luacfg.DecisionAsk
		},
		Approve: nil, // set via the public field below
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()
	s.SetApprover(func(ctx context.Context, req ApprovalRequest) bool {
		asked = append(asked, req)
		return false
	})

	var denied bool
	for ev := range s.Send(context.Background(), "go") {
		if ev.Kind == ToolResult && strings.Contains(ev.ToolOutput, "USER DENIED") {
			denied = true
		}
	}
	if len(asked) != 1 || asked[0].Tool != "bash" || asked[0].Reason != "scripted ask" {
		t.Fatalf("approver not invoked correctly: %+v", asked)
	}
	if !denied {
		t.Fatal("deny verdict should surface as USER DENIED tool result")
	}
}
```

- [ ] **Step 2: Implement.**
- Public type: `type ApprovalRequest = chat.ApprovalRequest`? NO — pkg/shell3 must not leak internal types. Mirror it:

```go
// ApprovalRequest describes a suspended tool call awaiting the host's verdict
// (a Lua guard returned action="ask"). Render it and return true to allow.
type ApprovalRequest struct {
	Tool    string // tool name
	RawArgs string // raw JSON arguments
	Reason  string // the guard's stated reason ("" if none)
	Agent   string // active agent name
}
```
- `Spec.Approve func(ctx context.Context, req ApprovalRequest) bool` (documented: runs on the turn goroutine, blocks the turn, ctx-cancellable, nil = ask denies) and the same field on `SessionOpts`.
- `Session.SetApprover(fn)` — stores the approver and installs the adapter into `s.cfg.Approve` (converting chat.ApprovalRequest → public ApprovalRequest); callable between turns. Start/Runtime.Session call it when Spec/SessionOpts carry a non-nil Approve. (turnConfig derives TurnConfig from s.cfg each turn via chat.NewTurnConfig, which copies Approve — verify and rely on that.)

- [ ] **Step 3:** `go test -race -count=1 ./pkg/shell3 && make lint` → PASS. Commit:

```bash
git add pkg/shell3 && git commit -m "feat(pkg): thread tool-approval hook through Spec and SessionOpts"
```

---

### Task 4: patchapp — blocking approval prompt

**Files:**
- Modify: `internal/patchapp/app.go`, `internal/patchapp/editor.go`
- Test: `internal/patchapp/approval_test.go` (new)

Behavior spec (exact):
- `App.RequestApproval(question string) bool` — callable from the turn goroutine while busy. Prints the question as a committed dim line (`[approve? y/N] <question>` — multi-line questions split like the steering echo), sets a pending-approval state, BLOCKS on a channel until resolved, returns the verdict.
- While approval is pending, the input goroutine routes keys: `y`/`Y` → resolve true; `n`/`N`/`Esc`/`Enter` → resolve false (Enter = default No); Ctrl-C → resolve false (and does NOT quit); every other key is ignored (no editing while a prompt is pending). After resolution print a dim `[approved]` / `[denied]` line and restore normal input handling.
- Pending state is mu-guarded (`pendingApproval chan bool` on App; nil = none). RequestApproval while another is pending: queue naturally by taking a lock around the whole request (a second concurrent RequestApproval blocks until the first resolves — guard chains are sequential per turn, but two sessions could share an App in the future; a simple `approvalMu sync.Mutex` serializing requests is enough — document it).
- If the app is shut down (Quit) while pending, resolve false so the turn goroutine can't wedge. Hook into wherever Quit/teardown lives — find how the input loop exits and ensure a pending channel is resolved (deny) on exit.

- [ ] **Step 1: tests first** (approval_test.go, reusing the App test helpers): y approves; n denies; Esc denies; Enter denies; other keys ignored then y approves; typed chars while pending do NOT reach the editor; ctrl-c denies without quitting. Each test runs RequestApproval on a goroutine, feeds key bytes via processInput, asserts the returned verdict via channel with timeout (follow existing async test patterns if any; otherwise a 2s `select` timeout).

- [ ] **Step 2: implement; Step 3:** `go test -race -count=1 ./internal/patchapp` → PASS. Commit:

```bash
git add internal/patchapp && git commit -m "feat(patchapp): blocking y/N approval prompt answered on the input goroutine"
```

---

### Task 5: TUI wiring + close-out

**Files:**
- Modify: `internal/tui/interactive.go`, `internal/tui/once.go` (no approver — verify deny path message reads OK in one-shot output), `CHANGELOG.md`
- Test: `internal/tui/interactive_test.go`

- [ ] **Step 1:** In RunInteractive, before `shell3.Start`, set:

```go
	spec.Approve = func(ctx context.Context, req shell3.ApprovalRequest) bool {
		q := fmt.Sprintf("%s wants to run %s(%s)", req.Agent, req.Tool, req.RawArgs)
		if req.Reason != "" {
			q += " — " + req.Reason
		}
		return app.RequestApproval(q)
	}
```
ORDERING PROBLEM: `app` may be constructed after the spec is consumed by Start — read RunInteractive's actual setup order and wire accordingly (e.g. construct the App first, or use Session.SetApprover after Start — the public API from Task 3 supports both; prefer SetApprover(sess-after-Start) if the App exists by then). Truncate very long RawArgs in the question (~200 chars) so the prompt stays readable.

- [ ] **Step 2:** RunOnce: confirm no approver is registered (ask → deny with the no-approver reason) and that nothing crashes — extend an existing once test only if trivial.

- [ ] **Step 3:** CHANGELOG under Unreleased/Added:

```markdown
- Tool approval: Lua guards can return `{ action = "ask" }` to suspend a tool
  call for human approval. Front-ends answer via `Spec.Approve` /
  `SessionOpts.Approve` (Telegram buttons, webui dialogs); the TUI shows an
  inline `[approve? y/N]` prompt. No approver registered → fail closed.
  Requests and verdicts are recorded in the audit JSONL.
```

- [ ] **Step 4:** `make lint && go test -race ./... && make build` → green. Commit:

```bash
git add -A && git commit -m "feat(tui): inline y/N tool-approval prompt; phase 3 complete"
```

---

## Self-review notes

- Spec coverage: ask verdict ✓, host callback ✓ (blocking, ctx-cancellable), fail-closed ✓, audit JSONL ✓ (new kinds flow through WriteChatEvent automatically — verify outsink includes ToolName/Text for them), TUI prompt ✓. Scaffold's example ask guard is phase 6.
- Precedence decision: ask sits between cancel and block in the ladder; an approved ask still passes schema validation before dispatch.
- Public-API decision: mirror type (no internal leak) + SetApprover so the TUI can register after Start.
- Risk: patchapp pending-approval input routing must not interfere with the busy-gate or interject paths — tests pin: typed chars while pending don't reach the editor.
