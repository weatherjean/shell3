package shell3

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/store"
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

// TestTranslateErrorPassesTypedErrThrough verifies translate threads the typed
// chat.Event.Err verbatim (not a string-rebuilt copy), so errors.Is works.
func TestTranslateErrorPassesTypedErrThrough(t *testing.T) {
	sentinel := errors.New("typed boom")
	got, ok := translate(chat.Event{Kind: chat.EventError, Text: sentinel.Error(), Err: sentinel})
	if !ok || got.Kind != Error {
		t.Fatalf("translate error event: got %+v ok=%v", got, ok)
	}
	if !errors.Is(got.Err, sentinel) {
		t.Fatalf("typed error not preserved through translate: %v", got.Err)
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

func TestSession_ID_NoStoreReportsZero(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()

	// newTestSession configures no store, so ID reports the documented "0".
	if got := s.ID(); got != "0" {
		t.Fatalf("ID() = %q, want %q (no store)", got, "0")
	}
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

// TestSession_Close_ReturnsEndSessionError verifies Close surfaces the store's
// EndSession error instead of always returning nil. The underlying store DB is
// closed before Close runs, forcing EndSession to fail; embedders' `if err :=
// sess.Close(); err != nil` must then see a non-nil error.
func TestSession_Close_ReturnsEndSessionError(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{Store: st})

	// Close the store's DB so EndSession fails when Close runs.
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close returned nil; expected the EndSession error to be surfaced")
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

func TestSession_SwitchAgent_Unknown(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	cfg := chat.Config{
		SwitchAgent: func(name string) (chat.ActiveAgent, error) {
			return chat.ActiveAgent{}, errors.New("unknown agent " + name)
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchAgent("nope"); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestSession_SwitchAgent_Applies(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	newClient := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "y"}}})
	cfg := chat.Config{
		AgentNames: []string{"base", "plan"},
		SwitchAgent: func(name string) (chat.ActiveAgent, error) {
			return chat.ActiveAgent{LLM: newClient, ModeLabel: name, ModelID: "m2", ContextWindow: 1000}, nil
		},
	}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SwitchAgent("plan"); err != nil {
		t.Fatalf("SwitchAgent: %v", err)
	}
	if s.cfg.LLM != chat.LLMClient(newClient) {
		t.Fatal("SwitchAgent did not swap the active client")
	}
	if s.ActiveAgent() != "plan" {
		t.Fatalf("ActiveAgent() = %q, want plan", s.ActiveAgent())
	}
}

func TestSession_CloseDoesNotDeadlockWhenSendChannelAbandoned(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "a"}, {TextDelta: "b"}, {TextDelta: "c"},
	}})
	s := newTestSession(t, client, chat.Config{})

	// Abandon the Send channel: never read it, so drain parks on the unbuffered
	// forward of the first token.
	out := s.Send(context.Background(), "hi")

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked: drain wedged on the abandoned unbuffered Send channel")
	}

	// Teardown must also CLOSE the abandoned Send channel so a consumer that
	// later (or concurrently) ranges over it observes EOF instead of hanging.
	for {
		select {
		case _, ok := <-out:
			if !ok {
				return // channel closed as required
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Send channel was not closed on Close; a ranging consumer would hang")
		}
	}
}

// blockingClient.Stream blocks until its ctx is cancelled, simulating an
// in-flight LLM stream. It signals when Stream is entered and when it returns.
type blockingClient struct {
	entered  chan struct{}
	returned chan struct{}
}

func (c *blockingClient) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamEvent)) error {
	close(c.entered)
	<-ctx.Done()
	close(c.returned)
	return ctx.Err()
}

func TestSession_ErrorEventPreservesTypedError(t *testing.T) {
	sentinel := errors.New("provider exploded")
	client := fakellm.New(fakellm.Script{Err: sentinel})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	var gotErr error
	for ev := range s.Send(context.Background(), "hi") {
		if ev.Kind == Error {
			gotErr = ev.Err
		}
	}
	if gotErr == nil {
		t.Fatal("no Error event received")
	}
	if !errors.Is(gotErr, sentinel) {
		t.Fatalf("public Error event lost the typed error: errors.Is(%v, sentinel) = false", gotErr)
	}
}

func TestSession_CloseCancelsAndJoinsInFlightTurn(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}), returned: make(chan struct{})}
	s := newTestSession(t, client, chat.Config{})

	out := s.Send(context.Background(), "hi")
	// Drain the turn channel in the background so drain() can forward the
	// terminal event (a real caller drains; this avoids the unrelated
	// unbuffered-Send-channel block, which is a separate finding).
	go func() {
		for range out {
		}
	}()

	// Wait until the turn is actually in-flight (Stream entered and blocked).
	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Stream never entered")
	}

	// Close must cancel the in-flight turn AND join its goroutine before
	// returning. Before the fix, Close returns without cancelling, so the
	// blocked Stream goroutine is leaked and `returned` is never closed.
	closeDone := make(chan struct{})
	go func() {
		_ = s.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return (deadlock)")
	}

	// Join proof: by the time Close returned, the turn's Stream must have
	// returned (i.e. Close waited for the turn goroutine, so the deferred
	// history persist completed before the store would be closed).
	select {
	case <-client.returned:
	default:
		t.Fatal("Close returned before the in-flight turn finished — turn not cancelled/joined (leak + potential write-after-close)")
	}
}
