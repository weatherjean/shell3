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

// TestPostCompletion_PlainPostAdvancesAnchor pins the background post shape:
// 🔔 posts are plain messages (never Telegram replies — a quote header on
// every completion is noise in the one conversation) and still advance the
// anchor via recordSent.
func TestPostCompletion_PlainPostAdvancesAnchor(t *testing.T) {
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
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "build done")
	})
	for _, m := range fc.sentReplies() {
		if strings.Contains(m.text, "build done") {
			t.Fatalf("a completion post must be a plain message, not a reply (got reply to %q)", m.replyTo)
		}
	}
	// The sent message still advanced the conversation anchor.
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

// TestStartFreshTurn_PostsReplyAsMail pins the catch-all mail: the note
// queues into the main conversation (created on demand) and the turn's reply
// posts to the user as ✉️ agent mail — one channel, no separate tool.
func TestStartFreshTurn_PostsReplyAsMail(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "fresh turn reply")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn("cron job \"nightly\" finished. result: all clear")

	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.main != nil && !b.turnActive && !b.main.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ fresh turn reply")
	})
}

// TestWakeTurn_MainSessionPostsMail pins that the conversation's wake turn
// delivers its reply as ✉️ agent mail once the queued mail drains.
func TestWakeTurn_MainSessionPostsMail(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "CRON_OK")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("anything")
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ CRON_OK")
	})
}

// TestWakeTurn_NoReplySentinelStaysSilent pins the silence path: a wake turn
// whose reply is NO_REPLY (however punctuated) posts nothing.
func TestWakeTurn_NoReplySentinelStaysSilent(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "NO_REPLY.")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("routine tick, nothing to say")
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("NO_REPLY wake turn must post nothing, got %v", texts)
	}
}

// Agent mail is ALWAYS silent (no ping, /quiet or not — mail is not a page)
// and always a plain message, never a Telegram reply.
func TestWakeTurn_MailAlwaysSilentAndPlain(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hushed news")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

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

// An agent mail identical to the previous one is dropped host-side — a
// narration-looping model must not fill the chat with copies. Changed text
// still posts.
func TestWakeTurn_IdenticalMailDroppedOnce(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "same old news")
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("tick one")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ same old news")
	})
	before := len(fc.sentTexts())

	sess.NotifyText("tick two") // fake replies the identical text again
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	if got := len(fc.sentTexts()); got != before {
		t.Fatalf("identical repeat mail must be dropped, extra posts: %v", fc.sentTexts()[before:])
	}
}

// Corrupt output — raw tool-call template markup — never posts as an update
// (report/wake turns): runWakeTurn's mail routing drops it entirely rather
// than posting a notice (a notice would itself be an unsolicited chat
// message from a silent background tick).
//
// The USER-turn and posted-queued-turn side of this guard (the notice posts,
// the raw markup never does) is covered end-to-end in mail_test.go by
// TestRunUserTurn_ToolMarkupReplacedWithNotice and
// TestRunPostedQueuedTurn_ToolMarkupReplacedWithNotice, which drive the real
// runUserTurn/runPostedQueuedTurn call sites in bot.go instead of
// re-implementing the guard in the test body.
func TestToolMarkupNeverReachesChat(t *testing.T) {
	corrupt := "]<]minimax[>[<tool_call>\nbash: git show 9cc4ffc\n</tool_call>"
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, corrupt)
	b := newBot(t, fc, rt)
	b.mu.Lock()
	b.main = sess
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.NotifyText("tick")
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !sess.HasQueuedInput()
	})
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("corrupt report reply must post nothing, got %v", texts)
	}
}
