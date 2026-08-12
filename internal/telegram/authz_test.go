//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// An existing single-DM config never names allow_from. It must keep working:
// in a direct chat the chat id IS the user id, so the owner is allowed by
// default and nobody else is.
func TestAllowlistDefaultsToChatOwner(t *testing.T) {
	a, err := newSenderAllowlist(42, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !a.allows(42) {
		t.Error("the chat owner must be allowed by default")
	}
	if a.allows(99) {
		t.Error("a stranger must not be allowed by default")
	}
}

// An explicit list replaces the default entirely — including the ability to
// authorize someone who is not the chat owner, which is the whole point in a
// group where chat id and user id are different numbers.
func TestAllowlistExplicitReplacesDefault(t *testing.T) {
	a, err := newSenderAllowlist(-1001234567890, []string{"42", " 77 "})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{42, 77} {
		if !a.allows(id) {
			t.Errorf("configured id %d must be allowed", id)
		}
	}
	if a.allows(-1001234567890) {
		t.Error("the group chat id is not a user and must not be allowed")
	}
}

// An unattributable message (a channel post, or a transport that cannot say
// who sent it) is denied: there is nobody to hold responsible for a turn that
// runs an unrestricted shell.
func TestAllowlistDeniesZeroSender(t *testing.T) {
	a, _ := newSenderAllowlist(42, []string{"42"})
	if a.allows(0) {
		t.Error("a zero sender must never be allowed")
	}
	var nilList *senderAllowlist
	if nilList.allows(42) {
		t.Error("a nil allowlist must deny everyone")
	}
}

// A malformed allow_from entry fails loudly at startup. Silently skipping it
// would narrow who can drive the bot without saying so — or, read the other
// way, leave an operator believing they granted access they did not.
func TestAllowlistRejectsNonNumeric(t *testing.T) {
	_, err := newSenderAllowlist(42, []string{"@someuser"})
	if err == nil || !strings.Contains(err.Error(), "not a numeric user id") {
		t.Fatalf("want a numeric-id error, got %v", err)
	}
}

// The gate must sit BEFORE the command branch. Telegram's privacy mode
// delivers /commands from every member of a group, so a check placed only on
// the conversation path would still let an unauthorized member /stop a running
// turn or /new the conversation away.
func TestUnauthorizedSenderCannotRunCommands(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	// /status from a stranger: no reply, no work.
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 99, ID: "1", Text: "/status"})
	if n := len(fc.sent); n != 0 {
		t.Fatalf("an unauthorized /status produced %d message(s): %v", n, fc.sent)
	}

	// And a plain message from the same stranger starts no turn.
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 99, ID: "2", Text: "hello"})
	if client.CallCount() != 0 {
		t.Fatalf("an unauthorized message started %d model call(s)", client.CallCount())
	}
}

// The authorized owner is unaffected by the new gate.
func TestAuthorizedSenderStillWorks(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "hi there"}}})
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hello"})
	if !waitForReply(t, fc, "hi there") {
		t.Fatal("the authorized owner got no reply")
	}
}

// SetAllowFrom widens the allowlist to a second person without the chat id
// changing — the group case.
func TestSetAllowFromWidens(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "sure"}}})
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	if err := b.SetAllowFrom([]string{"42", "77"}); err != nil {
		t.Fatal(err)
	}
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 77, ID: "1", Text: "hello"})
	if !waitForReply(t, fc, "sure") {
		t.Fatal("a newly authorized sender got no reply")
	}
}
