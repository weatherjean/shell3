package shell3

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// gateClient blocks mid-stream until release is closed, then finishes the turn
// with a plain text response and NO tool calls (the turn's final round). This
// lets a test interject while the turn is in flight (past the top-of-turn inbox
// drain) and then let the turn end with the item still queued.
type gateClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *gateClient) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamEvent)) error {
	select {
	case <-c.started:
	default:
		close(c.started)
	}
	select {
	case <-c.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	emit(llm.StreamEvent{TextDelta: "done"})
	return nil
}

// TestEndOfTurn_QueuedInterjectEmitsWake proves Part A: a turn that ends with a
// non-empty inbox (steering arrived during the final round) emits a Wake so the
// host can run a follow-up RunQueued turn.
func TestEndOfTurn_QueuedInterjectEmitsWake(t *testing.T) {
	gc := &gateClient{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: gc, ModeLabel: "code"}
	})
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	ch := s.Send(context.Background(), "go")
	// Wait until the stream is in flight (top-of-turn inbox drain already ran).
	select {
	case <-gc.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream never started")
	}
	// Steer mid-turn: this queues but is NOT drained (final round, no more rounds).
	s.Interject("wait, also do X")
	// Let the turn finish with text + no tool calls.
	close(gc.release)
	for range ch {
	}

	if !s.HasQueuedInput() {
		t.Fatal("interjected item should still be queued after the turn ended")
	}
	// The session is now idle with a non-empty inbox → expect a Wake.
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Wake && ev.Session == s.ID() {
				return
			}
		case <-deadline:
			t.Fatal("end-of-turn with queued inbox did not emit a Wake")
		}
	}
}

// TestEndOfTurn_EmptyInboxNoWake is the negative case: a normal turn that ends
// with an empty inbox emits NO Wake.
func TestEndOfTurn_EmptyInboxNoWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Send(context.Background(), "go") {
	}
	select {
	case ev := <-rt.Events():
		t.Fatalf("normal completion must not emit a Wake, got %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}
}
