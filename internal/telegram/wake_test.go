//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConsumeWakes_PushesResult(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "woke up and ran")
	b := NewBot(fc, rt, sess, 42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	// Interject on an idle session queues input and emits a Wake.
	sess.Interject("scheduled job result")

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "woke up and ran")
	})
	_ = time.Now
}
