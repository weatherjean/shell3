package acp

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// ── replayUpdates unit test ───────────────────────────────────────────────────

// TestReplayUpdates exercises the pure replayUpdates function with a mixed
// HistoryEntry slice and asserts the discriminator of each produced update.
func TestReplayUpdates(t *testing.T) {
	entries := []shell3.HistoryEntry{
		// user turn → user_message_chunk
		{Role: "user", Content: "hello"},
		// assistant with reasoning → thought chunk + message chunk
		{Role: "assistant", Reasoning: "thinking", Content: "reply"},
		// assistant with tool calls only → StartToolCall per call
		{
			Role: "assistant",
			ToolCalls: []shell3.ToolCallInfo{
				{ID: "tc1", Name: "bash", Args: `{"command":"ls"}`},
				{ID: "tc2", Name: "read", Args: `{"path":"/foo"}`},
			},
		},
		// tool result → tool_call_update (Completed)
		{Role: "tool", ToolCallID: "tc1", Content: "file.txt"},
		// system → skipped
		{Role: "system", Content: "system reminder"},
		// another tool result
		{Role: "tool", ToolCallID: "tc2", Content: "/foo content"},
	}

	updates := replayUpdates(entries)

	// Expected discriminators (system is skipped):
	// 1. user_message_chunk (user "hello")
	// 2. agent_thought_chunk (assistant Reasoning)
	// 3. agent_message_chunk (assistant Content)
	// 4. tool_call (tc1 bash)
	// 5. tool_call (tc2 read)
	// 6. tool_call_update (tc1 result)
	// 7. tool_call_update (tc2 result)
	wantDiscs := []string{
		"user_message_chunk",
		"agent_thought_chunk",
		"agent_message_chunk",
		"tool_call",
		"tool_call",
		"tool_call_update",
		"tool_call_update",
	}

	if len(updates) != len(wantDiscs) {
		t.Fatalf("replayUpdates: got %d updates, want %d", len(updates), len(wantDiscs))
	}
	for i, u := range updates {
		disc := updateDiscriminator(t, u)
		if disc != wantDiscs[i] {
			t.Errorf("updates[%d]: discriminator = %q, want %q", i, disc, wantDiscs[i])
		}
	}

	// Assert system entry produced no update (no extra updates beyond 7).
	// (Already covered by len check above.)

	// Assert tool_call kinds are correct.
	if updates[3].ToolCall == nil {
		t.Fatal("updates[3]: ToolCall should be non-nil")
	}
	if updates[3].ToolCall.Kind != acpsdk.ToolKindExecute {
		t.Errorf("updates[3]: kind = %q, want execute", updates[3].ToolCall.Kind)
	}
	if updates[4].ToolCall == nil {
		t.Fatal("updates[4]: ToolCall should be non-nil")
	}
	if updates[4].ToolCall.Kind != acpsdk.ToolKindRead {
		t.Errorf("updates[4]: kind = %q, want read", updates[4].ToolCall.Kind)
	}

	// Assert tool_call_update status = completed.
	for _, idx := range []int{5, 6} {
		tu := updates[idx].ToolCallUpdate
		if tu == nil {
			t.Fatalf("updates[%d]: ToolCallUpdate should be non-nil", idx)
		}
		if tu.Status == nil || *tu.Status != acpsdk.ToolCallStatusCompleted {
			t.Errorf("updates[%d]: status = %v, want completed", idx, tu.Status)
		}
	}
}

// updateDiscriminator marshals a SessionUpdate and returns the "sessionUpdate"
// discriminator field. Uses wireDiscriminator from mapping_test.go (same package).
func updateDiscriminator(t *testing.T, u acpsdk.SessionUpdate) string {
	t.Helper()
	b, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal SessionUpdate: %v", err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal to map: %v", err)
	}
	raw, ok := m["sessionUpdate"]
	if !ok {
		t.Fatalf("no sessionUpdate field in JSON: %s", b)
	}
	var disc string
	if err := json.Unmarshal(raw, &disc); err != nil {
		t.Fatalf("unmarshal discriminator: %v", err)
	}
	return disc
}

// ── TestListSessions ──────────────────────────────────────────────────────────

