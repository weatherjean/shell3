package cli

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// renderDevEventsReply must return the turn's assembled reply text so the
// heartbeat driver can apply the suppression verdict to it.
func TestRenderDevEventsReply(t *testing.T) {
	ch := make(chan shell3.Event, 4)
	ch <- shell3.Event{Kind: shell3.Token, Text: "HEARTBEAT"}
	ch <- shell3.Event{Kind: shell3.Token, Text: "_OK"}
	ch <- shell3.Event{Kind: shell3.Done}
	close(ch)

	var b strings.Builder
	reply, err := renderDevEventsReply(&b, ch)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reply != "HEARTBEAT_OK" {
		t.Fatalf("want assembled reply %q, got %q", "HEARTBEAT_OK", reply)
	}
}

// HeartbeatVerdict renders what the Telegram host would do with a heartbeat
// turn's reply: suppressed for a bare sentinel, delivered (token-stripped)
// otherwise.
func TestHeartbeatVerdict(t *testing.T) {
	if v := HeartbeatVerdict("HEARTBEAT_OK"); !strings.Contains(v, "suppress") {
		t.Fatalf("bare sentinel must report suppression, got %q", v)
	}
	v := HeartbeatVerdict("disk is 95% full\nHEARTBEAT_OK")
	if !strings.Contains(v, "deliver") || !strings.Contains(v, "disk is 95% full") {
		t.Fatalf("alert must report delivery with the stripped text, got %q", v)
	}
	if strings.Contains(strings.TrimPrefix(v, "· heartbeat"), "HEARTBEAT_OK") {
		t.Fatalf("delivered text must not carry the sentinel, got %q", v)
	}
}
