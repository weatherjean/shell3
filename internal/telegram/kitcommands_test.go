package telegram

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

func TestKitCommand_Answers(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetKitCommands(
		[]KitCommand{{Name: "standup", Desc: "yesterday's commits"}},
		func(_ context.Context, name, arg string) (string, error) {
			return "ran " + name + " with " + arg, nil
		})

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/standup week"})

	if got := strings.Join(fc.sentTexts(), "\n"); !strings.Contains(got, "ran standup with week") {
		t.Fatalf("sent = %v", fc.sentTexts())
	}
}

func TestKitCommand_FailureReported(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetKitCommands(
		[]KitCommand{{Name: "broken", Desc: "fails"}},
		func(context.Context, string, string) (string, error) {
			return "", errors.New("exit 1: no repo here")
		})

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/broken"})

	if got := strings.Join(fc.sentTexts(), "\n"); !strings.Contains(got, "no repo here") {
		t.Fatalf("sent = %v", fc.sentTexts())
	}
}

func TestKitCommand_EmptyOutputPostsNothing(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetKitCommands(
		[]KitCommand{{Name: "hush", Desc: "says nothing"}},
		func(context.Context, string, string) (string, error) { return "", nil })

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/hush"})

	if got := fc.sentTexts(); len(got) != 0 {
		t.Fatalf("sent = %v, want nothing", got)
	}
}

func TestKitCommand_BuiltinWins(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := false
	b.SetKitCommands(
		[]KitCommand{{Name: "stop", Desc: "shadow attempt"}},
		func(context.Context, string, string) (string, error) {
			called = true
			return "kit ran", nil
		})

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	if called {
		t.Error("kit command ran for a built-in verb")
	}
}

func TestKitCommand_UnknownStillUnknown(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetKitCommands([]KitCommand{{Name: "standup", Desc: "x"}},
		func(context.Context, string, string) (string, error) { return "no", nil })

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/nope"})

	if got := strings.Join(fc.sentTexts(), "\n"); !strings.Contains(got, "unknown command") {
		t.Fatalf("sent = %v", fc.sentTexts())
	}
}

func TestBotCommandsIncludeKitCommands(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	b.SetKitCommands([]KitCommand{{Name: "standup", Desc: "yesterday's commits"}}, nil)

	var found bool
	for _, c := range b.BotCommands() {
		if c.Command == "standup" && c.Description == "yesterday's commits" {
			found = true
		}
	}
	if !found {
		t.Fatalf("BotCommands = %+v, want a standup entry", b.BotCommands())
	}
}

func TestReservedCommandsCoverBuiltins(t *testing.T) {
	for _, c := range BotCommands() {
		if !slices.Contains(kit.ReservedCommands, c.Command) {
			t.Errorf("built-in /%s is not in kit.ReservedCommands — a kit could declare it and never see it fire", c.Command)
		}
	}
}
