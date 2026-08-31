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
	"github.com/weatherjean/shell3/internal/notify"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want *Event
	}{
		{"token", chat.Event{Kind: chat.EventAssistantToken, Text: "hi"}, &Event{Kind: Token, Text: "hi"}},
		{"reasoning", chat.Event{Kind: chat.EventAssistantReasoning, Text: "think"}, &Event{Kind: Reasoning, Text: "think"}},
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolCallID: "3", ToolInput: `{"cmd":"ls"}`}, &Event{Kind: ToolCall, ToolName: "bash", ToolCallID: "3", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolCallID: "3", ToolOutput: "ok"}, &Event{Kind: ToolResult, ToolName: "bash", ToolCallID: "3", ToolOutput: "ok"}},
		{"system reminder", chat.Event{Kind: chat.EventSystemReminder, Text: "<system-reminder>\nmodel changed\n</system-reminder>"}, &Event{Kind: SystemReminder, Text: "<system-reminder>\nmodel changed\n</system-reminder>"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, &Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &chat.EventUsageData{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, &Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, &Event{Kind: Retry, Text: "retrying"}},
		{"compacted", chat.Event{Kind: chat.EventCompacted, Text: "context auto-compacted at 100000 tokens", Usage: &chat.EventUsageData{PromptTokens: 1200, TotalTokens: 1200}}, &Event{Kind: Compacted, Text: "context auto-compacted at 100000 tokens", PromptTokens: 1200, TotalTokens: 1200}},
		{"error", chat.Event{Kind: chat.EventError, Text: "boom"}, &Event{Kind: Error}},
		{"session start dropped", chat.Event{Kind: chat.EventSessionStart}, nil},
		{"user message dropped", chat.Event{Kind: chat.EventUserMessage}, nil},
		{"assistant message dropped", chat.Event{Kind: chat.EventAssistantMessage}, nil},
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
				got.ToolName != tc.want.ToolName || got.ToolCallID != tc.want.ToolCallID ||
				got.ToolInput != tc.want.ToolInput ||
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
	return newSession(cfg, SessionOpts{})
}

func TestSession_ID_NoStoreReportsEmpty(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()

	if got := s.ID(); got != "" {
		t.Fatalf("ID() = %q, want %q (no store)", got, "")
	}
}

// TestSend_AfterCloseReturnsErrClosed pins the teardown contract: a Send that
// races session close (e.g. a Wake-driven queued drain) must be rejected with
// ErrClosed instead of running a turn against the ended store record.
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

// TestSession_History_CarriesReasoning proves a live turn's reasoning reaches
// the stored message history (llm.Message.ReasoningContent) — the Chat-tab
// thinking path; it is independent of resume (which doesn't persist reasoning
// by design).
func TestSession_History_CarriesReasoning(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
		{ReasoningDelta: "let me think about 42"},
		{TextDelta: "the answer"},
	}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	for range s.Send(context.Background(), "question") {
	}
	var got string
	for _, m := range s.sess.Messages() {
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

	// Teardown must also CLOSE the abandoned Send channel so a consumer that
	// later (or concurrently) ranges over it observes EOF instead of hanging.
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

func TestRoute_SetsIsHostTool(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{
		AgentKnobs: chat.AgentKnobs{HostToolNames: map[string]bool{"my_tool": true}},
	})
	defer s.Close()

	got := make(chan Event, 4)
	done := make(chan struct{})
	s.mu.Lock()
	s.cur = got
	s.curDone = done
	s.mu.Unlock()

	s.route(chat.Event{Kind: chat.EventToolCall, ToolName: "my_tool", ToolCallID: "1"})
	s.route(chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolCallID: "2"})

	custom := <-got
	if custom.Kind != ToolCall || custom.ToolName != "my_tool" || !custom.IsHostTool {
		t.Fatalf("host tool event = %+v, want IsHostTool=true", custom)
	}
	if custom.ToolCallID != "1" {
		t.Fatalf("ToolCallID = %q, want 1", custom.ToolCallID)
	}
	builtin := <-got
	if builtin.IsHostTool {
		t.Fatalf("builtin tool wrongly flagged custom: %+v", builtin)
	}
}

func TestSnapshot_PopulatesFromConfig(t *testing.T) {
	client := fakellm.New()
	cfg := chat.Config{
		ModeLabel:    "code",
		StatusLine:   "openai │ gpt-x │ high",
		AgentKnobs:   chat.AgentKnobs{ContextWindow: 4096},
		ActiveSkills: []string{"a", "b"},
		Params:       llm.RequestParams{ReasoningEffort: "high", MaxTokens: 512},
	}
	cfg.Personality.SystemPrompt = "be helpful"
	cfg.Personality.Tools = []llm.ToolDefinition{{Name: "bash", Description: "run a command"}}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	snap := s.Snapshot()
	if snap.Agent != "code" || snap.Model != "gpt-x" {
		t.Fatalf("snapshot header wrong: %+v", snap)
	}
	if snap.StatusLine != "openai │ gpt-x │ high" || snap.ContextWindow != 4096 {
		t.Fatalf("snapshot status/window wrong: %+v", snap)
	}
	if snap.SystemPrompt != "be helpful" {
		t.Fatalf("SystemPrompt = %q", snap.SystemPrompt)
	}
	if len(snap.Skills) != 2 || snap.Skills[0] != "a" {
		t.Fatalf("Skills = %v", snap.Skills)
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
	if len(s.sess.Messages()) < 2 {
		t.Fatalf("history not carried: %d messages", len(s.sess.Messages()))
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

	var sawReminder bool
	for ev := range s.Send(context.Background(), "go") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "change of plans") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatal("mid-turn Interject should surface as a SystemReminder event in the same turn")
	}
}

func TestSession_InterjectWhileIdle(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	s.Interject("remember the deadline")
	var sawReminder bool
	for ev := range s.Send(context.Background(), "hi") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "remember the deadline") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatal("idle Interject should be injected at the start of the next turn")
	}
}

func TestSessionJobsFromManager(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rt.jobs.startCommand(s, "sleep 1", t.TempDir(), []string{"sleep", "1"}, nil, notify.ReportAuto, "")
	jobs := s.Jobs()
	if len(jobs) != 1 || jobs[0].Kind != JobCommand {
		t.Fatalf("Session.Jobs = %+v, want one JobCommand", jobs)
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
