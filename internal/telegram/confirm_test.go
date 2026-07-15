//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestParseConfirmData(t *testing.T) {
	cases := []struct {
		data string
		id   string
		yes  bool
		ok   bool
	}{
		{"bs:1:y", "1", true, true},
		{"bs:42:n", "42", false, true},
		{"bs:1:x", "", false, false}, // bad choice
		{"other:1:y", "", false, false},
		{"bs:1", "", false, false}, // too few parts
		{"", "", false, false},
	}
	for _, c := range cases {
		id, yes, ok := parseConfirmData(c.data)
		if id != c.id || yes != c.yes || ok != c.ok {
			t.Errorf("parseConfirmData(%q) = (%q,%v,%v), want (%q,%v,%v)", c.data, id, yes, ok, c.id, c.yes, c.ok)
		}
	}
}

func TestConfirmData_RoundTrips(t *testing.T) {
	for _, yes := range []bool{true, false} {
		id, got, ok := parseConfirmData(confirmData("7", yes))
		if !ok || id != "7" || got != yes {
			t.Errorf("round-trip yes=%v: got (%q,%v,%v)", yes, id, got, ok)
		}
	}
}

func newConfirmBot(fc *fakeClient) *Bot {
	return &Bot{client: fc, chatID: 1, pending: make(map[string]chan bool)}
}

func TestBotAsk_Allow(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)

	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(context.Background(), "ls -la", "reason") }()

	conf := waitConfirm(t, fc)
	if !strings.Contains(conf.text, "ls -la") {
		t.Errorf("confirm text missing command: %q", conf.text)
	}
	b.handleCallback(context.Background(), Callback{ID: "cbid", Data: conf.yesData})

	if !waitResult(t, resCh) {
		t.Fatal("Ask returned false, want true (allowed)")
	}
	if !containsSubstr(fc.editTexts(), "allowed") {
		t.Errorf("expected an edit to 'allowed' (buttons removed), got %v", fc.editTexts())
	}
	if len(fc.answered) == 0 {
		t.Error("callback was not answered — the button spinner would hang")
	}
}

// The Lua gate's {ask="...", reason=...} text is the config author's prompt to
// the human; the Telegram confirm message must surface it (shell3 dev already
// does).
func TestBotAsk_SurfacesReason(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)

	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(context.Background(), "rm -rf /data", "deletes production data") }()

	conf := waitConfirm(t, fc)
	if !strings.Contains(conf.text, "deletes production data") {
		t.Errorf("confirm text missing the ask reason: %q", conf.text)
	}
	b.handleCallback(context.Background(), Callback{ID: "cbid", Data: conf.noData})
	waitResult(t, resCh)
}

func TestBotAsk_Deny(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)

	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(context.Background(), "rm -rf x", "reason") }()

	conf := waitConfirm(t, fc)
	b.handleCallback(context.Background(), Callback{ID: "cbid", Data: conf.noData})

	if waitResult(t, resCh) {
		t.Fatal("Ask returned true, want false (denied)")
	}
	if !containsSubstr(fc.editTexts(), "denied") {
		t.Errorf("expected an edit to 'denied', got %v", fc.editTexts())
	}
}

func TestBotAsk_CtxCancelDenies(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)
	ctx, cancel := context.WithCancel(context.Background())

	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(ctx, "sleep 9", "reason") }()

	waitConfirm(t, fc)
	cancel() // turn cancelled before any tap → fail-safe deny

	if waitResult(t, resCh) {
		t.Fatal("cancelled Ask returned true, want false")
	}
}

// A second tap for the same id (already answered) must be a harmless no-op, not
// a panic (send on a full/closed channel).
func TestBotAsk_DoubleTapIsNoOp(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)

	resCh := make(chan bool, 1)
	go func() { resCh <- b.Ask(context.Background(), "ls -la", "reason") }()

	conf := waitConfirm(t, fc)
	b.handleCallback(context.Background(), Callback{ID: "cb1", Data: conf.yesData})
	if !waitResult(t, resCh) {
		t.Fatal("first tap should allow")
	}
	// Second tap after the Ask already returned (pending entry deleted): no panic.
	b.handleCallback(context.Background(), Callback{ID: "cb2", Data: conf.yesData})
	b.handleCallback(context.Background(), Callback{ID: "cb3", Data: conf.noData})
}

// Two concurrent pending approvals must stay distinct: each resolves only to its
// own button press, regardless of resolution order.
func TestBotAsk_ConcurrentPendingStayDistinct(t *testing.T) {
	fc := newFakeClient()
	b := newConfirmBot(fc)

	res1 := make(chan bool, 1)
	go func() { res1 <- b.Ask(context.Background(), "cmd ONE", "r") }()
	conf1 := waitConfirmMatching(t, fc, "ONE")

	res2 := make(chan bool, 1)
	go func() { res2 <- b.Ask(context.Background(), "cmd TWO", "r") }()
	conf2 := waitConfirmMatching(t, fc, "TWO")

	if conf1.yesData == conf2.yesData {
		t.Fatalf("the two asks shared callback_data %q — ids not distinct", conf1.yesData)
	}

	// Resolve in reverse order: deny TWO, allow ONE.
	b.handleCallback(context.Background(), Callback{ID: "a", Data: conf2.noData})
	b.handleCallback(context.Background(), Callback{ID: "b", Data: conf1.yesData})

	if waitResult(t, res2) {
		t.Fatal("ask TWO should be denied")
	}
	if !waitResult(t, res1) {
		t.Fatal("ask ONE should be allowed")
	}
}

func waitConfirm(t *testing.T, fc *fakeClient) sentConfirm {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := fc.lastConfirm(); ok {
			return c
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("no inline confirm was sent")
	return sentConfirm{}
}

func waitConfirmMatching(t *testing.T, fc *fakeClient, sub string) sentConfirm {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c, ok := fc.confirmMatching(sub); ok {
			return c
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no inline confirm matching %q was sent", sub)
	return sentConfirm{}
}

func waitResult(t *testing.T, ch <-chan bool) bool {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return")
		return false
	}
}

func containsSubstr(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
