package acp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

// collectAgentText concatenates all AgentMessageChunk text values from a recorder
// snapshot. Used to assert the content of command replies without polling.
func collectAgentText(updates []acpsdk.SessionNotification) string {
	var sb strings.Builder
	for _, n := range updates {
		if n.Update.AgentMessageChunk != nil {
			if n.Update.AgentMessageChunk.Content.Text != nil {
				sb.WriteString(n.Update.AgentMessageChunk.Content.Text.Text)
			}
		}
	}
	return sb.String()
}

// TestAdvertisesCommandsOnNewSession verifies that after session/new the recorder
// receives an available_commands_update whose JSON discriminator is exactly
// "available_commands_update" and whose command list includes clear, compact, help.
func TestAdvertisesCommandsOnNewSession(t *testing.T) {
	e := newTestEnv(t)
	_ = newSession(t, e.conn)

	// Poll: the notification is sent within the NewSession handler but may be
	// dispatched to the recorder asynchronously on the client side.
	ok := waitForCondition(func() bool {
		for _, n := range e.rec.snapshotUpdates() {
			if n.Update.AvailableCommandsUpdate != nil {
				return true
			}
		}
		return false
	}, 5*time.Second)
	if !ok {
		t.Fatal("no available_commands_update received within 5 s after NewSession")
	}

	// Find the update and check the wire discriminator + command names.
	for _, n := range e.rec.snapshotUpdates() {
		if n.Update.AvailableCommandsUpdate == nil {
			continue
		}

		// Verify wire discriminator via JSON marshal.
		b, err := json.Marshal(n.Update)
		if err != nil {
			t.Fatalf("marshal SessionUpdate: %v", err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("unmarshal to map: %v", err)
		}
		rawDisc, ok := m["sessionUpdate"]
		if !ok {
			t.Fatal("JSON has no sessionUpdate discriminator field")
		}
		var disc string
		if err := json.Unmarshal(rawDisc, &disc); err != nil {
			t.Fatalf("unmarshal discriminator: %v", err)
		}
		if disc != "available_commands_update" {
			t.Errorf("sessionUpdate discriminator = %q, want %q", disc, "available_commands_update")
		}

		// Verify the command names.
		gotNames := make(map[string]bool)
		for _, cmd := range n.Update.AvailableCommandsUpdate.AvailableCommands {
			gotNames[cmd.Name] = true
		}
		for _, want := range []string{"clear", "compact", "help"} {
			if !gotNames[want] {
				t.Errorf("available_commands_update missing command %q; got names: %v", want, gotNames)
			}
		}
		return // exactly one update expected; checked
	}
}

// TestClearCommand verifies that prompting "/clear" resets history, returns
// StopReason end_turn, and does NOT invoke the LLM.
func TestClearCommand(t *testing.T) {
	// LLM-SENTINEL is a distinctive string; if it appears in the recorder the LLM was called.
	e := newTestEnv(t, "LLM-SENTINEL")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "/clear"))
	if err != nil {
		t.Fatalf("Prompt /clear: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}

	text := collectAgentText(e.rec.snapshotUpdates())
	if !strings.Contains(strings.ToLower(text), "cleared") {
		t.Errorf("reply %q does not contain 'cleared'", text)
	}
	if strings.Contains(text, "LLM-SENTINEL") {
		t.Error("LLM was called for /clear (found sentinel text in agent updates)")
	}
}

// TestCompactCommand verifies that "/compact" queues compaction and does not call the LLM.
func TestCompactCommand(t *testing.T) {
	e := newTestEnv(t, "LLM-SENTINEL")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "/compact"))
	if err != nil {
		t.Fatalf("Prompt /compact: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}

	text := collectAgentText(e.rec.snapshotUpdates())
	lower := strings.ToLower(text)
	if !strings.Contains(lower, "queued") && !strings.Contains(lower, "next message") {
		t.Errorf("compact reply %q does not mention 'queued' or 'next message'", text)
	}
	if strings.Contains(text, "LLM-SENTINEL") {
		t.Error("LLM was called for /compact (found sentinel text in agent updates)")
	}
}

// TestHelpCommand verifies that "/help" lists all registered commands.
func TestHelpCommand(t *testing.T) {
	e := newTestEnv(t, "LLM-SENTINEL")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "/help"))
	if err != nil {
		t.Fatalf("Prompt /help: %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("StopReason = %q, want end_turn", resp.StopReason)
	}

	text := collectAgentText(e.rec.snapshotUpdates())
	for _, name := range []string{"clear", "compact", "help"} {
		if !strings.Contains(text, name) {
			t.Errorf("help reply %q does not mention command %q", text, name)
		}
	}
	if strings.Contains(text, "LLM-SENTINEL") {
		t.Error("LLM was called for /help (found sentinel text in agent updates)")
	}
}

// TestNonCommandPassesToLLM verifies that a normal prompt and a slash-prefixed
// non-command both pass through to the LLM (the LLM scripts are consumed).
func TestNonCommandPassesToLLM(t *testing.T) {
	// Two scripts: one for "hello", one for "/etc/passwd".
	e := newTestEnv(t, "hi there", "not a command")
	ctx := context.Background()

	sessID := newSession(t, e.conn)

	// A normal prompt must consume the first LLM script.
	resp, err := e.conn.Prompt(ctx, promptRequest(sessID, "hello"))
	if err != nil {
		t.Fatalf("Prompt 'hello': %v", err)
	}
	if resp.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("'hello' StopReason = %q, want end_turn", resp.StopReason)
	}
	text1 := collectAgentText(e.rec.snapshotUpdates())
	if !strings.Contains(text1, "hi there") {
		t.Errorf("first LLM script 'hi there' not in agent text %q", text1)
	}

	// A slash-prefixed path that is not a registered command must also go to the LLM.
	resp2, err := e.conn.Prompt(ctx, promptRequest(sessID, "/etc/passwd"))
	if err != nil {
		t.Fatalf("Prompt '/etc/passwd': %v", err)
	}
	if resp2.StopReason != acpsdk.StopReasonEndTurn {
		t.Errorf("'/etc/passwd' StopReason = %q, want end_turn", resp2.StopReason)
	}
	// Snapshot after second turn: both scripts' text should now be present.
	text2 := collectAgentText(e.rec.snapshotUpdates())
	if !strings.Contains(text2, "not a command") {
		t.Errorf("second LLM script 'not a command' not in agent text %q after '/etc/passwd'", text2)
	}
}
