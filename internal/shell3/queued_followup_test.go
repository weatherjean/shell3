package shell3

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

// gateClient blocks mid-stream until release is closed, then finishes the turn
// with a plain text response and no tool calls. This lets a test interject after
// the top-of-turn inbox drain and leave the item queued at the final boundary.
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

func TestEndOfTurn_QueuedInterjectRemainsForFollowUp(t *testing.T) {
	gc := &gateClient{started: make(chan struct{}), release: make(chan struct{})}
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: gc}
	})
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	ch := s.Send(context.Background(), "go")
	select {
	case <-gc.started:
	case <-time.After(2 * time.Second):
		t.Fatal("stream never started")
	}
	s.Interject("wait, also do X")
	close(gc.release)
	for range ch {
	}

	if !s.HasQueuedSteer() {
		t.Fatal("interjected item should still be queued after the turn ended")
	}
	for range s.RunQueued(context.Background()) {
	}
	if s.HasQueuedSteer() {
		t.Fatal("follow-up turn did not drain queued steering")
	}
}

func TestEndOfTurn_EmptyInboxStaysEmpty(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Send(context.Background(), "go") {
	}
	if s.HasQueuedInput() {
		t.Fatal("normal completion left queued input")
	}
}
