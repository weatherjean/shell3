package acp

import (
	"context"
	"strings"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// agentText concatenates the text of every agent_message_chunk the recorder saw.
func agentText(updates []acpsdk.SessionNotification) string {
	var sb strings.Builder
	for _, n := range updates {
		if c := n.Update.AgentMessageChunk; c != nil && c.Content.Text != nil {
			sb.WriteString(c.Content.Text.Text)
		}
	}
	return sb.String()
}

// TestDisableSafetyToggleReply verifies /disable_safety toggles and reports the
// new state each time (disabled, then re-enabled).
func TestDisableSafetyToggleReply(t *testing.T) {
	e := newTestEnv(t) // commands never call the LLM, so no scripts needed
	ctx := context.Background()
	sessID := newSession(t, e.conn)

	r1, err := e.conn.Prompt(ctx, promptRequest(sessID, "/disable_safety"))
	if err != nil {
		t.Fatalf("first /disable_safety: %v", err)
	}
	if r1.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", r1.StopReason)
	}
	if got := agentText(e.rec.snapshotUpdates()); !strings.Contains(strings.ToLower(got), "disabled") {
		t.Errorf("first toggle reply = %q, want it to mention 'disabled'", got)
	}

	r2, err := e.conn.Prompt(ctx, promptRequest(sessID, "/disable_safety"))
	if err != nil {
		t.Fatalf("second /disable_safety: %v", err)
	}
	if r2.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", r2.StopReason)
	}
	if got := agentText(e.rec.snapshotUpdates()); !strings.Contains(strings.ToLower(got), "re-enabled") {
		t.Errorf("second toggle reply = %q, want it to mention 're-enabled'", got)
	}
}

// TestDisableSafetyAutoAllows verifies that after /disable_safety, a gated tool
// call runs WITHOUT any session/request_permission being sent — even though the
// recorder's permission func would REJECT. If a permission request were sent,
// the tool would be blocked; the tool completing proves the gate was skipped.
func TestDisableSafetyAutoAllows(t *testing.T) {
	e := newTestEnvWithGate(t, "run this?", "test gate",
		`tool:bash:{"command":"echo hi"}`,
		"done",
	)
	ctx := context.Background()

	// A permFunc that would REJECT if it were ever consulted.
	e.rec.permFunc = func(_ context.Context, _ acpsdk.RequestPermissionRequest) (acpsdk.RequestPermissionResponse, bool) {
		return acpsdk.RequestPermissionResponse{
			Outcome: acpsdk.NewRequestPermissionOutcomeSelected("reject"),
		}, true
	}

	sessID := newSession(t, e.conn)

	// Turn the gate off (command; consumes no LLM script).
	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "/disable_safety")); err != nil {
		t.Fatalf("/disable_safety: %v", err)
	}

	// Now the gated bash call must auto-allow with no permission round-trip.
	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "run echo hi"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}

	if perms := e.rec.snapshotPerms(); len(perms) != 0 {
		t.Errorf("expected NO permission request after /disable_safety, got %d", len(perms))
	}

	var completed bool
	for _, n := range e.rec.snapshotUpdates() {
		if tcu := n.Update.ToolCallUpdate; tcu != nil && tcu.Status != nil &&
			*tcu.Status == acpsdk.ToolCallStatusCompleted {
			completed = true
		}
	}
	if !completed {
		t.Error("bash tool_call_update not completed — auto-allow did not run the tool")
	}
}
