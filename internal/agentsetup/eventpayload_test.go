package agentsetup

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
)

func TestEventPayloadCarriesIdentity(t *testing.T) {
	b := eventPayload("main", chat.Event{
		Kind:      chat.EventTurnDone,
		SessionID: "sess-1",
		Time:      time.Unix(0, 0).UTC(),
	})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("payload is not JSON: %v (%s)", err, b)
	}
	if got["event"] != "turn_done" || got["agent"] != "main" || got["session"] != "sess-1" {
		t.Fatalf("payload = %s", b)
	}
}

func TestEventPayloadCarriesToolFields(t *testing.T) {
	b := eventPayload("main", chat.Event{
		Kind:       chat.EventToolResult,
		ToolName:   "bash",
		ToolOutput: "hello",
		ToolError:  true,
	})
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["tool"] != "bash" || got["output"] != "hello" || got["tool_error"] != true {
		t.Fatalf("payload = %s", b)
	}
}

func TestEventPayloadCarriesUsage(t *testing.T) {
	b := eventPayload("main", chat.Event{
		Kind:  chat.EventTurnDone,
		Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 3, TotalTokens: 13},
	})
	var got struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Usage == nil || got.Usage.PromptTokens != 10 || got.Usage.CompletionTokens != 3 {
		t.Fatalf("payload = %s", b)
	}
}

func TestEventPayloadCapsText(t *testing.T) {
	long := make([]byte, eventTextCap*2)
	for i := range long {
		long[i] = 'x'
	}
	b := eventPayload("main", chat.Event{Kind: chat.EventAssistantMessage, Text: string(long)})
	var got struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Text) > eventTextCap+64 {
		t.Fatalf("text is %d bytes, want it capped near %d", len(got.Text), eventTextCap)
	}
}
