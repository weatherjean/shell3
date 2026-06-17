package notify

import (
	"encoding/json"
	"testing"
)

func TestNotification_RoundTrip(t *testing.T) {
	n := Notification{Kind: KindAgentDone, ID: "a1", Preview: "done", Transcript: "/p.jsonl"}
	b, _ := json.Marshal(n)
	var got Notification
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.ID != "a1" || got.Preview != "done" || got.Transcript != "/p.jsonl" || got.Kind != KindAgentDone {
		t.Fatalf("round-trip lost data: %#v", got)
	}
}
