package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// promptRequest is a helper to build a minimal PromptRequest.
func promptRequest(sessID acpsdk.SessionId, text string) acpsdk.PromptRequest {
	return acpsdk.PromptRequest{
		SessionId: sessID,
		Prompt:    []acpsdk.ContentBlock{acpsdk.TextBlock(text)},
	}
}

// newSession is a helper to call NewSession and return the SessionId.
func newSession(t *testing.T, conn *acpsdk.ClientSideConnection) acpsdk.SessionId {
	t.Helper()
	resp, err := conn.NewSession(context.Background(), acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return resp.SessionId
}

// TestPromptStreamsTextAndStops verifies that a plain-text turn:
//   - returns StopReason == end_turn
//   - streams at least one agent_message_chunk update
func TestPromptStreamsTextAndStops(t *testing.T) {
	e := newTestEnv(t, "Hello world")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "say hello"))
	if err != nil {
		t.Fatalf("Prompt: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want %q", resp.StopReason, acpsdk.StopReasonEndTurn)
	}

	updates := e.rec.snapshotUpdates()
	var found bool
	for _, n := range updates {
		if n.Update.AgentMessageChunk != nil {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no agent_message_chunk in %d recorded updates", len(updates))
	}
}

// TestPromptStreamsToolCall verifies that a tool-call turn forwards both a
// tool_call (execute kind) and a tool_call_update (completed) to the client.
func TestPromptStreamsToolCall(t *testing.T) {
	e := newTestEnv(t, `tool:bash:{"command":"echo hi"}`, "done")
	ctx := context.Background()

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
		t.Error("no tool_call update recorded")
	}
	if !foundToolCallUpdate {
		t.Error("no tool_call_update recorded")
	}
}

// TestCancelMidTurn verifies that sending session/cancel while a Prompt is
// in flight causes Prompt to return StopReason == cancelled.
//
// Gate mechanism: the fake LLM serves a "block" script that streams one token
// then waits on r.Context().Done() (cancelled when the HTTP request is
// cancelled by turnCtx cancellation). The test waits for the first
// agent_message_chunk in the recorder before sending the cancel notification.
func TestCancelMidTurn(t *testing.T) {
	e := newTestEnv(t, "block")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	type result struct {
		resp acpsdk.PromptResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		r, err := e.conn.Prompt(ctx, promptRequest(sessID, "do something"))
		done <- result{r, err}
	}()

	// Wait until at least one update appears (turn is in flight).
	if !e.rec.waitForFirstUpdate(5 * time.Second) {
		t.Fatal("no update received before cancel timeout — turn may not have started")
	}

	// Send cancel notification.
	if err := e.conn.Cancel(ctx, acpsdk.CancelNotification{
		SessionId: sessID,
	}); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	// Prompt must return with StopReason == cancelled.
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("Prompt error after cancel: %v", res.err)
		}
		if res.resp.StopReason != acpsdk.StopReasonCancelled {
			t.Errorf("StopReason = %q, want %q", res.resp.StopReason, acpsdk.StopReasonCancelled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Prompt did not return within 5 s after cancel")
	}
}

// TestPromptWhileBusy verifies ACP prompt-supersede semantics: issuing a
// second Prompt on the same session while the first is still in flight makes
// the SDK cancel the first prompt's ctx; the agent waits for the first turn
// to unwind and then runs the second turn. The first Prompt returns
// stopReason cancelled; the second succeeds with end_turn.
func TestPromptWhileBusy(t *testing.T) {
	// Script 1 blocks until its request ctx is cancelled (the supersede);
	// script 2 answers the superseding prompt.
	e := newTestEnv(t, "block", "second answer")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	type result struct {
		resp acpsdk.PromptResponse
		err  error
	}
	firstDone := make(chan result, 1)
	go func() {
		r, err := e.conn.Prompt(ctx, promptRequest(sessID, "first"))
		firstDone <- result{r, err}
	}()

	// Wait until the first turn is visibly in flight.
	if !e.rec.waitForFirstUpdate(5 * time.Second) {
		t.Fatal("first Prompt never started (no updates)")
	}

	// Second Prompt on the same session supersedes the first: it must succeed
	// (after the first turn is cancelled and unwinds), not error.
	resp2, err := e.conn.Prompt(ctx, promptRequest(sessID, "second"))
	if err != nil {
		t.Fatalf("second Prompt should supersede the first, got error: %v", err)
	}
	if resp2.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("second Prompt StopReason = %q, want %q", resp2.StopReason, acpsdk.StopReasonEndTurn)
	}

	// The first Prompt must have returned stopReason cancelled (its ctx was
	// cancelled by the SDK when the second prompt arrived).
	select {
	case res := <-firstDone:
		if res.err != nil {
			t.Fatalf("first Prompt error after supersede: %v", res.err)
		}
		if res.resp.StopReason != acpsdk.StopReasonCancelled {
			t.Errorf("first Prompt StopReason = %q, want %q", res.resp.StopReason, acpsdk.StopReasonCancelled)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first Prompt did not return after being superseded")
	}
}
