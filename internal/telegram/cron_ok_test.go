//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// TestPostCompletion_CronPrefix pins the ⏰ post path: a cron-originated
// completion posts "⏰ <job>: <text>", standalone (no thread anchor).
func TestPostCompletion_CronPrefix(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "nightly", OwnerID: "", Text: "disk is 95% full"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⏰ nightly: disk is 95% full")
	})
}

// TestPostCompletion_BellPrefix pins the non-cron post: "🔔 <text>".
func TestPostCompletion_BellPrefix(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))

	b.PostCompletion(shell3.CompletionPost{CronJob: "", OwnerID: "", Text: "the fetch finished"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "🔔 the fetch finished")
	})
}

// TestPostCompletion_ThreadsIntoLiveOwner pins the threaded post: when ownerID
// names a live session with a thread anchor, the 🔔 post is a reply into that
// thread and advances the anchor (a user reply to it continues the session).
func TestPostCompletion_ThreadsIntoLiveOwner(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused") // real store → stable session ids
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b := newBot(t, fc, rt)
	b.track(sess, "41") // live + anchored at message 41

	b.PostCompletion(shell3.CompletionPost{OwnerID: sess.ID(), Text: "build done"})

	waitFor(t, func() bool {
		for _, m := range fc.sentReplies() {
			if m.replyTo == "41" && strings.Contains(m.text, "build done") {
				return true
			}
		}
		return false
	})
	// The sent message advanced the thread anchor.
	if id, ok := b.threads.Lookup(fc.lastSentID()); !ok || id != sess.ID() {
		t.Fatalf("sent post not recorded as thread anchor (id=%v ok=%v)", id, ok)
	}
}

// TestWakeOwner_LiveAndGone pins WakeOwner's contract: a live session gets the
// note queued (true); an unknown or pinned session returns false.
func TestWakeOwner_LiveAndGone(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.live[sess.ID()] = sess
	b.mu.Unlock()

	if !b.WakeOwner(sess.ID(), "note for the agent") {
		t.Fatal("live session must accept the wake")
	}
	if !sess.HasQueuedInput() {
		t.Fatal("wake note not queued on the session")
	}
	if b.WakeOwner("no-such-session", "n") {
		t.Fatal("unknown owner must return false")
	}
	// Pinned (cron parent) sessions never take wakes.
	b.AdoptSession(sess)
	if b.WakeOwner(sess.ID(), "n") {
		t.Fatal("pinned session must return false")
	}
}

// TestStartFreshTurn_RunsAndPostsThread pins the ownerless wake: a fresh
// main-agent session runs over the note and its reply posts as a new
// replyable thread.
func TestStartFreshTurn_RunsAndPostsThread(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "fresh turn reply")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn("cron job \"nightly\" finished. result: all clear")

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "fresh turn reply")
	})
	// The reply is recorded as a thread anchor so the user can reply to it.
	waitFor(t, func() bool {
		_, ok := b.threads.Lookup(fc.lastSentID())
		return ok
	})
}

// TestWakeTurn_OrdinarySessionPostsVerbatim pins that an ordinary threaded
// session's wake turn posts its narration verbatim — no sentinel stripping of
// any kind applies to normal replies.
func TestWakeTurn_OrdinarySessionPostsVerbatim(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "CRON_OK")
	b := newBot(t, fc, rt)
	// Live but not adopted → treated as an ordinary threaded session.
	b.mu.Lock()
	b.live[sess.ID()] = sess
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.Interject("anything")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "CRON_OK")
	})
}
