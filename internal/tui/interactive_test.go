package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/persona"
)

// fakeApp records calls to the appView interface for assertion.
// All methods are safe to call from any goroutine.
type fakeApp struct {
	mu       sync.Mutex
	calls    []string
	released bool
	cancel   context.CancelFunc
}

func (f *fakeApp) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeApp) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeApp) Print(lines []string) {
	f.record(fmt.Sprintf("Print(%d:%q)", len(lines), strings.Join(lines, "|")))
}
func (f *fakeApp) PrintLine(line string) {
	f.record(fmt.Sprintf("PrintLine(%q)", line))
}
func (f *fakeApp) Refresh() { f.record("Refresh") }
func (f *fakeApp) SetBusy(busy bool, cancel context.CancelFunc) {
	f.record(fmt.Sprintf("SetBusy(%v)", busy))
	f.mu.Lock()
	f.cancel = cancel
	f.mu.Unlock()
}
func (f *fakeApp) SetStatus(msg string)   { f.record(fmt.Sprintf("SetStatus(%q)", msg)) }
func (f *fakeApp) SetTokens(n int)        { f.record(fmt.Sprintf("SetTokens(%d)", n)) }
func (f *fakeApp) SetContextWindow(n int) { f.record(fmt.Sprintf("SetContextWindow(%d)", n)) }
func (f *fakeApp) WithReleasedTerminal(fn func()) {
	f.record("WithReleasedTerminal:start")
	f.mu.Lock()
	f.released = true
	f.mu.Unlock()
	fn()
	f.mu.Lock()
	f.released = false
	f.mu.Unlock()
	f.record("WithReleasedTerminal:end")
}

// Compile-time assertion that fakeApp satisfies patchapp.AppView.
var _ patchapp.AppView = (*fakeApp)(nil)

// runDrain feeds events to drainTurn synchronously and returns the recorded
// call list. The events slice is drained in order; the channel is closed
// after the last event.
func runDrain(t *testing.T, events []chat.Event) ([]string, *llm.Usage) {
	t.Helper()
	app := &fakeApp{}
	usage := &llm.Usage{}
	cfg := &chat.Config{}
	ch := make(chan chat.Event, len(events)+1)
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	drainTurn(ch, app, usage, cfg, nil)
	return app.snapshot(), usage
}

// usageData is a convenience for tests building EventUsage/EventTurnDone events.
func usageData(p, c, total int) *chat.EventUsageData {
	return &chat.EventUsageData{PromptTokens: p, CompletionTokens: c, TotalTokens: total}
}

// containsAll checks that every needle appears in the haystack in order.
func containsAll(haystack []string, needles ...string) bool {
	i := 0
	for _, h := range haystack {
		if i < len(needles) && strings.Contains(h, needles[i]) {
			i++
		}
	}
	return i == len(needles)
}

func TestDrainTurn_ChunkOnly_StreamsAndCommits(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventAssistantToken, Text: "hello "},
		{Kind: chat.EventAssistantToken, Text: "world"},
		{Kind: chat.EventTurnDone, Usage: usageData(0, 0, 42)},
	})

	if !containsAll(calls,
		"Print(",
		"SetTokens(42)",
		"SetBusy(false)",
	) {
		t.Fatalf("unexpected call sequence:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_UsageEventUpdatesTokensBeforeDone(t *testing.T) {
	calls, usage := runDrain(t, []chat.Event{
		{Kind: chat.EventUsage, Usage: usageData(3, 4, 7)},
		{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "tool output\n"},
		{Kind: chat.EventUsage, Usage: usageData(13, 9, 22)},
		{Kind: chat.EventTurnDone, Usage: usageData(13, 9, 22)},
	})

	if !containsAll(calls,
		"SetTokens(7)",
		"Print(",
		"SetTokens(22)",
		"SetBusy(false)",
	) {
		t.Fatalf("usage event ordering wrong:\n%s", strings.Join(calls, "\n"))
	}
	if usage.TotalTokens != 22 || usage.PromptTokens != 13 || usage.CompletionTokens != 9 {
		t.Fatalf("unexpected usage: %+v", usage)
	}
}

func TestDrainTurn_ToolResultCommitsPendingStreamFirst(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventAssistantToken, Text: "thinking..."},
		{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "tool output\n"},
		{Kind: chat.EventTurnDone},
	})

	wantOrder := []string{
		"Print(",
		"Print(",
		"SetBusy(false)",
	}
	if !containsAll(calls, wantOrder...) {
		t.Fatalf("ordering wrong:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_DoneNoStream_NoCommit(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventTurnDone, Usage: usageData(0, 0, 7)},
	})

	for _, c := range calls {
		if strings.HasPrefix(c, "Print(") {
			t.Fatalf("expected no Print on empty turn, got: %v", calls)
		}
	}
	if !containsAll(calls, "SetTokens(7)", "SetBusy(false)") {
		t.Fatalf("missing tokens/busy: %v", calls)
	}
}

