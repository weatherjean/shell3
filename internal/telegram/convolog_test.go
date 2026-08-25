//go:build unix

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// sendFailClient is a transport whose plain sends are rejected — a Telegram
// outage, from the log's point of view.
type sendFailClient struct {
	*fakeClient
}

func (sendFailClient) Send(context.Context, int64, string, ...SendOpt) (string, error) {
	return "", errSendRejected
}

var errSendRejected = errors.New("telegram: bad gateway")

// decodeConvo reads the JSONL log back.
func decodeConvo(t *testing.T, buf *bytes.Buffer) []convoEvent {
	t.Helper()
	var out []convoEvent
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var ev convoEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// findConvo returns the first event matching dir+kind, failing if none does.
func findConvo(t *testing.T, evs []convoEvent, dir, kind string) convoEvent {
	t.Helper()
	for _, ev := range evs {
		if ev.Dir == dir && ev.Kind == kind {
			return ev
		}
	}
	t.Fatalf("no %s/%s event in %+v", dir, kind, evs)
	return convoEvent{}
}

// The whole point of the log: a HOST-answered reply — no model turn, no
// message row, no app-log line — is on the wire record. Before this, the only
// copy of `❌ reload failed: …` was in the operator's chat.
func TestConvoLog_RecordsHostPostWithNoMessageRow(t *testing.T) {
	var buf bytes.Buffer
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	b.SetConvoLog(&buf)

	b.conv(42).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "/quiet on"})

	// The kind is send/send_html depending on whether the reply rendered as
	// markdown; what must hold is that the text reached the record at all.
	evs := decodeConvo(t, &buf)
	if len(evs) != 1 || evs[0].Dir != "out" {
		t.Fatalf("want exactly one outbound event, got %+v", evs)
	}
	ev := evs[0]
	if !strings.Contains(ev.Text, "quiet") {
		t.Fatalf("host reply not recorded: %+v", ev)
	}
	if ev.TS == "" || ev.Chat != 42 {
		t.Fatalf("event missing its stamp or room: %+v", ev)
	}
}

// Inbound is logged BELOW the gates, so a message the bot deliberately ignored
// still appears. That case leaves no other evidence at all — the bot discards
// it before a room exists — and "why did it ignore me" is unanswerable without
// it.
func TestConvoLog_RecordsMessageDroppedByTheSenderGate(t *testing.T) {
	var buf bytes.Buffer
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	b.SetConvoLog(&buf)
	if err := b.SetAllowFrom([]string{"42"}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := b.client.Updates(ctx) // the wrapped transport
	fc.in <- Msg{ChatID: 99, SenderID: 99, ID: "7", Text: "let me in", ChatType: "private"}
	<-updates

	ev := findConvo(t, decodeConvo(t, &buf), "in", "msg")
	if ev.Text != "let me in" || ev.Sender != 99 {
		t.Fatalf("inbound not recorded verbatim: %+v", ev)
	}
	if ev.ChatType != "private" {
		t.Fatalf("chat type is what explains a group drop; missing: %+v", ev)
	}
}

// A send the transport REJECTED is recorded with its error, so a post the user
// never saw is distinguishable from one they did. This is why sends are logged
// after the call, not before it.
func TestConvoLog_RecordsSendError(t *testing.T) {
	var buf bytes.Buffer
	c := newConvoLogClient(&sendFailClient{fakeClient: newFakeClient()}, &buf)

	if _, err := c.Send(context.Background(), 42, "does not land"); err == nil {
		t.Fatal("the fake was told to fail")
	}
	if ev := findConvo(t, decodeConvo(t, &buf), "out", "send"); ev.Err == "" {
		t.Fatalf("a rejected send must carry its error: %+v", ev)
	}
}

// Attachment bytes are never written: the log stays greppable, and does not
// become a second copy of the media dir.
func TestConvoLog_DescribesMediaWithoutEmbeddingIt(t *testing.T) {
	var buf bytes.Buffer
	c := newConvoLogClient(newFakeClient(), &buf)

	_, _ = c.SendDocument(context.Background(), 42, "reply.md", []byte("0123456789"), "long reply")

	ev := findConvo(t, decodeConvo(t, &buf), "out", "document")
	if ev.File != "reply.md" || ev.Bytes != 10 {
		t.Fatalf("attachment not described: %+v", ev)
	}
	if strings.Contains(buf.String(), "0123456789") {
		t.Fatal("attachment bytes leaked into the log")
	}
}
