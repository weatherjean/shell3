package acp

import (
	"context"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"
)

// TestInitialize verifies the ACP initialize handshake:
//   - protocolVersion is 1 (ProtocolVersionNumber) when client sends >= 1
//   - loadSession is advertised
//   - PromptCapabilities: Image and Audio are true
//   - AuthMethods is non-nil and empty
func TestInitialize(t *testing.T) {
	e := newTestEnv(t) // no LLM scripts needed for initialize
	ctx := context.Background()

	resp, err := e.conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: acpsdk.ProtocolVersionNumber,
		ClientInfo: &acpsdk.Implementation{
			Name:    "test-client",
			Version: "0.1.0",
		},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	// Protocol version: agent must echo ProtocolVersionNumber (1) when client sends >= 1.
	if resp.ProtocolVersion != acpsdk.ProtocolVersionNumber {
		t.Errorf("protocolVersion = %d, want %d", resp.ProtocolVersion, acpsdk.ProtocolVersionNumber)
	}

	if !resp.AgentCapabilities.LoadSession {
		t.Error("loadSession = false, want true")
	}

	// PromptCapabilities: Image and Audio must be true.
	pc := resp.AgentCapabilities.PromptCapabilities
	if !pc.Image {
		t.Error("PromptCapabilities.Image = false, want true")
	}
	if !pc.Audio {
		t.Error("PromptCapabilities.Audio = false, want true")
	}

	// AuthMethods must be non-nil and empty (not nil — wire difference matters).
	if resp.AuthMethods == nil {
		t.Error("AuthMethods is nil, want empty non-nil slice")
	}
	if len(resp.AuthMethods) != 0 {
		t.Errorf("AuthMethods len = %d, want 0", len(resp.AuthMethods))
	}

	// agentInfo must name the implementation.
	if resp.AgentInfo == nil {
		t.Fatal("AgentInfo is nil")
	}
	if resp.AgentInfo.Name != "shell3" {
		t.Errorf("AgentInfo.Name = %q, want %q", resp.AgentInfo.Name, "shell3")
	}
	if resp.AgentInfo.Version == "" {
		t.Error("AgentInfo.Version is empty")
	}
}

// TestInitialize_OlderClient verifies that a client claiming an older protocol
// version gets that version echoed back (so it can choose to disconnect).
func TestInitialize_OlderClient(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()

	resp, err := e.conn.Initialize(ctx, acpsdk.InitializeRequest{
		ProtocolVersion: 0,
	})
	if err != nil {
		t.Fatalf("Initialize (v0): %v", err)
	}
	// Client said v0 < 1, so agent echoes 0.
	if resp.ProtocolVersion != 0 {
		t.Errorf("protocolVersion = %d, want 0 (echo for older client)", resp.ProtocolVersion)
	}
}

// TestNewSession verifies that:
//   - NewSession returns a non-empty sessionId.
//   - A second NewSession returns a DIFFERENT sessionId.
//   - The cwd parameter is respected (stored in the acpSession).
func TestNewSession(t *testing.T) {
	cwd1 := t.TempDir()
	cwd2 := t.TempDir()

	e := newTestEnv(t)
	ctx := context.Background()

	// First session. McpServers must be a non-nil empty slice (SDK validates it).
	resp1, err := e.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        cwd1,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession #1: %v", err)
	}
	if resp1.SessionId == "" {
		t.Fatal("NewSession #1: sessionId is empty")
	}

	// Second session — must get a different id.
	resp2, err := e.conn.NewSession(ctx, acpsdk.NewSessionRequest{
		Cwd:        cwd2,
		McpServers: []acpsdk.McpServer{},
	})
	if err != nil {
		t.Fatalf("NewSession #2: %v", err)
	}
	if resp2.SessionId == "" {
		t.Fatal("NewSession #2: sessionId is empty")
	}
	if resp1.SessionId == resp2.SessionId {
		t.Errorf("both sessions have the same id %q", resp1.SessionId)
	}

	// Verify cwd is stored in the acpSession (accessible via acpAgent.byID).
	agent := e.getAgent(t)
	agent.mu.Lock()
	s1 := agent.byID[string(resp1.SessionId)]
	s2 := agent.byID[string(resp2.SessionId)]
	agent.mu.Unlock()

	if s1 == nil {
		t.Fatalf("session %q not found in agent.byID", resp1.SessionId)
	}
	if s2 == nil {
		t.Fatalf("session %q not found in agent.byID", resp2.SessionId)
	}
	if s1.workDir != cwd1 {
		t.Errorf("session 1 workDir = %q, want %q", s1.workDir, cwd1)
	}
	if s2.workDir != cwd2 {
		t.Errorf("session 2 workDir = %q, want %q", s2.workDir, cwd2)
	}
}
