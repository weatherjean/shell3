//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/shell3"
)

func TestCommand_SetNoArgsLists(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/set"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "settable") {
		t.Fatalf("expected bare /set to list settable parameters, got %v", fc.sentTexts())
	}
}

func TestCommand_Clear(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "cleared") {
		t.Fatalf("expected clear ack, got %v", fc.sentTexts())
	}
}

func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	fired := ""
	b.SetJobRunner(func(name string) error { fired = name; return nil })
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run nightly"})
	if fired != "nightly" {
		t.Fatalf("expected /run to fire job 'nightly', fired %q", fired)
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nightly") {
		t.Fatalf("expected an ack mentioning the job, got %v", fc.sentTexts())
	}
}

func TestCommand_RunNoRunner(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}

func TestCommand_Reload(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	called := false
	b.SetReloader(func() (shell3.ReloadResult, error) {
		called = true
		return shell3.ReloadResult{Agents: 3, Jobs: 1}, nil
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !called {
		t.Fatal("expected /reload to invoke the reloader")
	}
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reloaded") {
		t.Fatalf("expected a success reply, got %v", fc.sentTexts())
	}
}

func TestCommand_ReloadNoReloader(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload not available") {
		t.Fatalf("expected unavailable reply, got %v", fc.sentTexts())
	}
}

// TestCommand_CompactNothing pins the /compact wiring on a fresh session:
// nothing to summarise yet, so the bot reports that rather than erroring. The
// reply is asynchronous — /compact runs off the update loop under the turn
// slot — so the test polls for it.
func TestCommand_CompactNothing(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42)
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/compact"})
	deadline := time.After(3 * time.Second)
	for {
		if strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nothing to compact") {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected 'nothing to compact' on a fresh session, got %v", fc.sentTexts())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
