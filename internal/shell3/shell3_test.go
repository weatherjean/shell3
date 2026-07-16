package shell3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		want *Event // nil = dropped
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
	return newSession(cfg, SessionOpts{})
}

func TestSession_ID_NoStoreReportsEmpty(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()

	// newTestSession configures no store, so ID reports the documented "".
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

// TestSetSafetyOff_AutoAllowsAsks pins the session-level disable_safety
// toggle: with safety off, the turn's Asker allows without consulting the
// human asker; toggled back on, the real asker is consulted again.
func TestSetSafetyOff_AutoAllowsAsks(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()
	askerCalled := false
	s.asker = func(ctx context.Context, command, reason string) bool {
		askerCalled = true
		return false
	}

	s.SetSafetyOff(true)
	if !s.turnConfig().Asker(context.Background(), "rm -rf /", "test") {
		t.Fatal("safety off: ask should auto-allow")
	}
	if askerCalled {
		t.Fatal("safety off: the human asker must not be consulted")
	}

	s.SetSafetyOff(false)
	if s.turnConfig().Asker(context.Background(), "rm -rf /", "test") {
		t.Fatal("safety on: the denying asker's verdict should stand")
	}
	if !askerCalled {
		t.Fatal("safety on: the human asker should be consulted")
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
// closed before Close runs, forcing EndSession to fail; front-ends' `if err :=
// sess.Close(); err != nil` must then see a non-nil error.
func TestSession_Close_ReturnsEndSessionError(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{Store: st})

	// Delete the run's meta.json so EndSession (which reads meta) fails when Close
	// runs; front-ends' `if err := sess.Close(); err != nil` must then see it.
	if err := os.RemoveAll(filepath.Join(root, "runs", s.sess.ID())); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("Close returned nil; expected the EndSession error to be surfaced")
	}
}

// TestSession_Compact_Delta pins the manual /compact path end to end at the
// shell3 layer: a compactable history is summarised (one quiet fakellm call)
// and the reported token estimates shrink.
func TestSession_Compact_Delta(t *testing.T) {
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
	)
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	// Seed a large history directly: many chunky turns so the head dwarfs the
	// preserved tail.
	big := strings.Repeat("x", 2000)
	msgs := make([]llm.Message, 0, 40)
	for i := 0; i < 20; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleUser, Content: big},
			llm.Message{Role: llm.RoleAssistant, Content: big},
		)
	}
	s.sess.SetMessages(msgs)

	before, after, err := s.Compact(context.Background())
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if after <= 0 || before <= after {
		t.Fatalf("want before > after > 0, got before=%d after=%d", before, after)
	}

	// A fresh session has nothing to compact.
	s.sess.SetMessages([]llm.Message{{Role: llm.RoleUser, Content: "hi"}})
	if _, _, err := s.Compact(context.Background()); !errors.Is(err, chat.ErrNothingToCompact) {
		t.Fatalf("Compact on tiny history: want ErrNothingToCompact, got %v", err)
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

// TestSession_Clear_RotatesStoreSession verifies /clear ends the current store
// session (so it becomes a finished past conversation that retains its history)
// and opens a fresh one that later turns record under — rather than leaving the
// same open session lingering at the top of the dashboard's Runs list.
func TestSession_Clear_RotatesStoreSession(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("runs.Open: %v", err)
	}
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a"}}})
	s := newTestSession(t, client, chat.Config{Store: st})
	defer s.Close()

	old := s.sess.ID()
	if old == "" {
		t.Fatal("expected a non-empty store session id after start")
	}
	for range s.Send(context.Background(), "first") {
	}

	if err := s.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	// Clear must rotate onto a new, non-empty session id.
	if got := s.sess.ID(); got == old || got == "" {
		t.Fatalf("after Clear: session id = %q, want a fresh non-empty id (old=%q)", got, old)
	}

	// The old session must be ended and keep its persisted history.
	sessions, err := st.ListSessions(10)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	var found bool
	for _, m := range sessions {
		if m.ID != old {
			continue
		}
		found = true
		if m.EndedAt.IsZero() {
			t.Fatalf("old session %s not ended after Clear", old)
		}
		if msgs, err := st.LoadMessages(old); err != nil || len(msgs) == 0 {
			t.Fatalf("old session %s lost its history on Clear (len=%d err=%v)", old, len(msgs), err)
		}
	}
	if !found {
		t.Fatalf("old session %s not listed after Clear", old)
	}
}

