//go:build unix

package telegram

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func TestInboxQueuesCoalescesAndClears(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Both tasks finished."}}})
	b := newBot(t, newFakeClient(), shell3test.NewRuntimeForTestClient(t, fake))
	c := tconv(b)
	store := inbox.Store{Root: t.TempDir()}
	for _, body := range []string{"result one", "result two"} {
		if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	c.turnActive = true
	if err := b.WakeInbox(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	if fake.CallCount() != 0 {
		t.Fatal("overlapped busy turn")
	}
	c.turnActive = false
	b.activeTurns = b.maxTurns
	if c.startInboxTurn(t.Context(), time.Now()) {
		t.Fatal("exceeded global cap")
	}
	b.activeTurns = 0
	if !c.startInboxTurn(t.Context(), time.Now()) {
		t.Fatal("lost queued delivery")
	}
	waitPollTurn(t, c)
	if fake.CallCount() != 1 {
		t.Fatalf("calls=%d", fake.CallCount())
	}
	msgs := fake.CallsSnapshot()[0].Msgs
	input := msgs[len(msgs)-1].Content
	if !strings.Contains(input, "result one") || !strings.Contains(input, "result two") {
		t.Fatal("missing complete notice contents")
	}
	if _, count, err := store.List("main", inbox.StatusPending, 0, 10); err != nil || count != 0 {
		t.Fatalf("pending=%d err=%v", count, err)
	}
	c.mu.Lock()
	pending := c.inboxPending
	c.mu.Unlock()
	if pending {
		t.Fatal("empty inbox still competing with timed checks")
	}
}

func TestInboxRespectsStopResetRestartAndUserPriority(t *testing.T) {
	for _, mode := range []string{"/stop", "/superstop", "/new", "restart", "burst", "queue"} {
		t.Run(mode, func(t *testing.T) {
			fake := fakellm.New()
			b := newBot(t, newFakeClient(), shell3test.NewRuntimeForTestClient(t, fake))
			c := tconv(b)
			if _, err := c.mainSession(); err != nil {
				t.Fatal(err)
			}
			store := inbox.Store{Root: t.TempDir()}
			if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "result"}); err != nil {
				t.Fatal(err)
			}
			c.inboxStore, c.inboxPending = &store, true
			switch mode {
			case "restart":
				b.armRestart()
			case "burst":
				c.burst = []inboundMessage{{text: "user first"}}
			case "queue":
				c.pendingMessages = []inboundMessage{{text: "user first"}}
			default:
				c.handleCommand(t.Context(), Msg{Text: mode})
			}
			if c.startInboxTurn(t.Context(), time.Now()) {
				t.Fatalf("started after %s", mode)
			}
			if fake.CallCount() != 0 {
				t.Fatal("unexpected model call")
			}
		})
	}
}

func TestInboxFailureBacksOffAndRetries(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Err: errors.New("provider failed")}, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "Recovered."}}})
	b := newBot(t, newFakeClient(), shell3test.NewRuntimeForTestClient(t, fake))
	c := tconv(b)
	store := inbox.Store{Root: t.TempDir()}
	if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "done", Body: "result"}); err != nil {
		t.Fatal(err)
	}
	if err := b.WakeInbox(t.Context(), store); err != nil {
		t.Fatal(err)
	}
	waitPollTurn(t, c)
	if c.startInboxTurn(t.Context(), time.Now()) {
		t.Fatal("immediate retry after failure")
	}
	if _, count, err := store.List("main", inbox.StatusPending, 0, 10); err != nil || count != 1 {
		t.Fatalf("lost failed delivery: count=%d err=%v", count, err)
	}
	if !c.startInboxTurn(t.Context(), time.Now().Add(2*time.Minute)) {
		t.Fatal("retry lost")
	}
	waitPollTurn(t, c)
	if _, count, err := store.List("main", inbox.StatusPending, 0, 10); err != nil || count != 0 {
		t.Fatalf("retry pending=%d err=%v", count, err)
	}
}
