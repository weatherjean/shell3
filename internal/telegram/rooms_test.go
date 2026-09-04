//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

func TestConvIsPerChat(t *testing.T) {
	b := newBot(t, newFakeClient(), mustRuntime(t))
	a := b.conv(111)
	if b.conv(111) != a {
		t.Fatal("the same chat must resolve to the same conversation")
	}
	if b.conv(222) == a {
		t.Fatal("a different chat must get its own conversation")
	}
	if got := b.conv(222).chatID; got != 222 {
		t.Fatalf("chatID = %d, want 222", got)
	}
	if b.conv(111).index == b.conv(222).index {
		t.Fatal("each room needs its own thread index, or a restart resumes the wrong session")
	}
}

func TestGroupGatesOnSenderAndTrigger(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 9, ID: "1", Text: "@mybot deploy"})
	if len(b.allConvs()) != 0 {
		t.Fatal("an unauthorized sender must not enrol a room")
	}

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "2", Text: "lunch?"})
	time.Sleep(20 * time.Millisecond)
	if got := len(fc.sentReplies()) + len(fc.htmlTexts()); got != 0 {
		t.Fatalf("unaddressed group chatter started a turn (%d posts)", got)
	}

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "3", Text: "@mybot deploy"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	if b.conv(-100).session() == nil {
		t.Fatal("an addressed group message must open that room's conversation")
	}
}

func TestGroupMessagesAllSkipsTriggerButKeepsSenderGate(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	b.SetAnswerAllGroupMessages(true)
	ctx := context.Background()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 9, ID: "1", Text: "not allowed"})
	if len(b.allConvs()) != 0 {
		t.Fatal("group_messages: all must not bypass sender authorization")
	}

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "2", Text: "plain message"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	if b.conv(-100).session() == nil {
		t.Fatal("an allowlisted plain group message must open the room in all mode")
	}
}

func TestTwoRoomsHoldSeparateSessions(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "@mybot one"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	b.handleMsg(ctx, Msg{ChatID: -200, ChatType: "supergroup", SenderID: 7, ID: "2", Text: "@mybot two"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 2 })

	a, bb := b.conv(-100).session(), b.conv(-200).session()
	if a == nil || bb == nil {
		t.Fatal("both rooms must have a session")
	}
	if a.ID() == bb.ID() {
		t.Fatal("two rooms sharing one session is the context bloat this feature exists to remove")
	}
	var chats []int64
	for _, r := range fc.sentReplies() {
		chats = append(chats, r.chatID)
	}
	if len(chats) < 2 || chats[0] != -100 || chats[1] != -200 {
		t.Fatalf("replies landed in %v, want each room answered in itself", chats)
	}
}

func TestRoomSessionsPersistPerSurface(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "@mybot one"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })

	want := b.conv(-100).session().ID()
	got, ok := b.conv(-100).index.Current()
	if !ok || got != want {
		t.Fatalf("room marker = %q,%v; want %q", got, ok, want)
	}
	if surface := b.conv(-100).index.surface; surface != "telegram:-100" {
		t.Fatalf("surface = %q, want telegram:-100", surface)
	}
}

func TestRoomForOwnerResolvesFromStoreAfterRestart(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SetCurrentSession(roomSurface("telegram", -200), "sess-b"); err != nil {
		t.Fatal(err)
	}
	idx := NewThreadIndex(func() *runs.Store { return st }, "telegram")
	b := NewBot(newFakeClient(), mustRuntime(t), 42, idx)

	c := b.roomForOwner("sess-b")
	if c == nil {
		t.Fatal("a recovered completion must resolve its room from the store, not fall back to the home chat")
	}
	if c.chatID != -200 {
		t.Fatalf("resolved chat %d, want -200", c.chatID)
	}
	if b.roomForOwner("nobody") != nil {
		t.Fatal("an unknown owner names no room")
	}
}

func TestInboxNamesRooms(t *testing.T) {
	b := newBot(t, newFakeClient(), mustRuntime(t))
	c := b.conv(-100)
	c.mu.Lock()
	c.mailQueue = append(c.mailQueue, inMail{text: "waiting here"})
	c.mu.Unlock()

	out := b.Inbox()
	if !strings.Contains(out, "-100") || !strings.Contains(out, "waiting here") {
		t.Fatalf("inbox = %q, want the room and its queued message", out)
	}
}

func TestRoomSurvivesSupergroupMigration(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, mustRuntime(t))
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "group", SenderID: 7, ID: "1", Text: "@mybot hi"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 1 })
	sessID := b.conv(-100).session().ID()

	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "group", MigratedTo: -1009999})

	if b.peekConv(-100) != nil {
		t.Fatal("the old chat id must no longer name a room")
	}
	moved := b.peekConv(-1009999)
	if moved == nil || moved.session() == nil {
		t.Fatal("the conversation must move to the new chat id")
	}
	if moved.session().ID() != sessID {
		t.Fatal("migration must carry the SAME session — a rename is not a new conversation")
	}
	if !moved.isGroup {
		t.Fatal("a supergroup is still a group: the trigger gate must stay armed")
	}
	got, ok := moved.index.Current()
	if !ok || got != sessID {
		t.Fatalf("marker under the new surface = %q,%v; want %q", got, ok, sessID)
	}
	if st := b.threads.currentStore(); st != nil {
		if id, ok := st.CurrentSession(roomSurface("telegram", -100)); ok && id != "" {
			t.Fatalf("old surface still marks session %q", id)
		}
	}
}

func TestMigrationOfAnUnknownRoomIsANoop(t *testing.T) {
	b := newBot(t, newFakeClient(), mustRuntime(t))
	b.handleMsg(context.Background(), Msg{ChatID: -100, ChatType: "group", MigratedTo: -1009999})
	if len(b.allConvs()) != 0 {
		t.Fatal("migrating a room nobody has used must create nothing")
	}
}
