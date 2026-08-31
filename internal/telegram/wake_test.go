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
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("scheduled job result")

	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return !tconv(b).turnActive && !sess.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ woke up and ran")
	})
}
