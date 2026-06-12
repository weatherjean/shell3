package notify

import (
	"encoding/json"
	"testing"
)

func TestNotification_RoundTrip(t *testing.T) {
	n := Notification{Kind: KindAgentDone, ID: "a1", Preview: "done", Origin: 9}
	b, _ := json.Marshal(n)
	var got Notification
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Origin != 9 || got.Kind != KindAgentDone {
		t.Fatalf("round-trip lost data: %#v", got)
	}
}
