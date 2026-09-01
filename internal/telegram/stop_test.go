//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func TestStopCancelsInFlightTurn(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "do work"})

	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started: handleMsg did not launch the turn")
	}

	c := tconv(b)
	c.mu.Lock()
	inflight := c.turnActive
	c.mu.Unlock()
	if !inflight {
		t.Fatal("turnActive is false while a turn is in flight: handleMsg did not mark the turn active on its own goroutine")
	}

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		active := c.turnActive
		c.mu.Unlock()
		if !active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turnActive never cleared: /stop did not cancel the in-flight turn")
		}
		time.Sleep(5 * time.Millisecond)
	}

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
