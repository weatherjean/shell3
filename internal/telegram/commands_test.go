//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestCommand_SetNoArgsLists(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/set"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "settable") {
		t.Fatalf("expected bare /set to list settable parameters, got %v", fc.sentTexts())
	}
}

func TestCommand_Clear(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/clear"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "cleared") {
		t.Fatalf("expected clear ack, got %v", fc.sentTexts())
	}
}

func TestCommand_Dash(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.dashURL = "https://h.ts.net/"
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/dash"})
	btns := fc.lastButtons()
	if len(btns) != 1 || btns[0].WebApp != "https://h.ts.net/" {
		t.Fatalf("expected a Web App button to the dashboard URL, got %+v", btns)
	}
}

func TestCommand_DashDisabled(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/dash"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "disabled") {
		t.Fatalf("expected disabled message, got %v", fc.sentTexts())
	}
}

func TestCommand_Run(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
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
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/run x"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "no scheduled jobs") {
		t.Fatalf("expected a no-jobs reply, got %v", fc.sentTexts())
	}
}