func TestAuditSink_EndStatusReflectsError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "audit.jsonl")

	client := fakellm.New(fakellm.Script{Err: errors.New("boom")})
	s := newTestSession(t, client, chat.Config{})
	sink, cleanup, err := chat.OpenSink(out, nil)
	if err != nil {
		t.Fatalf("OpenSink: %v", err)
	}
	s.sink = sink
	s.sinkCleanup = cleanup
	sink.WriteStart("the prompt", "", "", out, false)

	for range s.Send(context.Background(), "hi") {
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var endStatus string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		if rec["kind"] == "end" {
			endStatus, _ = rec["status"].(string)
		}
	}
	if endStatus != "error" {
		t.Fatalf("audit end status = %q after an errored turn, want %q", endStatus, "error")
	}
}

func TestSession_Clear_RefreshesPrompt(t *testing.T) {
	client := fakellm.New()
	calls := 0
	cfg := chat.Config{RefreshPrompt: func() string {
		calls++
		return fmt.Sprintf("refreshed-%d", calls)
	}}
	cfg.Personality.SystemPrompt = "original"
	s := newTestSession(t, client, cfg)
	defer s.Close()

	s.Clear()
	if got := s.cfg.Personality.SystemPrompt; got != "refreshed-1" {
		t.Fatalf("after Clear: SystemPrompt = %q, want %q", got, "refreshed-1")
	}
}

func TestSession_Clear_NilRefreshIsNoop(t *testing.T) {
	client := fakellm.New()
	cfg := chat.Config{} // RefreshPrompt nil
	cfg.Personality.SystemPrompt = "frozen"
	s := newTestSession(t, client, cfg)
	defer s.Close()

	s.Clear()
	if got := s.cfg.Personality.SystemPrompt; got != "frozen" {
		t.Fatalf("after Clear with nil RefreshPrompt: SystemPrompt = %q, want %q", got, "frozen")
	}
}

func TestSession_Rollback(t *testing.T) {
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	if ok, err := s.Rollback(); ok || err != nil {
		t.Fatalf("Rollback on empty history should return (false, nil); got (%t, %v)", ok, err)
	}
	for range s.Send(context.Background(), "hi") {
	}
	if ok, err := s.Rollback(); !ok || err != nil {
		t.Fatalf("Rollback after a turn should return (true, nil); got (%t, %v)", ok, err)
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
			return chat.ActiveAgent{LLM: newClient, ModeLabel: name, ModelID: "m2", AgentKnobs: chat.AgentKnobs{ContextWindow: 1000}}, nil
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

// describerClient is a fakellm wrapper that also satisfies llm.ParamDescriber
// and llm.ParamSetter, so Snapshot's param population and SetParam's
// validate/push path can be exercised without a real adapter.
type describerClient struct {
	*fakellm.Client
	specs  []llm.ParamSpec
	gotSet *llm.RequestParams // last params pushed via SetParams
}

func (d *describerClient) ParamSpecs() []llm.ParamSpec { return d.specs }
func (d *describerClient) SetParams(p llm.RequestParams) {
	cp := p
	d.gotSet = &cp
}

func newDescriberClient(specs []llm.ParamSpec, scripts ...fakellm.Script) *describerClient {
	return &describerClient{Client: fakellm.New(scripts...), specs: specs}
}

// TestRoute_SetsIsCustomTool verifies route resolves IsCustomTool against the
// session's current CustomToolNames (translate stays pure, so route does it).
func TestRoute_SetsIsCustomTool(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{
		AgentKnobs: chat.AgentKnobs{CustomToolNames: map[string]bool{"my_tool": true}},
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
	if custom.Kind != ToolCall || custom.ToolName != "my_tool" || !custom.IsCustomTool {
		t.Fatalf("custom tool event = %+v, want IsCustomTool=true", custom)
	}
	if custom.ToolCallID != "1" {
		t.Fatalf("ToolCallID = %q, want 1", custom.ToolCallID)
	}
	builtin := <-got
	if builtin.IsCustomTool {
		t.Fatalf("builtin tool wrongly flagged custom: %+v", builtin)
	}
}

// TestSnapshot_PopulatesFromConfig verifies Snapshot mirrors cfg, including
// params from a ParamDescriber provider with currentParamValue mapping.
func TestSnapshot_PopulatesFromConfig(t *testing.T) {
	client := newDescriberClient([]llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"low", "high"}, Default: "low"},
		{Name: "max_tokens", Default: "0"},
	})
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
	if len(snap.Params) != 2 {
		t.Fatalf("Params count = %d, want 2", len(snap.Params))
	}
	re := snap.Params[0]
	if re.Name != "reasoning_effort" || re.Value != "high" || re.Default != "low" || len(re.Enum) != 2 {
		t.Fatalf("reasoning_effort param = %+v", re)
	}
	mt := snap.Params[1]
	if mt.Name != "max_tokens" || mt.Value != "512" {
		t.Fatalf("max_tokens param = %+v", mt)
	}
}

