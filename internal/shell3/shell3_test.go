package shell3

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want Event
	}{
		{"token", chat.Event{Kind: chat.EventAssistantToken, Text: "hi"}, Event{Kind: Token, Text: "hi"}},
		{"reasoning", chat.Event{Kind: chat.EventAssistantReasoning, Text: "think"}, Event{Kind: Reasoning, Text: "think"}},
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}, Event{Kind: ToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"}, Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &llm.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &llm.Usage{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, Event{Kind: Retry, Text: "retrying"}},
		{"compacted", chat.Event{Kind: chat.EventCompacted, Text: "context auto-compacted at 100000 tokens", Usage: &llm.Usage{PromptTokens: 1200, TotalTokens: 1200}}, Event{Kind: Compacted, Text: "context auto-compacted at 100000 tokens", PromptTokens: 1200, TotalTokens: 1200}},
		{"error", chat.Event{Kind: chat.EventError, Text: "boom"}, Event{Kind: Error}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := translate(tc.in)
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName ||
				got.ToolInput != tc.want.ToolInput ||
				got.ToolOutput != tc.want.ToolOutput || got.PromptTokens != tc.want.PromptTokens ||
				got.CompletionTokens != tc.want.CompletionTokens || got.TotalTokens != tc.want.TotalTokens {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, tc.want)
			}
			if tc.want.Kind == Error && (got.Err == nil || got.Err.Error() != tc.in.Text) {
				t.Fatalf("error: got Err=%v want %q", got.Err, tc.in.Text)
			}
		})
	}
}

func TestTranslateErrorPassesTypedErrThrough(t *testing.T) {
	sentinel := errors.New("typed boom")
	got := translate(chat.Event{Kind: chat.EventError, Text: sentinel.Error(), Err: sentinel})
	if got.Kind != Error {
		t.Fatalf("translate error event: got %+v", got)
	}
	if !errors.Is(got.Err, sentinel) {
		t.Fatalf("typed error not preserved through translate: %v", got.Err)
	}
}

func TestTranslateUnknownKindBecomesError(t *testing.T) {
	got := translate(chat.Event{Kind: chat.EventKind(999)})
	if got.Kind != Error || got.Err == nil || !strings.Contains(got.Err.Error(), "unknown chat event kind") {
		t.Fatalf("unknown event translation = %+v", got)
	}
}

// newTestSession builds a Session backed by a fakellm client, bypassing
// runtime assembly so the test needs no real config/network. It mirrors what Start
// produces: a persistent chat.Session + drain over a fake-LLM chat.Config.
func newTestSession(t *testing.T, client chat.LLMClient, cfg chat.Config) *Session {
	t.Helper()
	cfg.LLM = client
	if cfg.WorkDir == "" {
		cfg.WorkDir = t.TempDir()
	}
	return newSession(cfg, SessionOpts{})
}

func TestSession_ID_NoStoreReportsEmpty(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()

	if got := s.ID(); got != "" {
		t.Fatalf("ID() = %q, want %q (no store)", got, "")
	}
}

func TestSend_AfterCloseReturnsErrClosed(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	var got error
	for ev := range s.Send(context.Background(), "too late") {
		if ev.Kind == Error {
			got = ev.Err
		}
	}
	if !errors.Is(got, ErrClosed) {
		t.Fatalf("Send after Close = %v, want ErrClosed", got)
	}
}

func TestSession_History_CarriesReasoning(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ReasoningDelta: "let me think about 42"},
			{TextDelta: "the answer"},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "followed up"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	for range s.Send(context.Background(), "question") {
	}
	for range s.Send(context.Background(), "follow up") {
	}
	calls := client.CallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	var got string
	for _, m := range calls[1].Msgs {
		if m.Role == llm.RoleAssistant && m.ReasoningContent != "" {
			got = m.ReasoningContent
		}
	}
	if got != "let me think about 42" {
		t.Fatalf("assistant reasoning = %q, want the streamed thinking text", got)
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
	calls := client.CallsSnapshot()
	if len(calls) != 2 || len(calls[1].Msgs) < 4 {
		t.Fatalf("second model call did not carry prior history: %+v", calls)
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

func TestSession_Close_ReturnsEndSessionError(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{Store: st})

	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close returned nil; expected the EndSession error to be surfaced")
	}
}

