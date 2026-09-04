//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestWakeTurnMainSessionPostsPlainReply(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "host reminder acknowledged")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("anything")
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.turnActive && !sess.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "host reminder acknowledged")
	})
	if strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️") {
		t.Fatal("the mail marker is reserved for filesystem inbox notices")
	}
}

func TestWakeTurnNoReplySentinelStaysSilent(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "NO_REPLY.")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("routine host reminder, nothing to say")
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("NO_REPLY wake turn must post nothing, got %v", texts)
	}
}

func TestWakeTurnAlwaysSilentAndPlain(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hushed news")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("host reminder")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "hushed news")
	})
	if !fc.lastSilent() {
		t.Error("wake-turn reply must be silent")
	}
	for _, m := range fc.sentReplies() {
		if strings.Contains(m.text, "hushed news") {
			t.Fatalf("wake-turn reply must be a plain message, not a reply to %q", m.replyTo)
		}
	}
}

func TestWakeTurnIdenticalReplyDropped(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "same old news")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("tick one")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "same old news")
	})
	before := len(fc.sentTexts())

	sess.NotifyText("tick two")
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.turnActive && !sess.HasQueuedInput()
	})
	if got := len(fc.sentTexts()); got != before {
		t.Fatalf("identical repeat must be dropped, extra posts: %v", fc.sentTexts()[before:])
	}
}

func TestToolMarkupNeverReachesChatFromWake(t *testing.T) {
	corrupt := "]<]minimax[>[<tool_call>\nbash: git show 9cc4ffc\n</tool_call>"
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, corrupt)
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("tick")
	waitFor(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return !c.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("corrupt wake reply must post nothing, got %v", texts)
	}
}
