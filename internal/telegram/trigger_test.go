//go:build unix

package telegram

import "testing"

// A DM needs no trigger: there is nobody else in the room.
func TestAddressedPrivateChatAlwaysCounts(t *testing.T) {
	c := &conversation{}
	c.setGroup("private")
	if !c.addressed(Msg{Text: "hello"}, "mybot") {
		t.Fatal("every private-chat message is addressed to the bot")
	}
}

// In a group only an @mention or a reply to the bot counts. The lookalike
// case is the one that matters: another bot named @mybottom in the same room
// must not be able to drive this one.
func TestAddressedGroupTrigger(t *testing.T) {
	c := &conversation{}
	c.setGroup("supergroup")
	c.rememberSent("42")

	cases := []struct {
		name string
		m    Msg
		want bool
	}{
		{"plain group chatter", Msg{Text: "lunch?"}, false},
		{"mention at the start", Msg{Text: "@MyBot deploy please"}, true},
		{"mention mid-sentence", Msg{Text: "hey @mybot look at this"}, true},
		{"mention at the very end", Msg{Text: "ask @mybot"}, true},
		{"reply to the bot", Msg{Text: "do it", ReplyToID: "42"}, true},
		{"reply to a human", Msg{Text: "do it", ReplyToID: "7"}, false},
		{"lookalike username", Msg{Text: "@mybottom hi"}, false},
		{"name without the @", Msg{Text: "mybot do it"}, false},
	}
	for _, tc := range cases {
		if got := c.addressed(tc.m, "mybot"); got != tc.want {
			t.Errorf("%s: addressed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// With no resolvable bot username a room degrades to reply-only rather than
// going deaf: @mentions cannot match, replies still do.
func TestAddressedWithoutUsername(t *testing.T) {
	c := &conversation{}
	c.setGroup("group")
	c.rememberSent("9")
	if c.addressed(Msg{Text: "@mybot hi"}, "") {
		t.Fatal("no username known: an @mention cannot be matched")
	}
	if !c.addressed(Msg{Text: "hi", ReplyToID: "9"}, "") {
		t.Fatal("a reply to the bot must still count")
	}
}

// The sent-id ring is bounded; an old id falling out of it costs one repeated
// @mention, never a wrong yes.
func TestSentIDRingIsBounded(t *testing.T) {
	c := &conversation{}
	for i := 0; i < sentIDsCap+50; i++ {
		c.rememberSent(string(rune('a' + i%26)))
	}
	c.mu.Lock()
	n := len(c.sentIDs)
	c.mu.Unlock()
	if n > sentIDsCap {
		t.Fatalf("sentIDs grew to %d, cap is %d", n, sentIDsCap)
	}
}

// A reply is decided from Telegram's own author field, not from what this
// process remembers sending. The remembered set is empty after a restart, so
// a group whose only trigger was "reply to me" used to go deaf until someone
// @mentioned it again — every restart, silently.
func TestAddressedReplyToBotSurvivesRestart(t *testing.T) {
	c := &conversation{}
	c.setGroup("supergroup")
	// Nothing remembered: exactly the state a fresh process is in.
	if !c.addressed(Msg{Text: "do it", ReplyToID: "42", ReplyToBot: true}, "mybot") {
		t.Fatal("a reply to the bot must count even when this process never sent that message")
	}
	if c.addressed(Msg{Text: "do it", ReplyToID: "42"}, "mybot") {
		t.Fatal("a reply to someone ELSE must not count")
	}
}
