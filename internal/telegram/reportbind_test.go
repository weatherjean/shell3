//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

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
