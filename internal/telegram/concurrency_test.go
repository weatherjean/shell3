//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func (c *conversation) busy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turnActive
}

func TestRoomsRunTurnsConcurrently(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a, second := b.conv(-100), b.conv(-200)
	a.mu.Lock()
	_, cancelA, okA := a.takeSlotLocked(ctx)
	a.mu.Unlock()
	second.mu.Lock()
	_, cancelB, okB := second.takeSlotLocked(ctx)
	second.mu.Unlock()
	if !okA || !okB {
		t.Fatal("two rooms must be able to hold turn slots at the same time")
	}
	defer cancelA()
	defer cancelB()
	if !a.busy() || !second.busy() {
		t.Fatal("both rooms should report busy")
	}
}

func TestCapQueuesAndDrains(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.SetMaxConcurrentTurns(1)
	ctx := context.Background()

	a := b.conv(-100)
	a.mu.Lock()
	_, cancelA, ok := a.takeSlotLocked(ctx)
	a.mu.Unlock()
	if !ok {
		t.Fatal("the first room must get the slot")
	}

	b.handleMsg(ctx, Msg{ChatID: -200, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "@mybot hello"})
	waitFor(t, func() bool {
		c := b.peekConv(-200)
		if c == nil {
			return false
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.mailQueue) == 1
	})

	a.releaseSlot(cancelA)
	b.startNextWorkAll(ctx, a)
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	if r, _ := fc.lastReply(); r.chatID != -200 {
		t.Fatalf("the drained turn answered chat %d, want -200", r.chatID)
	}
}

func TestStopScopesToItsRoom(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	a, other := b.conv(-100), b.conv(-200)
	a.mu.Lock()
	_, cancelA, _ := a.takeSlotLocked(ctx)
	a.mu.Unlock()
	other.mu.Lock()
	turnCtxB, cancelB, _ := other.takeSlotLocked(ctx)
	other.mu.Unlock()
	defer cancelA()
	defer cancelB()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "/stop@mybot"})

	select {
	case <-turnCtxB.Done():
		t.Fatal("/stop in one room cancelled another room's turn")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCommandForAnotherBotIsIgnored(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	c := b.conv(-100)
	c.mu.Lock()
	_, cancel, _ := c.takeSlotLocked(ctx)
	turnCtx := context.Background()
	c.mu.Unlock()
	defer cancel()
	_ = turnCtx

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "/stop@otherbot"})
	if got := len(fc.sentTexts()) + len(fc.htmlTexts()); got != 0 {
		t.Fatalf("a command for another bot produced %d posts, want silence", got)
	}
	if !c.busy() {
		t.Fatal("a command for another bot stopped our turn")
	}
}

func TestUnaddressedChatterCreatesNoRoom(t *testing.T) {
	b := newBot(t, newFakeClient(), mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.handleMsg(context.Background(), Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "lunch?"})
	if b.peekConv(-100) != nil {
		t.Fatal("chatter that never reaches a turn must leave no room behind")
	}
}

func TestAskOpensAConversationInAGroup(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1",
		Text: "/ask@mybot what is the deploy status"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })

	r, _ := fc.lastReply()
	if r.chatID != -100 {
		t.Fatalf("/ask answered chat %d, want the room it was typed in", r.chatID)
	}
	if b.conv(-100).session() == nil {
		t.Fatal("/ask must open that room's conversation")
	}
	if !b.conv(-100).wasSent(r.msgID) {
		t.Fatal("the answer must be recorded as ours, or replying to it won't count as addressing the bot")
	}
}

func TestAskWithoutTextExplainsItself(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.handleMsg(context.Background(), Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "/ask"})
	all := strings.Join(append(fc.sentTexts(), fc.htmlTexts()...), "\n")
	if !strings.Contains(all, "usage") {
		t.Fatalf("bare /ask said %q, want usage", all)
	}
}

func TestHelpExplainsRoomsAndTheMentionCaveat(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.handleMsg(context.Background(), Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "/help@mybot"})
	all := strings.Join(append(fc.sentTexts(), fc.htmlTexts()...), "\n")
	for _, want := range []string{"privacy", "/ask", "Separate conversations", "its own memory", "/new"} {
		if !strings.Contains(strings.ToLower(all), strings.ToLower(want)) {
			t.Errorf("/help never mentions %q:\n%s", want, all)
		}
	}
}

func TestHelpNamesTheAdminRequirementForDescriptions(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.handleMsg(context.Background(), Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "/help@mybot"})
	all := strings.ToLower(strings.Join(append(fc.sentTexts(), fc.htmlTexts()...), "\n"))
	if !strings.Contains(all, "admin") || !strings.Contains(all, "description") {
		t.Fatalf("help must explain that a description needs admin rights:\n%s", all)
	}
	if strings.Contains(all, "supergroup") {
		t.Fatal("help must not send the user to convert their group — that is irreversible and not the fix")
	}
}
