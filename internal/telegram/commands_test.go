//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestCommandMenuIsMinimal(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	var names []string
	for _, command := range b.BotCommands() {
		names = append(names, command.Command)
	}
	if got, want := strings.Join(names, ","), "ask,help,stop,superstop,new,reload"; got != want {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}

func TestReloadUnavailableIsExplicit(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "reload is unavailable") {
		t.Fatalf("reload reply = %v", fc.sentTexts())
	}
}

func TestReloadCommandRunsHostCallback(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	called := 0
	b.SetReload(func() error { called++; return nil })
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/reload"})
	if called != 1 || !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "config reloaded") {
		t.Fatalf("called=%d replies=%v", called, fc.sentTexts())
	}
}

func TestCommandBotnameSuffixIsStripped(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/help@my_shell3_bot"})
	joined := strings.Join(fc.sentTexts(), "\n")
	if strings.Contains(joined, "unknown command") || !strings.Contains(joined, "shell3") {
		t.Fatalf("suffixed help reply = %v", fc.sentTexts())
	}
}

func TestSuperstopNothingRunning(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "ok")
	b := newBot(t, fc, rt)
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
	if !strings.Contains(strings.Join(fc.sentTexts(), "\n"), "nothing was running") {
		t.Fatalf("idle superstop reply = %v", fc.sentTexts())
	}
}

func TestSuperstopKillsJobsAndSummarizes(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "bg-call", Name: "bash_bg", RawArgs: `{"command":"sleep 30"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "background job started"}}},
	)
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)
	sess, err := tconv(b).mainSession()
	if err != nil {
		t.Fatal(err)
	}
	for range sess.Send(context.Background(), "start a background job") {
	}
	var id string
	waitFor(t, func() bool {
		for _, job := range sess.Jobs() {
			if !job.Done {
				id = job.ID
				return true
			}
		}
		return false
	})
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/superstop"})
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "superstop") || !strings.Contains(all, id) {
		t.Fatalf("superstop summary = %v", fc.sentTexts())
	}
}