// TestSnapshot_NoDescriberHasNoParams verifies a provider that doesn't
// implement ParamDescriber yields an empty Params slice (no panic).
func TestSnapshot_NoDescriberHasNoParams(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{ModeLabel: "base"})
	defer s.Close()
	if got := s.Snapshot().Params; len(got) != 0 {
		t.Fatalf("Params = %v, want empty", got)
	}
}

// TestPrune verifies Prune stubs a matching tool result (ok=true) and reports
// ok=false for an unknown id.
func TestPrune(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()
	s.sess.SetMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "do it"},
		{Role: llm.RoleTool, Name: "bash", ToolCallID: "7", Content: "[tool_call_id=7]\nlots of bytes here"},
	})

	if summary, err := s.Prune("nope"); err == nil {
		t.Fatalf("Prune(unknown) err=nil summary=%q", summary)
	}

	summary, err := s.Prune("7")
	if err != nil {
		t.Fatalf("Prune(7): %v", err)
	}
	if summary == "" {
		t.Fatal("Prune returned empty summary")
	}
	// The stored tool result must now be the short stub, not the original.
	for _, m := range s.sess.Messages() {
		if m.Role == llm.RoleTool && m.ToolCallID == "7" {
			if !strings.Contains(m.Content, "pruned by user") {
				t.Fatalf("tool result not stubbed: %q", m.Content)
			}
		}
	}
}

// TestSetParam verifies the validate → SetByName → SetParams path, the
// reasoning_effort status-line refresh, and error cases.
func TestSetParam(t *testing.T) {
	client := newDescriberClient([]llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"low", "high"}, Default: "low"},
	})
	cfg := chat.Config{StatusLine: "openai │ gpt-x │ low"}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	if err := s.SetParam("reasoning_effort", "high"); err != nil {
		t.Fatalf("SetParam: %v", err)
	}
	if s.cfg.Params.ReasoningEffort != "high" {
		t.Fatalf("Params.ReasoningEffort = %q", s.cfg.Params.ReasoningEffort)
	}
	if client.gotSet == nil || client.gotSet.ReasoningEffort != "high" {
		t.Fatalf("SetParams not pushed to provider: %+v", client.gotSet)
	}
	if s.cfg.StatusLine != "openai │ gpt-x │ high" {
		t.Fatalf("status line not refreshed: %q", s.cfg.StatusLine)
	}
	// Snapshot must reflect the new value.
	if got := s.Snapshot().Params[0].Value; got != "high" {
		t.Fatalf("snapshot after SetParam = %q, want high", got)
	}

	// Validation failure (not in enum) must be reported and leave state unchanged.
	if err := s.SetParam("reasoning_effort", "bogus"); err == nil {
		t.Fatal("expected validation error for out-of-enum value")
	}
	// Unknown parameter for this provider.
	if err := s.SetParam("does_not_exist", "1"); err == nil {
		t.Fatal("expected error for unknown parameter")
	}
}

// TestSend_TextPath verifies Send drives a plain-text turn (tokens stream,
// Done fires, history carries).
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

// TestAuditSink_WritesStartEventsEnd verifies that when Spec.OutPath is set the
// Session opens a JSONL sink and writes a start line, every internal event
// (losslessly), and an end line on Close.
func TestAuditSink_WritesStartEventsEnd(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "audit.jsonl")

	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "hi"}}})
	cfg := chat.Config{StatusLine: "openai │ gpt-x", ModeLabel: "code"}
	s := newTestSession(t, client, cfg)
	// Wire the sink the way Start does (newTestSession bypasses Start).
	sink, cleanup, err := chat.OpenSink(out, nil)
	if err != nil {
		t.Fatalf("OpenSink: %v", err)
	}
	s.sink = sink
	s.sinkCleanup = cleanup
	_, model := chat.SplitStatus(cfg.StatusLine)
	sink.WriteStart("the prompt", cfg.ModeLabel, model, out, cfg.Headless)

	for range s.Send(context.Background(), "hi") {
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read audit log: %v", err)
	}
	var starts, ends, tokens int
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("bad JSONL line %q: %v", line, err)
		}
		switch rec["kind"] {
		case "start":
			starts++
			if rec["input"] != "the prompt" || rec["model"] != "gpt-x" {
				t.Fatalf("start line wrong: %v", rec)
			}
		case "end":
			ends++
		case "assistant_token":
			tokens++
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("start=%d end=%d, want 1/1", starts, ends)
	}
	if tokens == 0 {
		t.Fatal("expected at least one assistant_token line in the audit log")
	}
}