func TestDrainTurn_NewlineCommitsLineMidStream(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventAssistantToken, Text: "first line\n"},
		{Kind: chat.EventAssistantToken, Text: "second line\n"},
		{Kind: chat.EventTurnDone},
	})

	prints := 0
	for _, c := range calls {
		if strings.HasPrefix(c, "Print(") {
			prints++
		}
	}
	if prints < 2 {
		t.Fatalf("expected at least 2 Print calls (one per completed line), got %d:\n%s", prints, strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_FencedCodeBlockIsPrintedVerbatim(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventAssistantToken, Text: "```python\n# this is a comment\nprint('hi')\n```\n"},
		{Kind: chat.EventTurnDone},
	})

	foundComment := false
	for _, c := range calls {
		if strings.Contains(c, "# this is a comment") && !strings.Contains(c, "\x1b[1m") {
			foundComment = true
		}
	}
	if !foundComment {
		t.Fatalf("expected verbatim '# this is a comment' line, got:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_DoneZeroTokens_SkipsSetTokens(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventTurnDone},
	})
	for _, c := range calls {
		if strings.HasPrefix(c, "SetTokens(") {
			t.Fatalf("expected no SetTokens for zero usage, got: %v", calls)
		}
	}
}

func TestDrainTurn_ErrorPrintsErrorLine(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventAssistantToken, Text: "partial"},
		{Kind: chat.EventError, Text: "boom"},
	})

	if !containsAll(calls, "Print(", "PrintLine(", "SetBusy(false)") {
		t.Fatalf("error path missing steps: %v", calls)
	}
	hasError := false
	for _, c := range calls {
		if strings.Contains(c, "[error: boom]") {
			hasError = true
		}
	}
	if !hasError {
		t.Fatalf("error message not found: %v", calls)
	}
}

func TestDrainTurn_CancelMessageIsDimmed(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventError, Text: "context canceled"},
	})

	hasCancel, hasError := false, false
	for _, c := range calls {
		if strings.Contains(c, "[cancelled]") {
			hasCancel = true
		}
		if strings.Contains(c, "[error:") {
			hasError = true
		}
	}
	if !hasCancel || hasError {
		t.Fatalf("expected [cancelled] only, got: %v", calls)
	}
}

func TestDrainTurn_RetryPrintsDimNoticeWithoutClearingBusy(t *testing.T) {
	calls, _ := runDrain(t, []chat.Event{
		{Kind: chat.EventRetry, Text: "stream failed (HTTP 503), retrying (2/5)"},
		{Kind: chat.EventTurnDone},
	})

	found := false
	busyClears := 0
	for _, c := range calls {
		if strings.Contains(c, "⟳") && strings.Contains(c, "retrying (2/5)") {
			found = true
		}
		if strings.Contains(c, "SetBusy(false)") {
			busyClears++
		}
	}
	if !found {
		t.Fatalf("retry notice not rendered with ⟳ glyph: %v", calls)
	}
	// The retry event must not clear busy; only EventTurnDone does.
	if busyClears != 1 {
		t.Fatalf("expected exactly one SetBusy(false) (from TurnDone), got %d: %v", busyClears, calls)
	}
}

// TestShellInteractive_CallbackInvoked exercises a stub Config.ShellInteractive
// to confirm the callback shape: turn-side code invokes the func and uses its
// return value as tool output. This replaces the previous TTYExecEvent-based
// drainTurn test, which is obsolete now that the TTY round-trip is a direct
// callback rather than an event-channel handshake.
func TestShellInteractive_CallbackInvoked(t *testing.T) {
	called := false
	var gotCmd, gotWd string
	cb := func(ctx context.Context, cmd, workdir string) string {
		called = true
		gotCmd = cmd
		gotWd = workdir
		return "ok-result"
	}
	out := cb(context.Background(), "true", "/tmp")
	if !called {
		t.Fatal("callback not invoked")
	}
	if gotCmd != "true" || gotWd != "/tmp" {
		t.Fatalf("unexpected args: cmd=%q wd=%q", gotCmd, gotWd)
	}
	if out != "ok-result" {
		t.Fatalf("unexpected return: %q", out)
	}
}

