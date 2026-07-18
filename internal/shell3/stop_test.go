package shell3

import (
	"context"
	"strings"
	"testing"
)

func TestSetUsageText(t *testing.T) {
	if !strings.Contains(SetUsageText, "usage: /set <name> <value>") ||
		!strings.Contains(SetUsageText, "no arguments") {
		t.Fatalf("SetUsageText = %q", SetUsageText)
	}
}

func TestStopAllNothingRunning(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	got := StopAll(s, func() context.CancelFunc { return nil })
	if got != "nothing running" {
		t.Fatalf("StopAll = %q, want %q", got, "nothing running")
	}
}

func TestStopAllCancelsRunningTurn(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	called := false
	got := StopAll(s, func() context.CancelFunc {
		return func() { called = true }
	})
	if got != "⏹ stopped" {
		t.Fatalf("StopAll = %q, want %q", got, "⏹ stopped")
	}
	if !called {
		t.Fatal("snapshot's cancel func was not invoked")
	}
}