// TestSession_BusyEnforcement pins the runtime enforcement of the
// single-turn-at-a-time contract: while a turn is in flight, Send yields an
// immediate ErrBusy Error event (without starting a turn), and the
// between-turns mutators return ErrBusy. Draining the in-flight turn clears
// the gate.
func TestSession_BusyEnforcement(t *testing.T) {
	client := &blockingClient{entered: make(chan struct{}), returned: make(chan struct{})}
	s := newTestSession(t, client, chat.Config{})
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	out := s.Send(ctx, "hi")
	<-client.entered // the turn is now in flight inside Stream

	// Overlapping Send: one ErrBusy Error event, then close. No second turn.
	var rejected []Event
	for ev := range s.Send(context.Background(), "overlap") {
		rejected = append(rejected, ev)
	}
	if len(rejected) != 1 || rejected[0].Kind != Error || !errors.Is(rejected[0].Err, ErrBusy) {
		t.Fatalf("overlapping Send: want exactly one ErrBusy Error event, got %+v", rejected)
	}

	if err := s.Clear(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Clear while busy: want ErrBusy, got %v", err)
	}
	if _, err := s.Rollback(); !errors.Is(err, ErrBusy) {
		t.Fatalf("Rollback while busy: want ErrBusy, got %v", err)
	}
	if err := s.SwitchAgent("any"); !errors.Is(err, ErrBusy) {
		t.Fatalf("SwitchAgent while busy: want ErrBusy, got %v", err)
	}
	if _, err := s.Prune("1"); !errors.Is(err, ErrBusy) {
		t.Fatalf("Prune while busy: want ErrBusy, got %v", err)
	}
	if _, _, err := s.Compact(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatalf("Compact while busy: want ErrBusy, got %v", err)
	}

	// Drain the in-flight turn; the gate must clear.
	cancel()
	for range out {
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear after drain should succeed, got %v", err)
	}
}

// TestSession_InterjectMidTurn: Interject during a running turn surfaces as a
// SystemReminder event in that same turn, after the tool round.
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
		AgentKnobs: chat.AgentKnobs{CustomToolNames: map[string]bool{"poke": true}},
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

// TestSession_InterjectWhileIdle: Interject between turns is delivered at the
// start of the next Send.
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

// TestSession_SinkStartLabel pins the "(session <label>)" line written by
// Runtime.Session into the JSONL audit log and exercises the writeStartLine +
// cfg.OutPath plumbing for real. It creates a runtime-hosted session with an
// OutPath, runs one trivial fakellm turn, closes, then reads the file and
// asserts: first line is the start event with a "(session ...)" input, last
// line is the end event.
func TestSession_SinkStartLabel(t *testing.T) {
	outFile := filepath.Join(t.TempDir(), "audit.jsonl")
	rt := newTestRuntime(t, fakeCfg("hello"))
	s, err := rt.Session(SessionOpts{OutPath: outFile})
	if err != nil {
		t.Fatal(err)
	}
	// Run one turn so the sink has events in between start and end.
	for range s.Send(context.Background(), "ping") {
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("reading audit log: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines in audit log, got %d: %q", len(lines), string(data))
	}

	// First line: start event with a "(session ...)" label.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("parsing first line: %v (line=%q)", err, lines[0])
	}
	if got := first["kind"]; got != "start" {
		t.Fatalf("first line kind=%q, want %q", got, "start")
	}
	if got, _ := first["input"].(string); !strings.HasPrefix(got, "(session ") {
		t.Fatalf("first line input=%q, want a \"(session ...)\" label", got)
	}

	// Last line: end event.
	var last map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("parsing last line: %v (line=%q)", err, lines[len(lines)-1])
	}
	if got := last["kind"]; got != "end" {
		t.Fatalf("last line kind=%q, want %q", got, "end")
	}
}

// TestSessionJobsFromManager verifies that Session.Jobs() reads from the
// in-process job runtime.
func TestSessionJobsFromManager(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rt.jobs.startCommand(s, "sleep 1", t.TempDir(), []string{"sleep", "1"}, nil)
	jobs := s.Jobs()
	if len(jobs) != 1 || jobs[0].Kind != JobCommand {
		t.Fatalf("Session.Jobs = %+v, want one JobCommand", jobs)
	}
}

// TestTurnConfigHeadlessAsk: HeadlessAsk mirrors asker presence — true with no
// asker attached (subagents, shell3 run), false when a front-end supplied one.
func TestTurnConfigHeadlessAsk(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()

	s.asker = nil
	if !s.turnConfig().HeadlessAsk {
		t.Fatal("no asker: want HeadlessAsk=true")
	}
	s.asker = func(context.Context, string, string) bool { return true }
	if s.turnConfig().HeadlessAsk {
		t.Fatal("asker attached: want HeadlessAsk=false")
	}
}