// ── handleSlash ────────────────────────────────────────────────────────────────

// fakeSlashApp captures slash command registrations without running a real
// patchapp.App. Each registered handler is exposed via call(name, args)
// for tests; Print/PrintLine/SetStatus calls are recorded.
type fakeSlashApp struct {
	mu       sync.Mutex
	calls    []string
	handlers map[string]patchapp.SlashHandler
}

func (f *fakeSlashApp) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func (f *fakeSlashApp) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

func (f *fakeSlashApp) Print(lines []string) {
	f.record(fmt.Sprintf("Print(%d:%q)", len(lines), strings.Join(lines, "|")))
}
func (f *fakeSlashApp) PrintLine(line string)          { f.record(fmt.Sprintf("PrintLine(%q)", line)) }
func (f *fakeSlashApp) SetStatus(msg string)           { f.record(fmt.Sprintf("SetStatus(%q)", msg)) }
func (f *fakeSlashApp) SetContextWindow(n int)         { f.record(fmt.Sprintf("SetContextWindow(%d)", n)) }
func (f *fakeSlashApp) Quit()                          { f.record("Quit") }
func (f *fakeSlashApp) WithReleasedTerminal(fn func()) { fn() }
func (f *fakeSlashApp) RegisterSlash(cmd patchapp.SlashCommand) {
	if f.handlers == nil {
		f.handlers = make(map[string]patchapp.SlashHandler)
	}
	f.handlers[cmd.Name] = cmd.Handler
	for _, a := range cmd.Aliases {
		f.handlers[a] = cmd.Handler
	}
}

func (f *fakeSlashApp) call(t *testing.T, name, args string) {
	t.Helper()
	h, ok := f.handlers[name]
	if !ok {
		t.Fatalf("no handler registered for %q (have %v)", name, f.handlerNames())
	}
	h(args)
}

func (f *fakeSlashApp) handlerNames() []string {
	out := make([]string, 0, len(f.handlers))
	for k := range f.handlers {
		out = append(out, k)
	}
	return out
}

// fakeLLM is a no-op chat.LLMClient used as the switched-in client in /agent
// tests; identity is checked by pointer comparison.
type fakeLLM struct{ id string }

func (f *fakeLLM) Stream(context.Context, []llm.Message, []llm.ToolDefinition, func(llm.StreamEvent)) error {
	return nil
}

// register sets up a fakeSlashApp with the chat command set, returning
// it plus the cfg/sess/usage state the closures captured. The config is
// seeded with two agents and a stub SwitchAgent so /agent tests can run.
func register() (*fakeSlashApp, *chat.Config, *chat.Session, *llm.Usage) {
	app := &fakeSlashApp{}
	cfg := &chat.Config{
		StatusLine:  "anthropic │ claude-x",
		ModeLabel:   "main",
		Personality: persona.Persona{SystemPrompt: "be helpful"},
		AgentNames:  []string{"main", "fast"},
		SwitchAgent: func(name string) (chat.ActiveAgent, error) {
			agents := map[string]chat.ActiveAgent{
				"main": {
					LLM:           &fakeLLM{id: "claude-x"},
					Params:        llm.RequestParams{ReasoningEffort: "high"},
					ModelID:       "claude-x",
					ModeLabel:     "main",
					ContextWindow: 200000,
				},
				"fast": {
					LLM:           &fakeLLM{id: "claude-fast"},
					Params:        llm.RequestParams{ReasoningEffort: "high"},
					ModelID:       "claude-fast",
					ModeLabel:     "fast",
					ContextWindow: 64000,
				},
			}
			if a, ok := agents[name]; ok {
				return a, nil
			}
			return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
		},
	}
	sess := chat.NewSession(chat.SessionOpts{BufSize: 8})
	usage := &llm.Usage{}
	registerSlashCommands(app, cfg, sess, usage, func(llm.Message) {}, func(chat.ActiveAgent) {})
	return app, cfg, sess, usage
}

func mkToolMsg(id, name, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: name, Content: content}
}

