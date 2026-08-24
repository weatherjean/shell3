package telegram

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/kit"
)

// A kit-declared command is answered by its shell function, with no model
// turn: the runner's stdout is the reply.
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

// A failing command reports the failure rather than posting nothing.
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

// A command whose output is empty posts nothing — an idempotent command with
// nothing to say should not spam the chat.
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

// A built-in always wins. The kit parser rejects the collision at load, so a
// runner reaching here for a built-in name would be a second bug; this pins
// that dispatch order regardless.
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

// A verb neither built-in nor kit-declared still answers "unknown command".
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

// Kit commands join the registered command menu, after the built-ins, so they
// appear in the client's "/" autocomplete.
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

// kit.ReservedCommands is what the kit parser refuses to let a command
// shadow. It cannot be derived from BotCommands at parse time without
// internal/kit importing the front-end, so this test is the pin: adding a
// built-in without reserving its name would let a kit declare a command that
// silently never fires.
func TestReservedCommandsCoverBuiltins(t *testing.T) {
	for _, c := range BotCommands() {
		if !slices.Contains(kit.ReservedCommands, c.Command) {
			t.Errorf("built-in /%s is not in kit.ReservedCommands — a kit could declare it and never see it fire", c.Command)
		}
	}
}