func TestSession_CloseDoesNotDeadlockWhenSendChannelAbandoned(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "a"}, {TextDelta: "b"}, {TextDelta: "c"},
	}})
	s := newTestSession(t, client, chat.Config{})

	out := s.Send(context.Background(), "hi")

	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close deadlocked: drain wedged on the abandoned unbuffered Send channel")
	}

	for {
		select {
		case _, ok := <-out:
			if !ok {
				return
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
	go func() {
		for range out {
		}
	}()

	select {
	case <-client.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Stream never entered")
	}

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

	select {
	case <-client.returned:
	default:
		t.Fatal("Close returned before the in-flight turn finished — turn not cancelled/joined (leak + potential write-after-close)")
	}
}

func TestSnapshot_PopulatesRuntimeFields(t *testing.T) {
	client := fakellm.New()
	cfg := chat.Config{
		ModelID:    "gpt-x",
		AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
	}
	cfg.Profile.SystemPrompt = "be helpful"
	cfg.Profile.Tools = []llm.ToolDefinition{{Name: "bash", Description: "run a command"}}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	snap := s.Snapshot()
	if snap.ContextWindow != 4096 {
		t.Fatalf("snapshot status/window wrong: %+v", snap)
	}
	if len(snap.Tools) != 1 || snap.Tools[0].Name != "bash" {
		t.Fatalf("snapshot tools = %+v", snap.Tools)
	}
}

func TestSend_TextPath(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "reply"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	var text string
	var done bool
	for ev := range s.Send(context.Background(), "hi there") {
		switch ev.Kind {
		case Token:
			text += ev.Text
		case Done:
			done = true
		}
	}
	if text != "reply" || !done {
		t.Fatalf("Send text path: text=%q done=%v", text, done)
	}
}

func TestSession_BusyEnforcement(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}), returned: make(chan struct{})}
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := s.Send(ctx, "hi")
	<-client.entered

	var rejected []Event
	for ev := range s.Send(context.Background(), "overlap") {
		rejected = append(rejected, ev)
	}
	if len(rejected) != 1 || rejected[0].Kind != Error || !errors.Is(rejected[0].Err, ErrBusy) {
		t.Fatalf("overlapping Send: want exactly one ErrBusy Error event, got %+v", rejected)
	}

	cancel()
	for range out {
	}
	if s.isBusy() {
		t.Fatal("the busy gate did not clear after the turn drained")
	}
}

func TestSession_InterjectMidTurn(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "poke", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	var s *Session
	cfg := chat.Config{
		LLM:        client,
		AgentKnobs: chat.AgentKnobs{HostToolNames: map[string]bool{"poke": true}},
		HostTool: func(ctx context.Context, name, args string) (string, error) {
			s.Interject("change of plans")
			return "ok", nil
		},
	}
	s = newTestSession(t, client, cfg)
	defer s.Close()

	for range s.Send(context.Background(), "go") {
	}
	if !callsContain(client.CallsSnapshot(), "change of plans") {
		t.Fatal("mid-turn Interject was not included in the next provider round")
	}
}

func TestSession_InterjectWhileIdle(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("remember the deadline")
	for range s.Send(context.Background(), "hi") {
	}
	if !callsContain(client.CallsSnapshot(), "remember the deadline") {
		t.Fatal("idle Interject should be injected at the start of the next turn")
	}
}

func callsContain(calls []fakellm.Call, text string) bool {
	for _, call := range calls {
		for _, msg := range call.Msgs {
			if strings.Contains(msg.Content, text) {
				return true
			}
		}
	}
	return false
}

func TestSessionRunningJobsFromManager(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rt.jobs.startCommand(s, "sleep 1", t.TempDir(), []string{"sleep", "1"}, nil)
	if got := s.RunningJobs(); got != 1 {
		t.Fatalf("Session.RunningJobs = %d, want 1", got)
	}
}

func TestTurnConfigHeadless(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()
	s.mu.Lock()
	tc := s.turnConfigLocked()
	s.mu.Unlock()
	if tc.Headless {
		t.Fatal("default session: want Headless=false")
	}
	s.cfg.Headless = true
	s.mu.Lock()
	tc = s.turnConfigLocked()
	s.mu.Unlock()
	if !tc.Headless {
		t.Fatal("headless config: want Headless=true")
	}
}
