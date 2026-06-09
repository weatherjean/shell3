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
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolCallID: "3", ToolInput: `{"cmd":"ls"}`}, &Event{Kind: ToolCall, ToolName: "bash", ToolCallID: "3", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolCallID: "3", ToolOutput: "ok"}, &Event{Kind: ToolResult, ToolName: "bash", ToolCallID: "3", ToolOutput: "ok"}},
		{"system reminder", chat.Event{Kind: chat.EventSystemReminder, Text: "<system-reminder>\nmodel changed\n</system-reminder>"}, &Event{Kind: SystemReminder, Text: "<system-reminder>\nmodel changed\n</system-reminder>"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, &Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &chat.EventUsageData{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, &Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, &Event{Kind: Retry, Text: "retrying"}},
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
	// Isolate HOME: Start() resolves ~/.shell3 via os.UserHomeDir(), and
	// resolvePaths runs EnsureProject (which mints a project dir) before the
	// config load fails. Without this, the test writes an orphan project into
	// the developer's real ~/.shell3/projects/ on every run.
	t.Setenv("HOME", t.TempDir())
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

func TestAuditSink_EndStatusReflectsError(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "audit.jsonl")

	client := fakellm.New(fakellm.Script{Err: errors.New("boom")})
	s := newTestSession(t, client, chat.Config{})
	sink, cleanup, err := chat.OpenSink(out)
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
		CustomToolNames: map[string]bool{"my_tool": true},
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
		ModeLabel:     "code",
		StatusLine:    "openai │ gpt-x │ high",
		ProjectRef:    "ref-123",
		ContextWindow: 4096,
		ActiveSkills:  []string{"a", "b"},
		Params:        llm.RequestParams{ReasoningEffort: "high", MaxTokens: 512},
	}
	cfg.Personality.SystemPrompt = "be helpful"
	cfg.Personality.Tools = []llm.ToolDefinition{{Name: "bash", Description: "run a command"}}
	s := newTestSession(t, client, cfg)
	defer s.Close()

	snap := s.Snapshot()
	if snap.Agent != "code" || snap.Model != "gpt-x" || snap.ProjectRef != "ref-123" {
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
	if len(snap.Tools) != 1 || snap.Tools[0].Name != "bash" || snap.Tools[0].Description != "run a command" {
		t.Fatalf("Tools = %+v", snap.Tools)
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

// TestHistory_StripsToolPrefix verifies History returns plain roles and strips
// the internal "[tool_call_id=…]\n" prefix from tool-role content only.
func TestHistory_StripsToolPrefix(t *testing.T) {
	s := newTestSession(t, fakellm.New(), chat.Config{})
	defer s.Close()
	s.sess.SetMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
		{Role: llm.RoleTool, Name: "bash", ToolCallID: "1", Content: "[tool_call_id=1]\nthe output"},
	})

	h := s.History()
	if len(h) != 3 {
		t.Fatalf("History len = %d, want 3", len(h))
	}
	if h[0].Role != "user" || h[0].Content != "hello" {
		t.Fatalf("user entry = %+v", h[0])
	}
	if h[1].Role != "assistant" || h[1].Content != "hi" {
		t.Fatalf("assistant entry = %+v", h[1])
	}
	tool := h[2]
	if tool.Role != "tool" || tool.Content != "the output" {
		t.Fatalf("tool entry not prefix-stripped: %+v", tool)
	}
	if tool.ToolName != "bash" || tool.ToolCallID != "1" {
		t.Fatalf("tool entry metadata = %+v", tool)
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

	if summary, ok := s.Prune("nope"); ok {
		t.Fatalf("Prune(unknown) ok=true summary=%q", summary)
	}

	summary, ok := s.Prune("7")
	if !ok {
		t.Fatalf("Prune(7) ok=false summary=%q", summary)
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
	sink, cleanup, err := chat.OpenSink(out)
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
	if summary, ok := s.Prune("1"); ok || !strings.Contains(summary, ErrBusy.Error()) {
		t.Fatalf("Prune while busy: want busy error summary, got (%q, %t)", summary, ok)
	}

	// Drain the in-flight turn; the gate must clear.
	cancel()
	for range out {
	}
	if err := s.Clear(); err != nil {
		t.Fatalf("Clear after drain should succeed, got %v", err)
	}
}