// TestListSessions verifies that a session created via NewSession + Prompt
// is visible in the ListSessions response with the correct sessionId.
func TestListSessions(t *testing.T) {
	e := newTestEnv(t, "Hello world")
	ctx := context.Background()

	// Create a session and run a prompt so the session has persisted messages.
	sessID := newSession(t, e.conn)
	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "hi")); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	resp, err := e.conn.ListSessions(ctx, acpsdk.ListSessionsRequest{})
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}

	var found bool
	for _, si := range resp.Sessions {
		if si.SessionId == sessID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("ListSessions: session %q not found in %d results", sessID, len(resp.Sessions))
	}
}

// ── TestLoadSessionReplays ────────────────────────────────────────────────────

// TestLoadSessionReplays creates a session in env1, completes a prompt (so
// messages are persisted), then opens env2 on the SAME workdir and calls
// LoadSession. It asserts:
//  1. replay updates were delivered to env2 before LoadSession returned (ordering)
//  2. the LoadSessionResponse contains a Modes field
func TestLoadSessionReplays(t *testing.T) {
	e1 := newTestEnv(t, "Hello")
	ctx := context.Background()

	// Create a session in env1 and complete a turn.
	sessID := newSession(t, e1.conn)
	if _, err := e1.conn.Prompt(ctx, promptRequest(sessID, "hi")); err != nil {
		t.Fatalf("env1 Prompt: %v", err)
	}

	// env2 shares the same workDir → same .shell3_project/runs/ store.
	e2 := newTestEnvSameDir(t, e1)

	// LoadSession on env2 — this call blocks until the agent responds.
	resp, err := e2.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		SessionId:  sessID,
		Cwd:        e1.workDir,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	// Ordering assertion: the recorder must already hold replay updates by the
	// time LoadSession returns, because the agent emits notifications via the
	// ordered io.Pipe BEFORE sending the LoadSession response.
	updates := e2.rec.snapshotUpdates()
	var foundUserChunk, foundAgentChunk bool
	for _, n := range updates {
		if n.Update.UserMessageChunk != nil {
			foundUserChunk = true
		}
		if n.Update.AgentMessageChunk != nil {
			foundAgentChunk = true
		}
	}
	if !foundUserChunk {
		t.Errorf("LoadSession replay: no user_message_chunk in %d updates before load returned", len(updates))
	}
	if !foundAgentChunk {
		t.Errorf("LoadSession replay: no agent_message_chunk in %d updates before load returned", len(updates))
	}

	// Response must include Modes (agents are configured in the test lua).
	if resp.Modes == nil {
		t.Error("LoadSessionResponse.Modes is nil, want non-nil SessionModeState")
	}
}

// ── TestLoadUnknownSession ────────────────────────────────────────────────────

// TestLoadUnknownSession verifies that LoadSession with a bogus id returns a
// JSON-RPC InvalidParams error (code -32602).
func TestLoadUnknownSession(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	_, err := e.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		SessionId:  "bogus-nonexistent-id-12345",
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err == nil {
		t.Fatal("LoadSession with bogus id: want error, got nil")
	}

	// The error must be InvalidParams (-32602), not just any error.
	var reqErr *acpsdk.RequestError
	if rErr, ok := err.(*acpsdk.RequestError); ok {
		reqErr = rErr
	}
	if reqErr == nil {
		t.Fatalf("LoadSession error is not a *RequestError: %T %v", err, err)
	}
	const wantCode = -32602 // JSON-RPC InvalidParams
	if reqErr.Code != wantCode {
		t.Errorf("error code = %d, want %d (InvalidParams)", reqErr.Code, wantCode)
	}
}

// ── TestLoadOrResumeAlreadyOpenErrors ─────────────────────────────────────────

