//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Rooms are independent conversations, so a slow turn in one must not hold up
// another. This is the whole point of a slot per room.
func TestRoomsRunTurnsConcurrently(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Hold both rooms' turns open by taking their slots directly: the fake
	// model answers instantly, so a real turn would finish before the second
	// one started and prove nothing.
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

// The global cap is what stops N rooms fanning out N concurrent agents.
// Crucially, a message queued BECAUSE of the cap has no waker of its own —
// only another room finishing frees it, which is what startNextWorkAll does.
func TestCapQueuesAndDrains(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.SetMaxConcurrentTurns(1)
	ctx := context.Background()

	// Room A holds the only slot.
	a := b.conv(-100)
	a.mu.Lock()
	_, cancelA, ok := a.takeSlotLocked(ctx)
	a.mu.Unlock()
	if !ok {
		t.Fatal("the first room must get the slot")
	}

	// Room B's message cannot run — it must queue, not be dropped.
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

	// Freeing A's slot is B's only possible waker.
	a.releaseSlot(cancelA)
	b.startNextWorkAll(ctx, a)
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	if r, _ := fc.lastReply(); r.chatID != -200 {
		t.Fatalf("the drained turn answered chat %d, want -200", r.chatID)
	}
}

// /stop is scoped: it cancels the room it was typed in and leaves the others
// running. A shared cancel would make one room's stop button a kill switch
// for everyone.
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

// A reload swaps the Parts every room shares, so it is refused while ANY room
// is mid-turn rather than racing one.
func TestReloadRefusedWhileAnotherRoomIsBusy(t *testing.T) {
	b := newBot(t, newFakeClient(), mustRuntime(t))
	ctx := context.Background()

	busyRoom := b.conv(-100)
	busyRoom.mu.Lock()
	_, cancel, _ := busyRoom.takeSlotLocked(ctx)
	busyRoom.mu.Unlock()
	defer cancel()

	if b.beginReload() {
		t.Fatal("a reload must not start while a room is mid-turn")
	}
	busyRoom.releaseSlot(cancel)
	if !b.beginReload() {
		t.Fatal("with every room idle the reload must proceed")
	}
	// While the latch is held no room may start a turn: they all share one
	// Parts, and a turn against the outgoing generation is the race the latch
	// exists to prevent.
	other := b.conv(-200)
	other.mu.Lock()
	_, _, ok := other.takeSlotLocked(ctx)
	other.mu.Unlock()
	if ok {
		t.Fatal("a room started a turn during a reload")
	}
	b.endReload()
}

// A command carrying another bot's @suffix is not ours. With privacy mode off
// we are delivered it anyway, and routing on the bare verb would let another
// bot's users stop our turns.
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

// Ordinary group chatter must not leave a phantom room behind: the registry
// feeds the inbox, the dash and the status tool.
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

// /ask is the group entry point that works with Telegram privacy mode ON:
// a privacy-mode bot is never delivered a plain @mention, but "/ask@bot …"
// and replies to its own messages always arrive.
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
	// And the reply is now something the user can reply TO, which is the
	// trigger that continues the thread without another /ask.
	if !b.conv(-100).wasSent(r.msgID) {
		t.Fatal("the answer must be recorded as ours, or replying to it won't count as addressing the bot")
	}
}

// /ask with nothing after it explains itself rather than starting an empty turn.
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

// /help has to answer the two things an operator forgets: each chat is its
// own conversation, and why an @mention can look ignored in a group.
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

// The help text has to say the TRUE reason a description can be missing:
// Telegram serves it only to a bot that can see group info. Telling the user
// to convert the group instead sends them down an irreversible path for a
// problem a promotion fixes.
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
