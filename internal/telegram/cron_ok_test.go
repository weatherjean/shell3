//go:build unix

package telegram

import (
	"context"
	"path/filepath"
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

// TestStartFreshTurn_RunsQuietly pins the ownerless mail: a fresh main-agent
// session runs over the note QUIETLY — its reply posts nowhere (mail_user is
// the only way out of such a turn), and the session goes back to idle.
func TestStartFreshTurn_RunsQuietly(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "fresh turn reply")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn("cron job \"nightly\" finished. result: all clear")

	// The turn runs and ends with nothing posted.
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && len(b.wakeQueue) == 0
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("a fresh mail turn must post nothing, got %v", texts)
	}
}

// TestWakeTurn_OrdinarySessionRunsQuietly pins that an ordinary threaded
// session's wake turn runs quietly: the queued mail drains, nothing posts.
func TestWakeTurn_OrdinarySessionRunsQuietly(t *testing.T) {
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
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("a wake (mail) turn must post nothing, got %v", texts)
	}
}

// setQuiet flips the bot's /quiet toggle through a real store.
func setQuiet(t *testing.T, b *Bot, on bool) *QuietStore {
	t.Helper()
	qs := &QuietStore{Path: filepath.Join(t.TempDir(), "quiet_mode.json")}
	b.SetQuiet(qs)
	if err := qs.Set(on); err != nil {
		t.Fatal(err)
	}
	return qs
}

// Quiet on: ⏰ and 🔔 posts arrive silently; ⚠️ failure posts always ring.
func TestPostCompletion_QuietSilencesButFailuresRing(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "unused"))
	setQuiet(t, b, true)

	b.PostCompletion(shell3.CompletionPost{CronJob: "nightly", Text: "all well"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⏰ nightly: all well")
	})
	if !fc.lastSilent() {
		t.Error("quiet cron post should be silent")
	}

	b.PostCompletion(shell3.CompletionPost{Text: "the fetch finished"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "🔔 the fetch finished")
	})
	if !fc.lastSilent() {
		t.Error("quiet 🔔 post should be silent")
	}

	b.PostCompletion(shell3.CompletionPost{Text: "⚠️ nightly failed: exit 1"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⚠️ nightly failed")
	})
	if fc.lastSilent() {
		t.Error("⚠️ failure post must ring even under quiet")
	}

	// A cron-origin failure takes the ⏰ prefix branch but must STILL ring:
	// the failure check reads the raw text before the prefix switch rewrites it.
	b.PostCompletion(shell3.CompletionPost{CronJob: "weekly", Text: "⚠️ weekly failed: exit 2"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "⚠️ weekly failed")
	})
	if fc.lastSilent() {
		t.Error("cron-origin ⚠️ failure must ring even under quiet")
	}
}

// Quiet off (the default): completion posts ring.
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
