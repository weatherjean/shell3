package acp

import (
	"context"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// TestNewSessionAdvertisesModes verifies that NewSession returns a Modes field
// with AvailableModes == ["code", "plan"] (set check) and CurrentModeId == "code"
// (the default agent declared in the test harness lua).
func TestNewSessionAdvertisesModes(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	resp, err := e.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        t.TempDir(),
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if resp.Modes == nil {
		t.Fatal("NewSession: Modes is nil, want non-nil SessionModeState")
	}

	modes := resp.Modes
	if modes.CurrentModeId != "code" {
		t.Errorf("CurrentModeId = %q, want %q", modes.CurrentModeId, "code")
	}

	if len(modes.AvailableModes) != 2 {
		t.Errorf("AvailableModes len = %d, want 2, modes = %v", len(modes.AvailableModes), modes.AvailableModes)
	}

	byID := make(map[string]acpsdk.SessionMode, len(modes.AvailableModes))
	for _, m := range modes.AvailableModes {
		byID[string(m.Id)] = m
	}
	for _, want := range []string{"code", "plan"} {
		m, ok := byID[want]
		if !ok {
			t.Errorf("AvailableModes missing %q", want)
			continue
		}
		if m.Name != want {
			t.Errorf("mode %q Name = %q, want %q", want, m.Name, want)
		}
	}
}

// TestSetSessionMode verifies that:
//   - Switching to "plan" returns a non-error response.
//   - A current_mode_update notification with CurrentModeId "plan" is emitted.
//   - The underlying shell3 session now reports "plan" as its active agent.
func TestSetSessionMode(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	_, err := e.conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sessID,
		ModeId:    "plan",
	})
	if err != nil {
		t.Fatalf("SetSessionMode: %v", err)
	}

	// Notifications are delivered asynchronously through the SDK pipe; poll
	// with a bounded timeout rather than sleeping a fixed duration.
	deadline := time.Now().Add(2 * time.Second)
	var foundUpdate bool
	for !foundUpdate && time.Now().Before(deadline) {
		for _, n := range e.rec.snapshotUpdates() {
			if n.Update.CurrentModeUpdate != nil &&
				n.Update.CurrentModeUpdate.CurrentModeId == acpsdk.SessionModeId("plan") {
				foundUpdate = true
				break
			}
		}
		if !foundUpdate {
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !foundUpdate {
		t.Errorf("no current_mode_update with CurrentModeId %q found in updates", "plan")
	}

	// Verify the underlying shell3 session switched agents.
	agent := e.getAgent(t)
	agent.mu.Lock()
	s := agent.byID[string(sessID)]
	agent.mu.Unlock()
	if s == nil {
		t.Fatal("session not found in agent registry after SetSessionMode")
	}
	if got := s.sess.ActiveAgent(); got != "plan" {
		t.Errorf("ActiveAgent() = %q, want %q", got, "plan")
	}
}

// TestSetSessionModeUnknown verifies that SetSessionMode with a bogus modeId
// returns an error (the agent should reject unknown mode IDs with InvalidParams).
func TestSetSessionModeUnknown(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	_, err := e.conn.SetSessionMode(ctx, acpsdk.SetSessionModeRequest{
		SessionId: sessID,
		ModeId:    "bogus-nonexistent-mode",
	})
	if err == nil {
		t.Fatal("SetSessionMode with bogus modeId: want error, got nil")
	}
}
