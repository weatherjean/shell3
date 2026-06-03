package shell3

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want *Event // nil = dropped
	}{
		{"token", chat.Event{Kind: chat.EventAssistantToken, Text: "hi"}, &Event{Kind: Token, Text: "hi"}},
		{"reasoning", chat.Event{Kind: chat.EventAssistantReasoning, Text: "think"}, &Event{Kind: Reasoning, Text: "think"}},
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}, &Event{Kind: ToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"}, &Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, &Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &chat.EventUsageData{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, &Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, &Event{Kind: Retry, Text: "retrying"}},
		{"error", chat.Event{Kind: chat.EventError, Text: "boom"}, &Event{Kind: Error}},
		{"session start dropped", chat.Event{Kind: chat.EventSessionStart}, nil},
		{"user message dropped", chat.Event{Kind: chat.EventUserMessage}, nil},
		{"assistant message dropped", chat.Event{Kind: chat.EventAssistantMessage}, nil},
		{"system reminder dropped", chat.Event{Kind: chat.EventSystemReminder}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translate(tc.in)
			if tc.want == nil {
				if ok {
					t.Fatalf("expected drop, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatal("expected event, got drop")
			}
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName || got.ToolInput != tc.want.ToolInput ||
				got.ToolOutput != tc.want.ToolOutput || got.PromptTokens != tc.want.PromptTokens ||
				got.CompletionTokens != tc.want.CompletionTokens || got.TotalTokens != tc.want.TotalTokens {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, *tc.want)
			}
			if tc.want.Kind == Error && (got.Err == nil || got.Err.Error() != tc.in.Text) {
				t.Fatalf("error: got Err=%v want %q", got.Err, tc.in.Text)
			}
		})
	}
}

// newTestSession builds a Session backed by a fakellm client, bypassing
// agentsetup so the test needs no real config/network. It mirrors what Start
// produces: a persistent chat.Session + drain over a fake-LLM chat.Config.
func newTestSession(t *testing.T, client chat.LLMClient, cfg chat.Config) *Session {
	t.Helper()
	cfg.LLM = client
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	if cfg.Personality.Name == "" {
		cfg.Personality.Name = "test"
	}
	return newSession(cfg, func() {})
}

func TestSession_MultiTurn_HistoryCarries(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "first"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "second"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	collect := func(ch <-chan Event) (text string, done bool) {
		for ev := range ch {
			switch ev.Kind {
			case Token:
				text += ev.Text
			case Done:
				done = true
			}
		}
		return
	}

	t1, d1 := collect(s.Send(context.Background(), "hello"))
	if t1 != "first" || !d1 {
		t.Fatalf("turn 1: text=%q done=%v", t1, d1)
	}
	t2, d2 := collect(s.Send(context.Background(), "again"))
	if t2 != "second" || !d2 {
		t.Fatalf("turn 2: text=%q done=%v", t2, d2)
	}
	// Two user turns + two assistant replies must be retained.
	if got := len(s.sess.Messages()); got < 4 {
		t.Fatalf("history has %d messages, want >= 4 (2 turns)", got)
	}
}

func TestSession_ErrorPath(t *testing.T) {
	client := fakellm.New(fakellm.Script{Err: errors.New("provider down")})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	var sawError, sawDone bool
	for ev := range s.Send(context.Background(), "hi") {
		switch ev.Kind {
		case Error:
			sawError = true
		case Done:
			sawDone = true
		}
	}
	if !sawError {
		t.Fatal("expected Error event")
	}
	if sawDone {
		t.Fatal("did not expect Done on error path")
	}
}

func TestRun_BadConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	ch, err := Run(context.Background(), Spec{
		Prompt:     "hi",
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		WorkDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if ch != nil {
		t.Fatal("expected nil channel on start failure")
	}
}

func TestSession_Clear_ResetsHistory(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "b"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	for range s.Send(context.Background(), "first") {
	}
	if len(s.sess.Messages()) == 0 {
		t.Fatal("expected history after first turn")
	}
	s.Clear()
	if got := len(s.sess.Messages()); got != 0 {
		t.Fatalf("after Clear: %d messages, want 0", got)
	}
}

func TestSession_Rollback(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	if s.Rollback() {
		t.Fatal("Rollback on empty history should return false")
	}
	for range s.Send(context.Background(), "hi") {
	}
	if !s.Rollback() {
		t.Fatal("Rollback after a turn should return true")
	}
	if got := len(s.sess.Messages()); got != 0 {
		t.Fatalf("after Rollback: %d messages, want 0", got)
	}
}

func TestSession_SwitchModel_Unknown(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	cfg := chat.Config{
		SwitchModel: func(name string) (chat.ActiveModel, error) {
			return chat.ActiveModel{}, errors.New("unknown model " + name)
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchModel("nope"); err == nil {
		t.Fatal("expected error for unknown model")
	}
}

func TestSession_SwitchModel_Applies(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	newClient := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "y"}}})
	cfg := chat.Config{
		SwitchModel: func(name string) (chat.ActiveModel, error) {
			return chat.ActiveModel{Client: newClient, ModelID: "m2", ContextWindow: 1000}, nil
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchModel("m2"); err != nil {
		t.Fatalf("SwitchModel: %v", err)
	}
	if s.cfg.LLM != chat.LLMClient(newClient) {
		t.Fatal("SwitchModel did not swap the active client")
	}
}