// TestLoadOrResumeAlreadyOpenErrors verifies the double-open guard: once a
// session is registered (open) in the agent, calling LoadSession or
// ResumeSession with the SAME id on the SAME connection returns a JSON-RPC
// InvalidRequest error (code -32600) instead of minting a second *shell3.Session
// against the same runs-store record. It also asserts no corruption: the
// original session stays registered in byID and continues to serve prompts.
func TestLoadOrResumeAlreadyOpenErrors(t *testing.T) {
	// Three scripts: initial prompt (persist history), then two follow-up prompts
	// (one to prove the original session still works after each guarded call).
	e := newTestEnv(t, "Hello", "Still here", "Yep")
	ctx := context.Background()

	// Create a session and run a prompt so it has persisted messages (validation
	// in the guarded handlers passes only for a session known to the store).
	sessID := newSession(t, e.conn)
	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "hi")); err != nil {
		t.Fatalf("initial Prompt: %v", err)
	}

	// The session is still open (registered in byID) at this point.
	agent := e.getAgent(t)
	agent.mu.Lock()
	original := agent.byID[string(sessID)]
	agent.mu.Unlock()
	if original == nil {
		t.Fatal("precondition: session not registered in byID after NewSession+Prompt")
	}

	const wantCode = -32600 // JSON-RPC InvalidRequest (acp.NewInvalidRequest)

	assertInvalidRequest := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Fatalf("%s on already-open id: want error, got nil", name)
		}
		reqErr, ok := err.(*acpsdk.RequestError)
		if !ok {
			t.Fatalf("%s error is not a *RequestError: %T %v", name, err, err)
		}
		if reqErr.Code != wantCode {
			t.Errorf("%s error code = %d, want %d (InvalidRequest)", name, reqErr.Code, wantCode)
		}
	}

	// LoadSession on the already-open id → InvalidRequest.
	_, err := e.conn.LoadSession(ctx, acpsdk.LoadSessionRequest{
		SessionId:  sessID,
		Cwd:        e.workDir,
		McpServers: []acpsdk.McpServer{},
	})
	assertInvalidRequest("LoadSession", err)

	// The original session must be untouched and still functional.
	agent.mu.Lock()
	afterLoad := agent.byID[string(sessID)]
	agent.mu.Unlock()
	if afterLoad != original {
		t.Error("byID no longer maps to the original session after guarded LoadSession")
	}
	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "still working?")); err != nil {
		t.Fatalf("Prompt on original session after guarded LoadSession: %v", err)
	}

	// ResumeSession on the already-open id → InvalidRequest.
	_, err = e.conn.ResumeSession(ctx, acpsdk.ResumeSessionRequest{
		SessionId: sessID,
		Cwd:       e.workDir,
	})
	assertInvalidRequest("ResumeSession", err)

	// Still the same original session, still functional.
	agent.mu.Lock()
	afterResume := agent.byID[string(sessID)]
	agent.mu.Unlock()
	if afterResume != original {
		t.Error("byID no longer maps to the original session after guarded ResumeSession")
	}
	if _, err := e.conn.Prompt(ctx, promptRequest(sessID, "still working 2?")); err != nil {
		t.Fatalf("Prompt on original session after guarded ResumeSession: %v", err)
	}
}

// ── TestCloseSession ──────────────────────────────────────────────────────────

// TestCloseSession verifies that after CloseSession:
//   - The close succeeds without error.
//   - A subsequent Prompt on the same session id returns an error.
func TestCloseSession(t *testing.T) {
	e := newTestEnv(t, "Hello")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	// Close the session.
	_, err := e.conn.CloseSession(ctx, acpsdk.CloseSessionRequest{
		SessionId: sessID,
	})
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	// Subsequent prompt on the closed session must fail.
	_, err = e.conn.Prompt(ctx, promptRequest(sessID, "hello after close"))
	if err == nil {
		t.Fatal("Prompt on closed session: want error, got nil")
	}

	// Also verify the session is no longer in byID.
	agent := e.getAgent(t)
	agent.mu.Lock()
	s := agent.byID[string(sessID)]
	agent.mu.Unlock()
	if s != nil {
		t.Error("CloseSession: session still registered in agent.byID after close")
	}
}

// ── TestInitializeAfterTask8 ──────────────────────────────────────────────────

// TestInitializeAfterTask8 verifies that Initialize now advertises LoadSession=true
// and the session capabilities for List, Close, and Resume.
func TestInitializeAfterTask8(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	resp, err := e.conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo:      &acpsdk.Implementation{Name: "test-client", Version: "0.1.0"},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	if !resp.AgentCapabilities.LoadSession {
		t.Error("AgentCapabilities.LoadSession = false, want true after Task 8")
	}

	sc := resp.AgentCapabilities.SessionCapabilities
	if sc.List == nil {
		t.Error("SessionCapabilities.List is nil, want non-nil ({})")
	}
	if sc.Close == nil {
		t.Error("SessionCapabilities.Close is nil, want non-nil ({})")
	}
	if sc.Resume == nil {
		t.Error("SessionCapabilities.Resume is nil, want non-nil ({})")
	}
}

// ── bounded-wait helper ───────────────────────────────────────────────────────

// waitForCondition polls cond up to timeout. Returns true if cond was satisfied.
// Used where notifications arrive asynchronously. Do not use for ordering proofs
// (use snapshot-after-blocking-call instead).
func waitForCondition(cond func() bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
