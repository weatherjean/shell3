package shell3

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
)

func TestCancelSubagentsStopsInFlight(t *testing.T) {
	blk := NewBlockingLLM()
	rt := NewRuntimeForTestClient(t, blk)
	parent, err := rt.Session(SessionOpts{Name: "telegram", Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	id, err := parent.spawn(context.Background(), chat.SpawnRequest{Subagent: "explorer", Task: "do work"})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	// Wait for the subagent's turn to be in flight.
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("subagent never started")
	}
	// It is running.
	if subs := parent.Subagents(); len(subs) != 1 || subs[0].Status != "running" {
		t.Fatalf("want one running subagent, got %v", subs)
	}
	// Cancel and assert it unwinds to finished promptly.
	parent.CancelSubagents()
	deadline := time.Now().Add(2 * time.Second)
	for {
		subs := parent.Subagents()
		if len(subs) == 1 && subs[0].Status == "finished" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("subagent %s never finished after CancelSubagents: %v", id, subs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
