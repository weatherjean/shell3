//go:build unix

package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestDriverSendRunsTurn(t *testing.T) {
	rt := shell3test.NewRuntimeForTest(t, "pong")
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(context.Background(), rt, sess)
	var gotUsage bool
	d.SetUsageRecorder(func(p, c, tot int) { gotUsage = true })
	d.Send("ping")
	waitFor(t, "turn to finish", func() bool { return !d.Busy() })
	if h := fmt.Sprint(sess.History()); !strings.Contains(h, "pong") {
		t.Fatalf("reply not in history: %s", h)
	}
	if !gotUsage {
		t.Fatal("usage recorder not called")
	}
}

// blockingLLM streams "held" and then blocks until release is closed (or ctx
// dies), holding the turn open so tests can observe mid-turn behavior.
type blockingLLM struct{ release chan struct{} }

func (b *blockingLLM) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	onEvent(llm.StreamEvent{TextDelta: "held"})
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// A second message sent while a turn runs must be interjected — steered into
// the running turn — not silently dropped.
func TestDriverSendWhileBusyInterjects(t *testing.T) {
	b := &blockingLLM{release: make(chan struct{})}
	rt := shell3test.NewRuntimeForTestClient(t, b)
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(context.Background(), rt, sess)
	d.Send("first")
	waitFor(t, "turn to start", d.Busy)
	d.Send("second — steer the turn")
	close(b.release)
	waitFor(t, "turn to finish", func() bool { return !d.Busy() })
	// The interjection must be part of the conversation, not lost.
	if h := fmt.Sprint(sess.History()); !strings.Contains(h, "second — steer the turn") {
		t.Fatalf("interjected message not in history: %s", h)
	}
}

func TestDriverAskAnswerAllow(t *testing.T) {
	d := NewDriver(context.Background(), nil, nil)
	got := make(chan bool, 1)
	go func() { got <- d.Ask(context.Background(), "rm -rf /", "gate") }()
	var id string
	waitFor(t, "ask to appear", func() bool {
		asks := d.Asks()
		if len(asks) == 1 {
			id = asks[0].ID
			return true
		}
		return false
	})
	if a := d.Asks()[0]; a.Command != "rm -rf /" || a.Reason != "gate" {
		t.Fatalf("bad ask: %+v", a)
	}
	d.Answer(id, true)
	if !<-got {
		t.Fatal("allow answer must return true")
	}
	if len(d.Asks()) != 0 {
		t.Fatal("answered ask must be cleared")
	}
	// Answering an unknown/stale id is a harmless no-op.
	d.Answer("999", true)
}

func TestDriverAskCancelledDenies(t *testing.T) {
	d := NewDriver(context.Background(), nil, nil)
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan bool, 1)
	go func() { got <- d.Ask(ctx, "x", "") }()
	waitFor(t, "ask to appear", func() bool { return len(d.Asks()) == 1 })
	cancel()
	if <-got {
		t.Fatal("cancelled ask must deny")
	}
}

func TestDriverStopCancelsTurn(t *testing.T) {
	b := &blockingLLM{release: make(chan struct{})}
	rt := shell3test.NewRuntimeForTestClient(t, b)
	sess, err := rt.Session(shell3.SessionOpts{Name: "web", Agent: "code"})
	if err != nil {
		t.Fatal(err)
	}
	d := NewDriver(context.Background(), rt, sess)
	d.Stop() // idle stop is a no-op, must not panic
	d.Send("hi")
	waitFor(t, "turn to start", d.Busy)
	d.Stop() // cancels the turn ctx; the blocked stream unblocks via ctx.Done
	waitFor(t, "turn to end after stop", func() bool { return !d.Busy() })
}
