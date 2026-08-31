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
	// Wait for the room to be IDLE, not merely to exist: the turn this message
	// started reports "BUSY (mid-turn)" until it ends, and asserting on "idle"
	// while it runs is a race the -race build loses reliably.
	waitFor(t, func() bool { return b.conv(-100).session() != nil && !b.conv(-100).busy() })

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

func TestBriefStateNamesWhyADescriptionIsMissing(t *testing.T) {
	cases := []struct {
		name           string
		meta           chatMeta
		group, useDesc bool
		want           string
	}{
		{"direct chat says nothing", chatMeta{known: true}, false, true, ""},
		{"turned off", chatMeta{known: true, description: "x"}, true, false, "off for this room"},
		{"not fetched yet", chatMeta{}, true, true, "not looked up yet"},
		{"present", chatMeta{known: true, description: "hello"}, true, true, "in your prompt (5 bytes)"},
		{"empty from telegram", chatMeta{known: true}, true, true, "admin"},
	}
	for _, tc := range cases {
		got := briefState(tc.meta, tc.group, tc.useDesc)
		if tc.want == "" {
			if got != "" {
				t.Errorf("%s: got %q, want empty", tc.name, got)
			}
			continue
		}
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s: got %q, want it to mention %q", tc.name, got, tc.want)
		}
	}
}

func TestStatusReportsDescriptionState(t *testing.T) {
	fc := newFakeClient()
	fc.chatTitle, fc.chatDesc = "backend-infra", "about the payments service"
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	if err := b.SetAllowFrom([]string{"7"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	b.handleMsg(ctx, Msg{ChatID: -100, ChatType: "supergroup", SenderID: 7, ID: "1", Text: "@mybot hi"})
	waitFor(t, func() bool { return b.conv(-100).session() != nil })

	out, err := b.statusToolHandler(ctx, decoratedSession(t, b, rt), "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "description: in your prompt") {
		t.Fatalf("status = %q, want the room's description state", out)
	}
}
