//go:build unix

package telegram

import (
	"context"
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
