//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestNotifyInboxPostsWithoutStartingTurn(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "must not run")
	b := newBot(t, fc, rt)
	if err := b.NotifyInbox(context.Background(), 2); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fc.sentTexts(), "\n"); !strings.Contains(got, "Inbox: 2 pending notices") {
		t.Fatalf("sent text = %q", got)
	}
	if sess := b.homeConv().session(); sess != nil {
		t.Fatalf("notification created a session: %v", sess)
	}
}
