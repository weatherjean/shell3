//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestCommand_Agents(t *testing.T) {
	fc := newFakeClient()
	rt, sess := newFakeRuntime(t, "ok")
	b := NewBot(fc, rt, sess, 42, "")
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/agents"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "code") {
		t.Fatalf("expected agent list, got %v", fc.sentTexts())
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
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "h.ts.net") {
		t.Fatalf("expected dashboard link, got %v", fc.sentTexts())
	}
}
