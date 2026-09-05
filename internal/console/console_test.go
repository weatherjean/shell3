package console

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestRunLineConversationEndToEnd(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "plain reply"}}})
	rt, err := shell3.NewConfiguredRuntime(context.Background(), t.TempDir(), nil, 1, nil,
		func(shell3.SessionOpts) (chat.Config, error) {
			return chat.Config{
				LLM: fake, WorkDir: t.TempDir(), ModelID: "fake",
				Profile: chat.AgentProfile{SystemPrompt: "test"},
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "console"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunWithReload(context.Background(), strings.NewReader("hello\n"), &out, rt, sess, inbox.Store{}, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); !strings.Contains(got, "you› ") || !strings.Contains(got, "shell3›\nplain reply") {
		t.Fatalf("transcript = %q", got)
	}
	calls := fake.CallsSnapshot()
	if len(calls) != 1 || calls[0].Msgs[len(calls[0].Msgs)-1].Content != "hello" {
		t.Fatalf("provider calls = %+v", calls)
	}
}

func TestRunHelpAliasesAreHostHandled(t *testing.T) {
	fake := fakellm.New()
	rt, err := shell3.NewConfiguredRuntime(context.Background(), t.TempDir(), nil, 1, nil,
		func(shell3.SessionOpts) (chat.Config, error) {
			return chat.Config{
				LLM: fake, WorkDir: t.TempDir(), ModelID: "fake",
				Profile: chat.AgentProfile{SystemPrompt: "test"},
			}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "console"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunWithReload(context.Background(), strings.NewReader("/\n/h\n/help\n/exit\n"), &out, rt, sess, inbox.Store{}, nil); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if strings.Count(got, "show this help") != 3 {
		t.Fatalf("help aliases did not all render help:\n%s", got)
	}
	for _, want := range []string{"Tell me what you can do", "Build a checked wrk workflow", "Esc cancels"} {
		if !strings.Contains(got, want) {
			t.Errorf("startup/help missing %q:\n%s", want, got)
		}
	}
	if calls := fake.CallsSnapshot(); len(calls) != 0 {
		t.Fatalf("help aliases made %d model calls", len(calls))
	}
}

func TestRunReloadIsHostHandled(t *testing.T) {
	fake := fakellm.New()
	rt, err := shell3.NewConfiguredRuntime(context.Background(), t.TempDir(), nil, 1, nil,
		func(shell3.SessionOpts) (chat.Config, error) {
			return chat.Config{LLM: fake, Profile: chat.AgentProfile{SystemPrompt: "test"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "console"})
	if err != nil {
		t.Fatal(err)
	}
	called := 0
	var out bytes.Buffer
	if err := RunWithReload(context.Background(), strings.NewReader("/reload\n/exit\n"), &out, rt, sess, inbox.Store{}, func() error {
		called++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if called != 1 || !strings.Contains(out.String(), "config reloaded") || len(fake.CallsSnapshot()) != 0 {
		t.Fatalf("called=%d output=%q model_calls=%d", called, out.String(), len(fake.CallsSnapshot()))
	}
}

type gatedConsoleLLM struct {
	mu      sync.Mutex
	calls   [][]llm.Message
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (g *gatedConsoleLLM) Stream(ctx context.Context, msgs []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	g.mu.Lock()
	g.calls = append(g.calls, append([]llm.Message(nil), msgs...))
	call := len(g.calls)
	g.mu.Unlock()
	if call == 1 {
		g.once.Do(func() { close(g.started) })
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-g.release:
		}
	}
	onEvent(llm.StreamEvent{TextDelta: fmt.Sprintf("reply %d", call)})
	return nil
}

func TestRunQueuesCLIInputUntilActiveTurnEnds(t *testing.T) {
	model := &gatedConsoleLLM{started: make(chan struct{}), release: make(chan struct{})}
	dir := t.TempDir()
	rt, err := shell3.NewConfiguredRuntime(context.Background(), dir, nil, 1, nil,
		func(shell3.SessionOpts) (chat.Config, error) {
			return chat.Config{LLM: model, WorkDir: dir, ModelID: "fake", Profile: chat.AgentProfile{SystemPrompt: "test"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "console"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- RunWithReload(context.Background(), strings.NewReader("first\nsecond\n/exit\n"), &out, rt, sess, inbox.Store{}, nil)
	}()
	<-model.started
	model.mu.Lock()
	if got := len(model.calls); got != 1 {
		model.mu.Unlock()
		t.Fatalf("calls while first turn active = %d", got)
	}
	model.mu.Unlock()
	close(model.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if len(model.calls) != 2 {
		t.Fatalf("calls = %d, want two sequential turns", len(model.calls))
	}
	if got := model.calls[1][len(model.calls[1])-1].Content; got != "second" {
		t.Fatalf("second prompt = %q", got)
	}
}

func TestRunInteractiveTurnEscapeCancels(t *testing.T) {
	input := &consoleInput{cancels: make(chan struct{}, 1)}
	input.cancels <- struct{}{}
	turnCanceled := make(chan struct{})
	start := func(ctx context.Context) <-chan shell3.Event {
		events := make(chan shell3.Event, 1)
		go func() {
			<-ctx.Done()
			close(turnCanceled)
			events <- shell3.Event{Kind: shell3.Error, Err: ctx.Err()}
			close(events)
		}()
		return events
	}
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runInteractiveTurn(ctx, &out, input, start, newTheme(strings.NewReader(""), &out)); !errors.Is(err, errTurnCancelled) {
		t.Fatalf("cancelled turn error = %v", err)
	}
	select {
	case <-turnCanceled:
	default:
		t.Fatal("Escape did not cancel the turn context")
	}
	if !strings.Contains(out.String(), "cancelling turn") {
		t.Fatalf("cancel feedback missing:\n%s", out.String())
	}
}

func TestRunReportsInboxWithoutStartingTurnOrInjectingContent(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ordinary reply"}}})
	dir := t.TempDir()
	rt, err := shell3.NewConfiguredRuntime(context.Background(), dir, nil, 1, nil,
		func(shell3.SessionOpts) (chat.Config, error) {
			return chat.Config{LLM: fake, WorkDir: dir, ModelID: "fake", Profile: chat.AgentProfile{SystemPrompt: "test"}}, nil
		})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "console"})
	if err != nil {
		t.Fatal(err)
	}
	store := inbox.Store{Root: t.TempDir()}
	for i := range 2 {
		body := fmt.Sprintf("private notice %d DEEP_BODY_MARKER", i)
		if _, err := store.Notify(inbox.Request{To: "main", Source: "test", Event: "large", Body: body}); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	if err := RunWithReload(context.Background(), strings.NewReader("hello\n/exit\n"), &out, rt, sess, store, nil); err != nil {
		t.Fatal(err)
	}
	if got := out.String(); strings.Count(got, "inbox · 2 pending · ask me to check it") != 2 {
		t.Fatalf("startup and per-message inbox status missing: %q", got)
	}
	calls := fake.CallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("provider calls = %d", len(calls))
	}
	content := calls[0].Msgs[len(calls[0].Msgs)-1].Content
	if content != "hello" || strings.Contains(content, "DEEP_BODY_MARKER") {
		t.Fatalf("model input = %q", content)
	}
	if _, total, err := store.List("main", inbox.StatusNew, 0, 20); err != nil || total != 2 {
		t.Fatalf("pending count=%d err=%v", total, err)
	}
}

func TestTurnRendererKeepsTranscriptCompact(t *testing.T) {
	var out bytes.Buffer
	r := turnRenderer{out: &out, theme: newTheme(strings.NewReader(""), &out)}
	r.event(shell3.Event{Kind: shell3.Reasoning, Text: "a very long private chain"})
	r.event(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: `{"command":"rg -n weather ."}`})
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	r.event(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: strings.Join(lines, "\n")})
	r.event(shell3.Event{Kind: shell3.Token, Text: "It is sunny."})
	r.event(shell3.Event{Kind: shell3.Done, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15})
	r.finish()
	got := out.String()
	for _, want := range []string{
		"… thinking", "→ bash: rg -n weather .", "← bash: line-00", "line-29", "shell3›\nIt is sunny.",
		"10 prompt + 5 completion = 15 tokens",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "private chain") {
		t.Fatalf("reasoning body leaked into compact transcript:\n%s", got)
	}
}

func TestTruncateResultBoundsLongSingleLine(t *testing.T) {
	got := truncateResult("sample_tool", strings.Repeat("x", maxResultRunes*2))
	if !strings.Contains(got, "characters omitted") {
		t.Fatalf("long output was not elided: %d runes", len([]rune(got)))
	}
	if len([]rune(got)) > maxResultRunes+80 {
		t.Fatalf("elided output is still too large: %d runes", len([]rune(got)))
	}
}

func TestTruncateResultUsesSmallerBashBudget(t *testing.T) {
	var lines []string
	for i := 0; i < 30; i++ {
		lines = append(lines, fmt.Sprintf("line-%02d", i))
	}
	got := truncateResult("bash", strings.Join(lines, "\n"))
	for _, want := range []string{"line-00", "line-29"} {
		if !strings.Contains(got, want) {
			t.Fatalf("bash output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\n") || len([]rune(got)) > maxBashResultRunes {
		t.Fatalf("bash output exceeded one-line budget:\n%s", got)
	}
}

func TestToolSummaryPrefersUsefulArgument(t *testing.T) {
	if got := toolSummary(`{"file_path":"notes.md","old_string":"x","new_string":"y"}`); got != "notes.md" {
		t.Fatalf("summary = %q", got)
	}
}

func TestThinkingAnimationParksCursorBelowMarker(t *testing.T) {
	var out bytes.Buffer
	r := turnRenderer{out: &out, theme: consoleTheme{tty: true}}
	r.startThinking()
	r.tickThinking()
	r.stopThinking()
	got := out.String()
	if !strings.Contains(got, "\n\x1b[1A\r\x1b[2K") {
		t.Fatalf("animation did not return from the line below: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[1A\r\x1b[2K") {
		t.Fatalf("stop did not reclaim the marker line: %q", got)
	}
}
