//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

// TestStopCancelsInFlightTurn proves that /stop reaches and cancels a turn that
// is still running: handleMsg must not block the bot, and /stop must unwind the
// in-flight turn (turnActive clears) and reply "stopped".
func TestStopCancelsInFlightTurn(t *testing.T) {
	fc := newFakeClient()
	blk := shell3test.NewBlockingLLM()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	sess, err := rt.Session(shell3.SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b := NewBot(fc, rt, sess, 42)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, Text: "do work"})

	// Wait for the turn to be in flight (proves handleMsg launched it).
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started: handleMsg did not launch the turn")
	}

	// The turn is in flight: turnActive must be true (the loop stayed responsive
	// because handleMsg ran the turn on its own goroutine).
	b.mu.Lock()
	inflight := b.turnActive
	b.mu.Unlock()
	if !inflight {
		t.Fatal("turnActive is false while a turn is in flight: handleMsg did not mark the turn active on its own goroutine")
	}

	// /stop runs synchronously on the test goroutine and must return promptly.
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/stop"})

	// The turn must unwind: turnActive clears.
	deadline := time.Now().Add(2 * time.Second)
	for {
		b.mu.Lock()
		active := b.turnActive
		b.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turnActive never cleared: /stop did not cancel the in-flight turn")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// A "stopped" reply must have been sent.
	found := false
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "stopped") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no 'stopped' reply sent; got %v", fc.sentTexts())
	}
}