func TestSlash_RegistersExpectedCommands(t *testing.T) {
	app, _, _, _ := register()
	want := []string{"clear", "rollback", "prune", "usage", "prompt", "print", "agent", "exit", "quit", "image"}
	for _, name := range want {
		if _, ok := app.handlers[name]; !ok {
			t.Errorf("missing handler: /%s", name)
		}
	}
}

func TestSlash_Clear(t *testing.T) {
	app, _, sess, _ := register()
	sess.SetMessages([]llm.Message{{Role: llm.RoleUser, Content: "x"}})
	app.call(t, "clear", "")

	if len(sess.Messages()) != 0 {
		t.Errorf("messages not cleared")
	}
	if !containsAll(app.snapshot(), "[context cleared]") {
		t.Errorf("missing dim: %v", app.snapshot())
	}
}

func TestSlash_ClearRefreshesPrompt(t *testing.T) {
	app, cfg, _, _ := register()
	cfg.Personality.SystemPrompt = "stale boot-time prompt"
	cfg.RefreshPrompt = func() string { return "fresh prompt with new time" }

	app.call(t, "clear", "")

	if cfg.Personality.SystemPrompt != "fresh prompt with new time" {
		t.Errorf("clear did not refresh system prompt: %q", cfg.Personality.SystemPrompt)
	}
}

func TestSlash_ClearNilRefreshIsNoop(t *testing.T) {
	app, cfg, _, _ := register()
	cfg.Personality.SystemPrompt = "original"
	cfg.RefreshPrompt = nil // run.go may not wire it (e.g. embedders)

	app.call(t, "clear", "")

	if cfg.Personality.SystemPrompt != "original" {
		t.Errorf("nil RefreshPrompt should leave prompt untouched, got %q", cfg.Personality.SystemPrompt)
	}
}

func TestSlash_RollbackEmpty(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "rollback", "")
	if !containsAll(app.snapshot(), "[nothing to roll back]") {
		t.Errorf("want nothing-to-roll-back: %v", app.snapshot())
	}
}

func TestSlash_RollbackRemovesTurn(t *testing.T) {
	app, _, sess, _ := register()
	sess.SetMessages([]llm.Message{
		{Role: llm.RoleUser, Content: "q"},
		{Role: llm.RoleAssistant, Content: "r"},
	})
	app.call(t, "rollback", "")
	if len(sess.Messages()) != 0 {
		t.Errorf("expected rolled back, got %d msgs", len(sess.Messages()))
	}
}

func TestSlash_PruneNoArg(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "prune", "")
	if !containsAll(app.snapshot(), "/prune usage") {
		t.Errorf("want usage hint: %v", app.snapshot())
	}
}

func TestSlash_PruneByID(t *testing.T) {
	app, _, sess, _ := register()
	big := strings.Repeat("x", 600)
	sess.SetMessages([]llm.Message{mkToolMsg("3", "bash", big)})
	app.call(t, "prune", "3")
	msgs := sess.Messages()
	if !strings.Contains(msgs[0].Content, "[pruned by user") {
		t.Errorf("expected stub, got %q", msgs[0].Content)
	}
}

func TestSlash_UsageNoData(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "usage", "")
	if !containsAll(app.snapshot(), "[no usage data yet]") {
		t.Errorf("want no-data: %v", app.snapshot())
	}
}

func TestSlash_UsageWithData(t *testing.T) {
	app, _, _, usage := register()
	*usage = llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}
	app.call(t, "usage", "")

	hasPrint := false
	for _, c := range app.snapshot() {
		if strings.HasPrefix(c, "Print(") && strings.Contains(c, "prompt:") {
			hasPrint = true
		}
	}
	if !hasPrint {
		t.Errorf("expected usage print: %v", app.snapshot())
	}
}

func TestSlash_PromptDumps(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "prompt", "")
	hasPrint := false
	for _, c := range app.snapshot() {
		if strings.HasPrefix(c, "Print(") && strings.Contains(c, "System prompt") {
			hasPrint = true
		}
	}
	if !hasPrint {
		t.Errorf("expected /prompt to print: %v", app.snapshot())
	}
}

func TestSlash_PrintNoArg(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "print", "")
	if !containsAll(app.snapshot(), "/print usage") {
		t.Errorf("want usage hint: %v", app.snapshot())
	}
}

