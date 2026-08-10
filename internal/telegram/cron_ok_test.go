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

// TestPostCompletion_ThreadsIntoConversation pins the threaded post: when the
// conversation exists and has an anchor, the 🔔 post lands as a reply onto it
// and advances the anchor.
func TestPostCompletion_ThreadsIntoConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused") // real store → stable session ids
	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()
	b.setAnchor("41")

	b.PostCompletion(shell3.CompletionPost{OwnerID: sess.ID(), Text: "build done"})

	waitFor(t, func() bool {
		for _, m := range fc.sentReplies() {
			if m.replyTo == "41" && strings.Contains(m.text, "build done") {
				return true
			}
		}
		return false
	})
	// The sent message advanced the conversation anchor.
	b.mu.Lock()
	anchor := b.mainAnchor
	b.mu.Unlock()
	if anchor != fc.lastSentID() {
		t.Fatalf("anchor = %q, want the sent post id %q", anchor, fc.lastSentID())
	}
}

// TestWakeOwner_MainAndForeign pins WakeOwner's contract: the current main
// conversation gets the note queued (true); anything else — an unknown id,
// the cron parent — returns false, sending the router to StartFreshTurn.
func TestWakeOwner_MainAndForeign(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "unused")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

	if !b.WakeOwner(sess.ID(), "note for the agent") {
		t.Fatal("the main conversation must accept the wake")
	}
	if !sess.HasQueuedInput() {
		t.Fatal("wake note not queued on the session")
	}
	if b.WakeOwner("no-such-session", "n") {
		t.Fatal("unknown owner must return false")
	}
	// The cron parent never takes wakes even when adopted.
	cron, err := rt.Session(shell3.SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	b.AdoptSession(cron)
	if b.WakeOwner(cron.ID(), "n") {
		t.Fatal("cron parent must return false")
	}
}

// TestStartFreshTurn_RunsQuietly pins the catch-all mail: the note queues
// into the main conversation (created on demand) and runs a QUIET turn — its
// reply posts nowhere (mail_user is the only way out of such a turn).
func TestStartFreshTurn_RunsQuietly(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "fresh turn reply")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn("cron job \"nightly\" finished. result: all clear")

	// The turn runs on the (freshly created) main conversation with nothing posted.
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.main != nil && !b.turnActive && !b.main.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("a fresh mail turn must post nothing, got %v", texts)
	}
}

// TestWakeTurn_MainSessionRunsQuietly pins that the conversation's wake turn
// runs quietly: the queued mail drains, nothing posts.
func TestWakeTurn_MainSessionRunsQuietly(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "CRON_OK")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
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
