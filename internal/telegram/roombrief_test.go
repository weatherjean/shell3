//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRoomBriefCarriesTitleAndDescription(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle, fc.chatDesc = "backend-infra", "the payments service; repo at ~/code/pay"
	b := newBot(t, fc, mustRuntime(t))

	got := b.conv(-100).brief()
	if !strings.Contains(got, "backend-infra") || !strings.Contains(got, "-100") {
		t.Fatalf("brief = %q, want the room named", got)
	}
	if !strings.Contains(got, "payments service") {
		t.Fatalf("brief = %q, want the group description", got)
	}
	if !strings.Contains(got, "<group-description>") {
		t.Fatal("the description must be delimited: it is written by chat members, not the operator")
	}
	if !strings.Contains(got, "not an instruction") {
		t.Fatal("the description must be labelled as context rather than instruction")
	}
}

func TestRoomBriefRefreshesAfterCacheDrop(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle, fc.chatDesc = "infra", "first"
	b := newBot(t, fc, mustRuntime(t))
	c := b.conv(-100)
	if !strings.Contains(c.brief(), "first") {
		t.Fatal("expected the first description")
	}

	fc.chatTitle, fc.chatDesc = "infra", "second"
	if strings.Contains(c.brief(), "second") {
		t.Fatal("within the refresh window the cached value should still be used")
	}
	b.refreshChatMeta(context.Background(), -100)
	if !strings.Contains(c.brief(), "second") {
		t.Fatal("after a reload the brief must re-read the description")
	}
}

func TestRoomBriefSurvivesChatInfoFailure(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle, fc.chatDesc = "infra", "known"
	b := newBot(t, fc, mustRuntime(t))
	c := b.conv(-100)
	_ = c.brief()

	fc.failChatInfo = errFakeHTML
	b.refreshChatMeta(context.Background(), -100)
	got := c.brief()
	if !strings.Contains(got, "-100") {
		t.Fatalf("a failed lookup must still yield a usable brief, got %q", got)
	}
}

func TestRoomBriefDoesNotBlockOnAStaleEntry(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle = "infra"
	b := newBot(t, fc, mustRuntime(t))
	c := b.conv(-100)
	_ = c.brief()

	b.metaMu.Lock()
	meta := b.chatMetaCache[-100]
	meta.fetched = time.Now().Add(-2 * briefRefresh)
	b.chatMetaCache[-100] = meta
	b.metaMu.Unlock()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	fc.blockChatInfo = release

	done := make(chan string, 1)
	go func() { done <- c.brief() }()
	select {
	case got := <-done:
		if !strings.Contains(got, "infra") {
			t.Fatalf("stale brief = %q, want the last known title", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("brief() blocked on a hanging getChat — a turn would hang with it")
	}
}

func TestRoomBriefRefreshIsOnePerRoom(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle = "infra"
	b := newBot(t, fc, mustRuntime(t))
	c := b.conv(-100)
	_ = c.brief()

	b.metaMu.Lock()
	meta := b.chatMetaCache[-100]
	meta.fetched = time.Now().Add(-2 * briefRefresh)
	b.chatMetaCache[-100] = meta
	b.metaMu.Unlock()
	release := make(chan struct{})
	fc.blockChatInfo = release
	before := fc.chatInfoCalls()

	for i := 0; i < 5; i++ {
		_ = c.brief()
	}
	waitFor(t, func() bool { return fc.chatInfoCalls() > before })
	close(release)
	if got := fc.chatInfoCalls() - before; got != 1 {
		t.Fatalf("%d concurrent getChat calls for one room, want 1", got)
	}
}
