package chat

import (
	"context"
	"errors"
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

// TestAsk_CtxCancelDuringApprovalEndsTurn: when the turn context is cancelled
// while the approver is blocked, the turn ends with context.Canceled — no
// fabricated USER DENIED message lands in history and no approval_decision
// event is emitted after the request.
func TestAsk_CtxCancelDuringApprovalEndsTurn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := askTurnCfg(func(c context.Context, _ ApprovalRequest) bool {
		cancel()
		<-c.Done()
		return false
	})
	events, sess := collectTurn(t, ctx, cfg, "go")

	var turnErr error
	reqSeen := false
	for _, ev := range events {
		switch ev.Kind {
		case EventApprovalRequest:
			reqSeen = true
		case EventApprovalDecision:
			if reqSeen {
				t.Fatalf("approval_decision emitted after cancelled request: %+v", ev)
			}
		case EventError:
			turnErr = ev.Err
		}
	}
	if !reqSeen {
		t.Fatal("approval_request event not emitted")
	}
	if !errors.Is(turnErr, context.Canceled) {
		t.Fatalf("turn error = %v, want context.Canceled", turnErr)
	}
	if hasToolMessage(sess, "echo", "USER DENIED") {
		t.Fatal("ctx cancel must not fabricate a USER DENIED tool message")
	}
	if hasToolMessage(sess, "echo", "echoed") {
		t.Fatal("cancelled call must not execute")
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
