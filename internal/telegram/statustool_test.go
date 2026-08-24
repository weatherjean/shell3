//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
)

func TestStatusTool_ReportsAgentAndConfig(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	sess := decoratedSession(t, b, rt)
	out, err := b.statusToolHandler(context.Background(), sess, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "agent: code") {
		t.Fatalf("status should report the active agent, got %q", out)
	}
	if !strings.Contains(out, "config:") {
		t.Fatalf("status should report a config line, got %q", out)
	}
	if !strings.Contains(out, "cron: none") {
		t.Fatalf("status should report cron state, got %q", out)
	}
}

// The rooms section is the agent's only view of what other rooms are doing —
// they share one working directory, so a concurrent turn there can collide
// with the work about to happen here.
func TestStatusListsLiveRooms(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle = "backend-infra"
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "@mybot hi"})
	waitFor(t, func() bool { return b.conv(-100).session() != nil })

	sess := decoratedSession(t, b, rt)
	out, err := b.statusToolHandler(ctx, sess, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "rooms:") || !strings.Contains(out, "backend-infra") || !strings.Contains(out, "-100") {
		t.Fatalf("status = %q, want the live room named", out)
	}
	if !strings.Contains(out, "idle") {
		t.Fatalf("status = %q, want each room's turn state", out)
	}
}

// With no room live the section is absent rather than an empty header.
func TestStatusOmitsRoomsWhenNoneLive(t *testing.T) {
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, newFakeClient(), rt)
	sess := decoratedSession(t, b, rt)
	out, err := b.statusToolHandler(context.Background(), sess, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "rooms:") {
		t.Fatalf("status = %q, want no rooms section", out)
	}
}
