package agentsetup

import (
	"context"
	"sync"
	"testing"
	"time"
)

func droppedEvents(d *eventDispatcher) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dropped
}

func TestEventDispatcherDeliversAsynchronously(t *testing.T) {
	var mu sync.Mutex
	var got []string
	done := make(chan struct{})
	d := newEventDispatcher(2, func(_ context.Context, agent, kind string, _ []byte) error {
		mu.Lock()
		got = append(got, agent+"/"+kind)
		if len(got) == 1 {
			close(done)
		}
		mu.Unlock()
		return nil
	}, nil)
	defer d.Close()

	d.Post("main", "turn_done", []byte(`{}`))
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("event was never delivered")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || got[0] != "main/turn_done" {
		t.Fatalf("got = %v", got)
	}
}

func TestEventDispatcherDropsOldestWhenFull(t *testing.T) {
	release := make(chan struct{})
	var mu sync.Mutex
	var got []string
	d := newEventDispatcher(2, func(_ context.Context, _, kind string, _ []byte) error {
		if kind == "first" {
			<-release
		}
		mu.Lock()
		got = append(got, kind)
		mu.Unlock()
		return nil
	}, nil)
	defer d.Close()

	d.Post("main", "first", nil) // claimed by the worker, blocks on release
	for i := 0; i < 100; i++ {
		if droppedEvents(d) == 0 && len(d.q) == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	d.Post("main", "a", nil)
	d.Post("main", "b", nil)
	d.Post("main", "c", nil)
	close(release)

	deadline := time.After(2 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 3 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("only %d events delivered", n)
		case <-time.After(time.Millisecond):
		}
	}
	if droppedEvents(d) == 0 {
		t.Error("dropped events = 0, want the overflow counted")
	}
	mu.Lock()
	defer mu.Unlock()
	for _, k := range got {
		if k == "a" {
			t.Errorf("got = %v, want the OLDEST queued event dropped", got)
		}
	}
}

func TestEventDispatcherPostAfterCloseIsNoop(t *testing.T) {
	d := newEventDispatcher(1, func(context.Context, string, string, []byte) error { return nil }, nil)
	d.Close()
	d.Close()
	d.Post("main", "turn_done", nil)
}

func TestEventDispatcherCloseDrainsQueue(t *testing.T) {
	const queued = 50
	release := make(chan struct{})
	var mu sync.Mutex
	delivered := 0
	d := newEventDispatcher(queued+1, func(_ context.Context, _, kind string, _ []byte) error {
		if kind == "first" {
			<-release
		}
		mu.Lock()
		delivered++
		mu.Unlock()
		return nil
	}, nil)

	d.Post("main", "first", nil) // claimed by the worker, which now blocks
	for i := 0; i < 200 && len(d.q) != 0; i++ {
		time.Sleep(time.Millisecond)
	}
	for i := 0; i < queued; i++ {
		d.Post("main", "queued", nil)
	}
	close(release)
	d.Close()

	mu.Lock()
	defer mu.Unlock()
	if delivered != queued+1 {
		t.Fatalf("delivered %d of %d — Close dropped queued events", delivered, queued+1)
	}
}

func TestEventDispatcherCloseIsBounded(t *testing.T) {
	d := newEventDispatcher(8, func(ctx context.Context, _, _ string, _ []byte) error {
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	for i := 0; i < 8; i++ {
		d.Post("main", "stuck", nil)
	}
	start := time.Now()
	d.Close()
	if el := time.Since(start); el > eventHookTimeout+eventDrainBudget+2*time.Second {
		t.Fatalf("Close took %s — draining is not bounded", el)
	}
}