func TestSlash_PrintByID(t *testing.T) {
	app, _, sess, _ := register()
	// 15 lines so it exceeds the inline 10-line truncation cap; a tail marker
	// on the last line proves /print shows the full, untruncated output. The
	// [tool_call_id=…] prefix must be stripped from the display.
	body := strings.Repeat("filler\n", 14) + "TAILMARKER"
	sess.SetMessages([]llm.Message{mkToolMsg("3", "bash", "[tool_call_id=3]\n"+body)})
	app.call(t, "print", "3")

	var printed string
	for _, c := range app.snapshot() {
		if strings.HasPrefix(c, "Print(") {
			printed = c
		}
	}
	if !strings.Contains(printed, "TAILMARKER") {
		t.Errorf("expected full untruncated output, got: %q", printed)
	}
	if strings.Contains(printed, "tool_call_id") {
		t.Errorf("expected [tool_call_id=…] prefix stripped, got: %q", printed)
	}
}

func TestSlash_PrintUnknownID(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "print", "999")
	if !containsAll(app.snapshot(), "no tool result with id") {
		t.Errorf("want not-found hint: %v", app.snapshot())
	}
}

func TestSlash_AgentList(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "agent", "")

	var printed string
	for _, c := range app.snapshot() {
		if strings.HasPrefix(c, "Print(") {
			printed = c
		}
	}
	if printed == "" {
		t.Fatalf("expected a Print listing agents: %v", app.snapshot())
	}
	// Both agent names listed, and the active agent marked.
	if !strings.Contains(printed, "main") || !strings.Contains(printed, "fast") {
		t.Errorf("agent names missing from list: %q", printed)
	}
	if !strings.Contains(printed, "(active)") {
		t.Errorf("active marker missing: %q", printed)
	}
}

func TestSlash_AgentSwitch(t *testing.T) {
	app := &fakeSlashApp{}
	cfg := &chat.Config{
		StatusLine: "anthropic │ claude-x",
		ModeLabel:  "main",
		AgentNames: []string{"main", "fast"},
		SwitchAgent: func(name string) (chat.ActiveAgent, error) {
			if name == "fast" {
				return chat.ActiveAgent{
					LLM:           &fakeLLM{id: "claude-fast"},
					Params:        llm.RequestParams{ReasoningEffort: "high"},
					ModelID:       "claude-fast",
					ModeLabel:     "fast",
					ContextWindow: 64000,
				}, nil
			}
			return chat.ActiveAgent{}, fmt.Errorf("unknown agent %q", name)
		},
	}
	sess := chat.NewSession(chat.SessionOpts{BufSize: 8})
	var applied chat.ActiveAgent
	registerSlashCommands(app, cfg, sess, &llm.Usage{}, func(llm.Message) {}, func(rt chat.ActiveAgent) {
		applied = rt
		cfg.LLM = rt.LLM
		cfg.ModeLabel = rt.ModeLabel
		cfg.ContextWindow = rt.ContextWindow
		cfg.Params = rt.Params
		cfg.StatusLine = fmt.Sprintf("%s │ %s", rt.ModeLabel, rt.ModelID)
	})
	app.call(t, "agent", "fast")

	if applied.ModelID != "claude-fast" {
		t.Errorf("applyAgent not called with fast agent: %+v", applied)
	}
	if !containsAll(app.snapshot(), "[agent: fast]") {
		t.Errorf("missing agent confirm: %v", app.snapshot())
	}
}

func TestSlash_AgentUnknown(t *testing.T) {
	app, cfg, _, _ := register()
	before := cfg.StatusLine
	app.call(t, "agent", "bogus")

	if cfg.StatusLine != before {
		t.Errorf("status changed on unknown agent: %q", cfg.StatusLine)
	}
	if !containsAll(app.snapshot(), "unknown agent") {
		t.Errorf("missing unknown-agent error: %v", app.snapshot())
	}
}

func TestSlash_AgentNoneConfigured(t *testing.T) {
	app := &fakeSlashApp{}
	cfg := &chat.Config{StatusLine: "anthropic │ claude-x"}
	sess := chat.NewSession(chat.SessionOpts{BufSize: 8})
	registerSlashCommands(app, cfg, sess, &llm.Usage{}, func(llm.Message) {}, func(chat.ActiveAgent) {})
	app.call(t, "agent", "fast")

	if !containsAll(app.snapshot(), "[no agents configured]") {
		t.Errorf("want no-agents message: %v", app.snapshot())
	}
}

func TestSlash_QuitAliasesExit(t *testing.T) {
	app, _, _, _ := register()
	if app.handlers["exit"] == nil || app.handlers["quit"] == nil {
		t.Errorf("exit/quit not both registered")
	}
}
