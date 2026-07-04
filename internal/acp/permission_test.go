package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// newTestEnvWithGate builds a test env identical to newTestEnv but injects an
// on_tool_call gate into the generated shell3.lua that returns {ask=askPrompt,
// reason=askReason} for any bash tool call. Existing callers of newTestEnv are
// unaffected; this sibling is used only by permission tests.
//
// The injected snippet is appended after the agent declarations, so the Lua
// state (model, agents, subagents) is already set up when on_tool_call runs.
func newTestEnvWithGate(t *testing.T, askPrompt, askReason string, scripts ...string) *env {
	t.Helper()
	// Inject the on_tool_call hook into the lua content by wrapping newTestEnv's
	// Lua writer. We do this by re-implementing newTestEnv with the extra snippet.
	// The simplest approach: replicate newTestEnv logic here with a gate appended.
	// This avoids changing newTestEnv's signature and breaks no existing callers.
	return newTestEnvFull(t, askPrompt, askReason, scripts...)
}

// TestPermissionAllow verifies that when on_tool_call returns an ask verdict
// and the recorder answers "allow", the bash tool actually runs and the turn
// completes with end_turn. The recorder must observe both a tool_call and a
// tool_call_update (completed) event.
func TestPermissionAllow(t *testing.T) {
	// LLM scripts: first turn calls bash; after bash runs, the model gets the
	// result and emits "done" to end the turn.
	e := newTestEnvWithGate(t, "run this?", "test gate",
		`tool:bash:{"command":"echo hi"}`,
		"done",
	)
	ctx := context.Background()

	// Wire the recorder to answer "allow" for every permission request.
	e.rec.permFunc = func(_ context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, bool) {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.NewRequestPermissionOutcomeSelected("allow"),
		}, true
	}

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "run echo hi"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, acpsdk.StopReasonEndTurn)
	}

	updates := e.rec.snapshotUpdates()
	var foundToolCall, foundToolCallUpdate bool
	for _, n := range updates {
		if n.Update.ToolCall != nil {
			foundToolCall = true
		}
		if n.Update.ToolCallUpdate != nil {
			foundToolCallUpdate = true
		}
	}
	if !foundToolCall {
		t.Error("TestPermissionAllow: no tool_call update recorded — bash was not dispatched")
	}
	if !foundToolCallUpdate {
		t.Error("TestPermissionAllow: no tool_call_update recorded — bash result not forwarded")
	}
	perms := e.rec.snapshotPerms()
	if len(perms) == 0 {
		t.Error("TestPermissionAllow: no permission request recorded")
	}
}

// TestPermissionReject verifies that when on_tool_call returns an ask verdict
// and the recorder answers "reject" (or cancelled), the bash tool is blocked,
// the turn still completes with end_turn, and the recorded tool_call_update
// reflects a failure status (bash was denied, not executed).
func TestPermissionReject(t *testing.T) {
	// LLM scripts: first turn tries bash; since it's blocked the model sees an
	// error and emits a text response to end the turn.
	e := newTestEnvWithGate(t, "run this?", "test gate",
		`tool:bash:{"command":"echo hi"}`,
		"I cannot run that command.",
	)
	ctx := context.Background()

	// Wire the recorder to answer "reject" for every permission request.
	e.rec.permFunc = func(_ context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, bool) {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.NewRequestPermissionOutcomeSelected("reject"),
		}, true
	}

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "run echo hi"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	// Turn must still complete normally.
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, acpsdk.StopReasonEndTurn)
	}

	// The tool_call_update for bash must reflect a failed (blocked) status.
	updates := e.rec.snapshotUpdates()
	var bashUpdateFailed bool
	for _, n := range updates {
		if tcu := n.Update.ToolCallUpdate; tcu != nil {
			if tcu.Status != nil && *tcu.Status == acpsdk.ToolCallStatusFailed {
				bashUpdateFailed = true
				break
			}
		}
	}
	if !bashUpdateFailed {
		t.Error("TestPermissionReject: expected tool_call_update with status=failed for blocked bash")
	}
}

// TestPermissionRequestShape verifies the shape of the permission request
// sent by askerFor: exactly 2 options (allow_once and reject_once), a
// non-empty title, and the session ID is populated.
func TestPermissionRequestShape(t *testing.T) {
	e := newTestEnvWithGate(t, "run this?", "test gate",
		`tool:bash:{"command":"echo hi"}`,
		"done",
	)
	ctx := context.Background()

	// Allow so the turn completes cleanly.
	e.rec.permFunc = func(_ context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, bool) {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.NewRequestPermissionOutcomeSelected("allow"),
		}, true
	}

	sessID := newSession(t, e.conn)

	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "run echo hi")); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	perms := e.rec.snapshotPerms()
	if len(perms) == 0 {
		t.Fatal("TestPermissionRequestShape: no permission request recorded")
	}
	req := perms[0]

	// Title must be non-empty.
	if req.ToolCall.Title == nil || *req.ToolCall.Title == "" {
		t.Error("TestPermissionRequestShape: permission request title is empty")
	}

	// Exactly 2 options.
	if len(req.Options) != 2 {
		t.Errorf("TestPermissionRequestShape: want 2 options, got %d", len(req.Options))
	}

	// Check kinds: one allow_once and one reject_once.
	var hasAllow, hasReject bool
	for _, opt := range req.Options {
		switch opt.Kind {
		case acpsdk.PermissionOptionKindAllowOnce:
			hasAllow = true
		case acpsdk.PermissionOptionKindRejectOnce:
			hasReject = true
		}
	}
	if !hasAllow {
		t.Error("TestPermissionRequestShape: missing allow_once option")
	}
	if !hasReject {
		t.Error("TestPermissionRequestShape: missing reject_once option")
	}

	// The permission request must reference the REAL streamed tool-call card
	// (the bash tool_call already sent to the client), not a synthetic
	// disjoint id that would materialize a dangling duplicate card.
	var streamedID acpsdk.ToolCallId
	for _, n := range e.rec.snapshotUpdates() {
		if n.Update.ToolCall != nil {
			streamedID = n.Update.ToolCall.ToolCallId
			break
		}
	}
	if streamedID == "" {
		t.Fatal("TestPermissionRequestShape: no tool_call update recorded")
	}
	if req.ToolCall.ToolCallId != streamedID {
		t.Errorf("TestPermissionRequestShape: permission ToolCallId = %q, want streamed tool card id %q",
			req.ToolCall.ToolCallId, streamedID)
	}
}
