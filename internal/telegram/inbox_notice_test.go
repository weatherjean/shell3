//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestNotifyLifecyclePostsWithoutStartingTurn(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "must not run")
	b := newBot(t, fc, rt)
	if err := b.NotifyLifecycle(context.Background(), StartupNotice); err != nil {
		t.Fatal(err)
	}
	if !fc.lastSilent() {
		t.Fatal("startup notice was not silent")
	}
	if err := b.NotifyLifecycle(context.Background(), ShutdownNotice); err != nil {
		t.Fatal(err)
	}
	if !fc.lastSilent() {
		t.Fatal("shutdown notice was not silent")
	}
	got := fc.sentTexts()
	if len(got) != 2 || got[0] != "๑ï shell3 started" || got[1] != "๑ï shell3 shutting down" {
		t.Fatalf("lifecycle notices = %q", got)
	}
	if sess := b.homeConv().session(); sess != nil {
		t.Fatalf("lifecycle notice created a session: %v", sess)
	}
}

func TestNotifyInboxPostsWithoutStartingTurn(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "must not run")
	b := newBot(t, fc, rt)
	if err := b.NotifyInbox(context.Background(), 2, "wrk.failed", "workflow daily failed\nwith details"); err != nil {
		t.Fatal(err)
	}
	if !fc.lastSilent() {
		t.Fatal("inbox notice was not silent")
	}
	got := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(got, "✉️ Inbox: 2 pending notices") || !strings.Contains(got, "Latest: wrk.failed — workflow daily failed") {
		t.Fatalf("sent text = %q", got)
	}
	if strings.Contains(got, "with details") {
		t.Fatalf("preview exposed more than the summary line: %q", got)
	}
	if sess := b.homeConv().session(); sess != nil {
		t.Fatalf("notification created a session: %v", sess)
	}
}

func TestNotifyInboxBoundsLatestPreview(t *testing.T) {
	fc := newFakeClient()
	b := newBot(t, fc, storeRuntime(t, "must not run"))
	if err := b.NotifyInbox(context.Background(), 1, "done", strings.Repeat("界", 300)); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(fc.sentTexts(), "\n")
	preview := strings.TrimPrefix(strings.Split(got, "\n")[1], "Latest: ")
	if len([]rune(preview)) > inboxPreviewRunes {
		t.Fatalf("preview has %d runes: %q", len([]rune(preview)), preview)
	}
}
