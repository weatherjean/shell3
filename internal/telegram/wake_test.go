//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestConsumeWakes_PostsReplyAsMail(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "woke up and ran")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess // a wake is honored only for the main conversation
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	// NotifyText on an idle session queues input and emits a Wake; the mail
	// turn's reply posts as ✉️ agent mail once the queued input drains.
	sess.NotifyText("scheduled job result")

	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ woke up and ran")
	})
}
