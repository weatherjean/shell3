//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A bare (non-reply) message when previous conversations exist does not run
// immediately: the bot asks "start a new thread?" with New-thread/Cancel
// buttons and holds the message.
func TestBareMessageAsksBeforeRunning(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "fresh reply"))
	b.threads.Record("9", "some-old-session") // a previous conversation exists

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "10", Text: "yes please"})

	waitFor(t, func() bool { return len(fc.menusSnapshot()) == 1 })
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "fresh reply") {
		t.Fatalf("turn ran before the thread choice: %q", got)
	}
	// Tap "new thread": the held message runs as its own session.
	menu := fc.menusSnapshot()[0]
	b.handleCallback(context.Background(), Callback{ChatID: 42, ID: "cb1", Data: menu.options[0].Data})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "fresh reply")
	})
	// The ask message was edited to reflect the choice.
	if edits := fc.editTexts(); len(edits) == 0 || !strings.Contains(edits[len(edits)-1], "new thread") {
		t.Fatalf("ask message not edited after tap: %v", fc.editTexts())
	}
}

// Cancel drops the held message: nothing runs, the ask edits to say so.
func TestBareMessageCancelDropsIt(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "should never post"))
	b.threads.Record("9", "some-old-session")

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "10", Text: "oops"})
	waitFor(t, func() bool { return len(fc.menusSnapshot()) == 1 })
	menu := fc.menusSnapshot()[0]
	b.handleCallback(context.Background(), Callback{ChatID: 42, ID: "cb1", Data: menu.options[1].Data})

	waitFor(t, func() bool {
		edits := fc.editTexts()
		return len(edits) > 0 && strings.Contains(strings.ToLower(edits[len(edits)-1]), "cancel")
	})
	time.Sleep(50 * time.Millisecond)
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "should never post") {
		t.Fatalf("cancelled message still ran: %q", got)
	}
}

// No tap within the timeout: the message runs as a new thread — never dropped.
func TestBareMessageTimeoutRunsAsNew(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "late reply"))
	b.threadAskTimeout = 50 * time.Millisecond
	b.threads.Record("9", "some-old-session")

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "10", Text: "hello?"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "late reply")
	})
}

// The very first conversation (no thread history anywhere) runs immediately —
// there is nothing the user could have meant to continue.
func TestFirstMessageSkipsAsk(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "first reply"))

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "hi"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "first reply")
	})
	if len(fc.menusSnapshot()) != 0 {
		t.Fatalf("first-ever message should not ask: %v", fc.menusSnapshot())
	}
}

// A Telegram reply threads explicitly and never asks.
func TestReplyBypassesAsk(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "threaded reply")
	b := newBot(t, fc, rt)

	// Establish a conversation the normal way (first-ever message: no ask).
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "start"})
	waitFor(t, func() bool { return len(fc.sentTexts()) > 0 })

	before := len(fc.menusSnapshot())
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "2", ReplyToID: "1", Text: "continue"})
	waitFor(t, func() bool {
		return strings.Count(strings.Join(fc.sentTexts(), "\n"), "threaded reply") >= 1
	})
	if len(fc.menusSnapshot()) != before {
		t.Fatal("a reply should never trigger the thread ask")
	}
}

// Bare messages arriving while one is already held join the same batch — one
// ask, one decision, one new-thread turn over all of them.
func TestHeldBatchJoins(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "batched"))
	b.threads.Record("9", "some-old-session")

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "10", Text: "first part"})
	waitFor(t, func() bool { return len(fc.menusSnapshot()) == 1 })
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "11", Text: "second part"})
	if len(fc.menusSnapshot()) != 1 {
		t.Fatalf("second bare message posted a second ask: %v", fc.menusSnapshot())
	}
	menu := fc.menusSnapshot()[0]
	b.handleCallback(context.Background(), Callback{ChatID: 42, ID: "cb1", Data: menu.options[0].Data})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "batched")
	})
}
