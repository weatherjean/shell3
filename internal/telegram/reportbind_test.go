//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// requiredMail is the shape a report:"always" completion arrives in.
func requiredMail(note string) shell3.Mail {
	return shell3.Mail{
		Note:     note,
		Required: true,
		Fallback: "bg8 finished:\ncamoufox import OK",
		Post: shell3.CompletionPost{
			JobID: "bg8", Text: "bg8 finished:\ncamoufox import OK",
		},
	}
}

// The bind's whole point: a report turn that answers NO_REPLY does not get to
// swallow a result the spawner said the user was waiting on — the job's own
// output posts in its place. This is the 2026-08-25 failure, pinned: a
// finished install sat unreported for nine minutes because the model judged
// its own start-of-job narration as "they already know".
func TestWakeTurn_NoReplyUnderRequiredPostsFallback(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "NO_REPLY")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn(requiredMail("TASK REPORT — bg8 (clean)"))

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "camoufox import OK")
	})
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "✉️") {
		t.Fatalf("the agent said nothing, so nothing should post as ✉️ mail: %q", got)
	}
}

// The same NO_REPLY under the default mode stays silent — the cost valve for
// routine ticks has to keep working.
func TestWakeTurn_NoReplyUnboundStaysSilent(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "NO_REPLY")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	m := requiredMail("TASK REPORT — bg8 (clean)")
	m.Required, m.Fallback, m.Post = false, "", shell3.CompletionPost{}
	b.StartFreshTurn(m)

	waitFor(t, func() bool {
		c := tconv(b)
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.main != nil && !c.turnActive && !c.main.HasQueuedInput()
	})
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "camoufox") {
		t.Fatalf("an unbound NO_REPLY must post nothing, got %q", got)
	}
}

// A bound turn that DOES answer discharges the bind: the user sees the
// agent's own ✉️ words and not a second raw copy of the same result.
func TestWakeTurn_ReplyUnderRequiredDischargesTheBind(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "camoufox is back — smoke test passed")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	b.StartFreshTurn(requiredMail("TASK REPORT — bg8 (clean)"))

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ camoufox is back")
	})
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "camoufox import OK") {
		t.Fatalf("the fallback must not post alongside the agent's own answer: %q", got)
	}
}

// A USER turn drains queued notices too, so it settles the binds it
// inherited: it answered the user, so the fallback must NOT also post. Without
// this the user would read the agent's reply and then a raw copy of the same
// result under it.
func TestUserTurn_ReplyDischargesTheBind(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "all done, camoufox works"))

	c := tconv(b)
	c.requireReport(shell3.CompletionPost{JobID: "bg8", Text: "bg8 finished:\ncamoufox import OK"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "anything?"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "all done, camoufox works")
	})
	waitIdle(t, b)
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "camoufox import OK") {
		t.Fatalf("the bind was discharged by the reply; the fallback must not post: %q", got)
	}
}

// The same turn answering NO_REPLY posts nothing of its own, so the bind is
// unmet and the job's result posts instead.
func TestUserTurn_NoReplyFlushesTheBind(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "NO_REPLY"))

	c := tconv(b)
	c.requireReport(shell3.CompletionPost{JobID: "bg8", Text: "bg8 finished:\ncamoufox import OK"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "anything?"})

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "camoufox import OK")
	})
}

// A reply that says its piece and then signs off with the sentinel posts the
// piece and NOT the sentinel. Observed live 2026-08-25: the literal word
// NO_REPLY was delivered to the user as part of an ✉️ update, because the
// whole-reply match let anything with content through untouched.
func TestWakeTurn_TrailingSentinelIsStrippedNotPosted(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "Background dry-run dispatched. Will land next turn.\n\nNO_REPLY")
	b := newBot(t, fc, rt)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)

	m := requiredMail("TASK REPORT — sub4 (clean)")
	m.Required, m.Fallback, m.Post = false, "", shell3.CompletionPost{}
	b.StartFreshTurn(m)

	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "Background dry-run dispatched")
	})
	if got := strings.Join(fc.sentTexts(), "\n"); strings.Contains(got, "NO_REPLY") {
		t.Fatalf("the sentinel reached the chat: %q", got)
	}
}
