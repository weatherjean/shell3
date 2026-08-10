//go:build unix

package telegram

import (
	"context"
	"testing"
)

func TestConsumeWakes_RunsQuietTurn(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "woke up and ran")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess // a wake is honored only for the main conversation
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	// Interject on an idle session queues input and emits a Wake; the mail
	// turn runs quietly — the queued input drains, nothing posts.
	sess.Interject("scheduled job result")

	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("a wake (mail) turn must post nothing, got %v", texts)
	}
}
