//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

// newHeartbeatRuntime is newFakeRuntime with a heartbeat block configured, so
// the bot's HEARTBEAT_OK suppression is armed.
func newHeartbeatRuntime(t *testing.T, replyText string) (*shell3.Runtime, *shell3.Session) {
	rt, sess := newFakeRuntime(t, replyText)
	rt.SetHeartbeatForTest(&shell3.Heartbeat{Every: 30 * time.Minute, Checklist: "- x"})
	return rt, sess
}

// TestWakeTurn_HeartbeatOKSuppressed pins the quiet path: a heartbeat tick
// whose turn ends in HEARTBEAT_OK sends nothing to the chat.
func TestWakeTurn_HeartbeatOKSuppressed(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newHeartbeatRuntime(t, "HEARTBEAT_OK")
	b := NewBot(fc, rt, sess, 42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	before := sess.MessageCount()
	sess.Interject("[heartbeat] check things")
	// Wait for the wake turn to land in the transcript, then assert silence.
	waitFor(t, func() bool { return sess.MessageCount() > before })
	time.Sleep(50 * time.Millisecond) // give a wrong send a chance to surface
	if texts := fc.sentTexts(); len(texts) != 0 {
		t.Fatalf("HEARTBEAT_OK turn must send nothing, sent %q", texts)
	}
}

// TestWakeTurn_HeartbeatAlertStripped pins the alert path: the token is
// stripped but the alert text is delivered.
func TestWakeTurn_HeartbeatAlertStripped(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newHeartbeatRuntime(t, "disk is 95% full\nHEARTBEAT_OK")
	b := NewBot(fc, rt, sess, 42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.Interject("[heartbeat] check things")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "disk is 95% full")
	})
	if all := strings.Join(fc.sentTexts(), "\n"); strings.Contains(all, "HEARTBEAT_OK") {
		t.Fatalf("token must be stripped from the delivered alert, got %q", all)
	}
}

// TestWakeTurn_NoHeartbeatNoStrip pins that suppression is armed only by a
// configured heartbeat: without one, a literal HEARTBEAT_OK reply flows out.
func TestWakeTurn_NoHeartbeatNoStrip(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "HEARTBEAT_OK")
	b := NewBot(fc, rt, sess, 42)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	sess.Interject("anything")
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "HEARTBEAT_OK")
	})
}

// TestBotBusy pins the heartbeat skip signal: mid-turn the bot reports busy.
func TestBotBusy(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "hi")
	b := NewBot(fc, rt, sess, 42)
	if b.Busy() {
		t.Fatal("fresh bot must not be busy")
	}
	b.mu.Lock()
	b.turnActive = true
	b.mu.Unlock()
	if !b.Busy() {
		t.Fatal("bot with an active turn must be busy")
	}
}
