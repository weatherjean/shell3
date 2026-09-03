//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestPostCompletion_CronPrefix(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "nightly", OwnerID: "", Text: "disk is 95% full"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⏰ nightly: disk is 95% full")
	})
}

func TestPostCompletion_BellPrefix(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "", OwnerID: "", Text: "the fetch finished"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "🔔 the fetch finished")
	})
}

func TestPostCompletion_ToolJobSuccessSingleMarker(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "sync", Text: "3 rows updated"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "3 rows updated")
	})
	got := strings.Join(fc.sentTexts(), "\n")
	if want := "⏰ sync: 3 rows updated"; !strings.Contains(got, want) {
		t.Fatalf("rendered = %q, want exactly %q", got, want)
	}
	if n := strings.Count(got, "⏰"); n != 1 {
		t.Fatalf("rendered = %q, want exactly one ⏰ marker, got %d", got, n)
	}
	if n := strings.Count(got, "sync"); n != 1 {
		t.Fatalf("rendered = %q, want the job name exactly once, got %d", got, n)
	}
}

func TestPostCompletion_ToolJobFailureSingleMarker(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{Text: "⚠️ sync failed: notion 502"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "notion 502")
	})
	got := strings.Join(fc.sentTexts(), "\n")
	if want := "⚠️ sync failed: notion 502"; !strings.Contains(got, want) {
		t.Fatalf("rendered = %q, want exactly %q", got, want)
	}
	if n := strings.Count(got, "⏰"); n != 0 {
		t.Fatalf("rendered = %q, want NO ⏰ marker on a failure post, got %d", got, n)
	}
	if n := strings.Count(got, "⚠️"); n != 1 {
		t.Fatalf("rendered = %q, want exactly one ⚠️ marker, got %d", got, n)
	}
	if n := strings.Count(got, "sync"); n != 1 {
		t.Fatalf("rendered = %q, want the job name exactly once, got %d", got, n)
	}
}

func TestPostCompletion_AgentCronFailureUnchanged(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "weekly", Text: "⚠️ weekly failed: exit 2"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "exit 2")
	})
	got := strings.Join(fc.sentTexts(), "\n")
	if want := "⏰ weekly: ⚠️ weekly failed: exit 2"; !strings.Contains(got, want) {
		t.Fatalf("rendered = %q, want the pre-existing (untouched) double-marker shape %q", got, want)
	}
}

func TestPostCompletion_PlainPostAdvancesAnchor(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()
	tconv(b).setAnchor("41")

	b.PostCompletion(shell3.CompletionPost{OwnerID: sess.ID(), Text: "build done"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "build done")
	})
	for _, m := range fc.sentReplies() {
		if strings.Contains(m.text, "build done") {
			t.Fatalf("a completion post must be a plain message, not a reply (got reply to %q)", m.replyTo)
		}
	}
	c.mu.Lock()
	anchor := c.mainAnchor
	c.mu.Unlock()
	if anchor != fc.lastSentID() {
		t.Fatalf("anchor = %q, want the sent post id %q", anchor, fc.lastSentID())
	}
}

func TestWakeOwner_MainAndForeign(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	c := tconv(b)
	c.mu.Lock()
	c.main = sess
	c.mu.Unlock()

	if !b.WakeOwner(shell3.Mail{OwnerID: sess.ID(), Note: "note for the agent"}) {
		t.Fatal("the main conversation must accept the wake")
	}
	if !sess.HasQueuedInput() {
		t.Fatal("wake note not queued on the session")
	}
	if b.WakeOwner(shell3.Mail{OwnerID: "no-such-session", Note: "n"}) {
		t.Fatal("unknown owner must return false")
	}
	cron, err := rt.Session(shell3.SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if b.WakeOwner(shell3.Mail{OwnerID: cron.ID(), Note: "n"}) {
		t.Fatal("cron parent must return false")
	}
}

func TestStartFreshTurn_PostsReplyAsMail(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "fresh turn reply")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn(shell3.Mail{Note: "cron job \"nightly\" finished. result: all clear"})

	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return tconv(b).main != nil && !tconv(b).turnActive && !tconv(b).main.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ fresh turn reply")
	})
}

func TestWakeTurn_MainSessionPostsMail(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "CRON_OK")
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
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return !tconv(b).turnActive && !sess.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ CRON_OK")
	})
}

func TestWakeTurn_NoReplySentinelStaysSilent(t *testing.T) {
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

	sess.NotifyText("routine tick, nothing to say")
	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return !tconv(b).turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("NO_REPLY wake turn must post nothing, got %v", texts)
	}
}

func TestWakeTurn_MailAlwaysSilentAndPlain(t *testing.T) {
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

	sess.NotifyText("bg done")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ hushed news")
	})
	if !fc.lastSilent() {
		t.Error("wake-turn mail must always be silent")
	}
	for _, m := range fc.sentReplies() {
		if strings.Contains(m.text, "hushed news") {
			t.Fatalf("agent mail must be a plain message, not a reply (got reply to %q)", m.replyTo)
		}
	}
}

func TestPostCompletion_DefaultRings(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "nightly", Text: "all well"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⏰ nightly: all well")
	})
	if fc.lastSilent() {
		t.Error("default cron post should ring")
	}
}

func TestWakeTurn_IdenticalMailDroppedOnce(t *testing.T) {
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
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ same old news")
	})
	before := len(fc.sentTexts())

	sess.NotifyText("tick two")
	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return !tconv(b).turnActive && !sess.HasQueuedInput()
	})
	if got := len(fc.sentTexts()); got != before {
		t.Fatalf("identical repeat mail must be dropped, extra posts: %v", fc.sentTexts()[before:])
	}
}

func TestToolMarkupNeverReachesChat(t *testing.T) {
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
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return !tconv(b).turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("corrupt report reply must post nothing, got %v", texts)
	}
}
