package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/patchapp"
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
func (f *fakeApp) SetStatus(msg string)            { f.record(fmt.Sprintf("SetStatus(%q)", msg)) }
func (f *fakeApp) SetStreamPreview(lines []string) { f.record(fmt.Sprintf("SetStreamPreview(%d)", len(lines))) }
func (f *fakeApp) SetTokens(n int)                 { f.record(fmt.Sprintf("SetTokens(%d)", n)) }
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
func runDrain(t *testing.T, events []patchapp.Event) ([]string, *llm.Usage) {
	t.Helper()
	app := &fakeApp{}
	usage := &llm.Usage{}
	cfg := &Config{}
	ch := make(chan patchapp.Event, len(events)+1)
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	drainTurn(ch, app, usage, cfg)
	return app.snapshot(), usage
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
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.ChunkEvent{Text: "hello "},
		patchapp.ChunkEvent{Text: "world"},
		patchapp.TurnDoneEvent{Usage: llm.Usage{TotalTokens: 42}},
	})

	// Each chunk triggers SetStreamPreview; TurnDone clears preview, prints
	// committed text, sets tokens, clears busy.
	if !containsAll(calls,
		"SetStreamPreview",
		"SetStreamPreview",
		"SetStreamPreview(0)", // cleared on done
		"Print(",              // committed render
		"SetTokens(42)",
		"SetBusy(false)",
	) {
		t.Fatalf("unexpected call sequence:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_AppendCommitsPendingStreamFirst(t *testing.T) {
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.ChunkEvent{Text: "thinking..."},
		patchapp.AppendEvent{Text: "tool output\n"},
		patchapp.TurnDoneEvent{},
	})

	// Order matters: stream chunk previews, then on Append we clear preview,
	// commit stream, then commit append.
	wantOrder := []string{
		"SetStreamPreview(1)", // chunk preview
		"SetStreamPreview(0)", // cleared before commit
		"Print(",              // committed stream
		"Print(",              // committed append
		"SetBusy(false)",
	}
	if !containsAll(calls, wantOrder...) {
		t.Fatalf("ordering wrong:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_DoneNoStream_NoCommit(t *testing.T) {
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.TurnDoneEvent{Usage: llm.Usage{TotalTokens: 7}},
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

func TestDrainTurn_DoneZeroTokens_SkipsSetTokens(t *testing.T) {
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.TurnDoneEvent{},
	})
	for _, c := range calls {
		if strings.HasPrefix(c, "SetTokens(") {
			t.Fatalf("expected no SetTokens for zero usage, got: %v", calls)
		}
	}
}

func TestDrainTurn_ErrorPrintsErrorLine(t *testing.T) {
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.ChunkEvent{Text: "partial"},
		patchapp.TurnErrEvent{Err: errors.New("boom")},
	})

	// Error path: clear preview, print buffered raw, print error line, clear busy.
	if !containsAll(calls, "SetStreamPreview(0)", "Print(", "PrintLine(", "SetBusy(false)") {
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
	calls, _ := runDrain(t, []patchapp.Event{
		patchapp.TurnErrEvent{Err: errors.New("context canceled")},
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

func TestDrainTurn_TTYExecReleasesAndReplies(t *testing.T) {
	app := &fakeApp{}
	usage := &llm.Usage{}
	cfg := &Config{}
	replyC := make(chan string, 1)
	ch := make(chan patchapp.Event, 2)
	ch <- patchapp.TTYExecEvent{Cmd: "true", ReplyC: replyC}
	close(ch)

	drainTurn(ch, app, usage, cfg)

	select {
	case got := <-replyC:
		if got == "" {
			t.Fatal("expected non-empty reply")
		}
	default:
		t.Fatal("ReplyC never received a value")
	}

	calls := app.snapshot()
	if !containsAll(calls, "WithReleasedTerminal:start", "WithReleasedTerminal:end") {
		t.Fatalf("missing terminal release: %v", calls)
	}
}

// ── pruneLastTurn ──────────────────────────────────────────────────────────────

func TestPruneLastTurn(t *testing.T) {
	mk := func(role llm.Role, content string) llm.Message {
		return llm.Message{Role: role, Content: content}
	}
	u, a := llm.RoleUser, llm.RoleAssistant

	cases := []struct {
		name string
		in   []llm.Message
		want int // expected length
	}{
		{"empty", nil, 0},
		{"only assistant", []llm.Message{mk(a, "x")}, 1},
		{"single turn", []llm.Message{mk(u, "q"), mk(a, "r")}, 0},
		{"two turns", []llm.Message{mk(u, "q1"), mk(a, "r1"), mk(u, "q2"), mk(a, "r2")}, 2},
		{"trailing user only", []llm.Message{mk(a, "r1"), mk(u, "q2")}, 1},
	}
	for _, tc := range cases {
		got := pruneLastTurn(tc.in)
		if len(got) != tc.want {
			t.Errorf("%s: got len %d, want %d", tc.name, len(got), tc.want)
		}
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
func (f *fakeSlashApp) PrintLine(line string) { f.record(fmt.Sprintf("PrintLine(%q)", line)) }
func (f *fakeSlashApp) SetStatus(msg string)  { f.record(fmt.Sprintf("SetStatus(%q)", msg)) }
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

// call invokes a registered handler; fails the test if unknown.
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

// register sets up a fakeSlashApp with the chat command set, returning
// it plus the cfg/sess/usage state the closures captured.
func register() (*fakeSlashApp, *Config, *session, *llm.Usage) {
	app := &fakeSlashApp{}
	cfg := &Config{
		StatusLine:  "anthropic │ claude-x",
		Personality: persona.Persona{SystemPrompt: "be helpful"},
	}
	sess := &session{}
	usage := &llm.Usage{}
	registerSlashCommands(app, cfg, sess, usage)
	return app, cfg, sess, usage
}

func TestSlash_RegistersExpectedCommands(t *testing.T) {
	app, _, _, _ := register()
	want := []string{"clear", "rollback", "prune", "model", "usage", "prompt", "truncate", "exit", "quit"}
	for _, name := range want {
		if _, ok := app.handlers[name]; !ok {
			t.Errorf("missing handler: /%s", name)
		}
	}
}

func TestSlash_Clear(t *testing.T) {
	app, _, sess, _ := register()
	sess.messages = []llm.Message{{Role: llm.RoleUser, Content: "x"}}
	app.call(t, "clear", "")

	if len(sess.messages) != 0 {
		t.Errorf("messages not cleared")
	}
	if !containsAll(app.snapshot(), "[context cleared]") {
		t.Errorf("missing dim: %v", app.snapshot())
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
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "q"},
		{Role: llm.RoleAssistant, Content: "r"},
	}
	app.call(t, "rollback", "")
	if len(sess.messages) != 0 {
		t.Errorf("expected rolled back, got %d msgs", len(sess.messages))
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
	sess.messages = []llm.Message{mkToolMsg("3", "bash", big)}
	app.call(t, "prune", "3")
	if !strings.Contains(sess.messages[0].Content, "[pruned by user") {
		t.Errorf("expected stub, got %q", sess.messages[0].Content)
	}
}

func TestSlash_ModelSwitches(t *testing.T) {
	app, cfg, _, _ := register()
	switched := ""
	cfg.ModelSwitcher = func(provider, name string) (LLMClient, error) {
		switched = provider + "/" + name
		return nil, nil
	}

	app.call(t, "model", "claude-y")

	if switched != "/claude-y" && !strings.HasSuffix(switched, "/claude-y") {
		t.Errorf("ModelSwitcher got %q, want suffix /claude-y", switched)
	}
	if !strings.Contains(cfg.StatusLine, "claude-y") {
		t.Errorf("status line not updated: %q", cfg.StatusLine)
	}
	if !containsAll(app.snapshot(), "SetStatus(", "[model: ") {
		t.Errorf("missing status update: %v", app.snapshot())
	}
}

func TestSlash_ModelMissingArg(t *testing.T) {
	app, _, _, _ := register()
	app.call(t, "model", "")
	if !containsAll(app.snapshot(), "[/model usage:") {
		t.Errorf("want usage hint: %v", app.snapshot())
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
		if strings.HasPrefix(c, "Print(") && strings.Contains(c, "system prompt:") {
			hasPrint = true
		}
	}
	if !hasPrint {
		t.Errorf("expected /prompt to print: %v", app.snapshot())
	}
}

func TestSlash_TruncateToggles(t *testing.T) {
	app, cfg, _, _ := register()
	cfg.Truncate = false
	app.call(t, "truncate", "")
	if !cfg.Truncate {
		t.Errorf("truncate not toggled on")
	}
	app.call(t, "truncate", "")
	if cfg.Truncate {
		t.Errorf("truncate not toggled off")
	}
}

// ── handlePruneToolResult ──────────────────────────────────────────────────────

func mkToolMsg(id, name, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: name, Content: content}
}

func TestPruneToolResult_Success(t *testing.T) {
	big := strings.Repeat("x", 600)
	a := []llm.Message{mkToolMsg("tc1", "bash", big)}
	b := []llm.Message{mkToolMsg("tc1", "bash", big)}

	out := handlePruneToolResult(`{"tool_call_id":"tc1","reason":"not needed"}`, a, b)
	if !strings.HasPrefix(out, "Pruned") {
		t.Fatalf("want success, got %q", out)
	}
	if !strings.Contains(a[0].Content, "[pruned:") || !strings.Contains(b[0].Content, "[pruned:") {
		t.Fatalf("content not updated in both slices: a=%q b=%q", a[0].Content, b[0].Content)
	}
}

func TestPruneToolResult_TooSmall(t *testing.T) {
	a := []llm.Message{mkToolMsg("tc1", "bash", "tiny output")}
	out := handlePruneToolResult(`{"tool_call_id":"tc1","reason":"x"}`, a)
	if !strings.Contains(out, "below") {
		t.Fatalf("expected size-gate refusal, got %q", out)
	}
	if !strings.Contains(a[0].Content, "tiny output") {
		t.Fatalf("content unexpectedly modified: %q", a[0].Content)
	}
}

func TestPruneToolResult_RefusesError(t *testing.T) {
	body := "error: " + strings.Repeat("y", 600)
	a := []llm.Message{mkToolMsg("tc1", "bash", body)}
	out := handlePruneToolResult(`{"tool_call_id":"tc1","reason":"x"}`, a)
	if !strings.Contains(out, "error") || !strings.Contains(out, "refusing") {
		t.Fatalf("expected error-gate refusal, got %q", out)
	}
	if a[0].Content != body {
		t.Fatalf("content unexpectedly modified")
	}
}

func TestPruneToolResult_MissingID(t *testing.T) {
	a := []llm.Message{mkToolMsg("tc1", "bash", strings.Repeat("z", 600))}
	out := handlePruneToolResult(`{"tool_call_id":"missing","reason":"x"}`, a)
	if !strings.Contains(out, "no tool result") {
		t.Fatalf("expected not-found error, got %q", out)
	}
}

func TestPruneToolResult_BadArgs(t *testing.T) {
	out := handlePruneToolResult(`{"reason":"x"}`)
	if !strings.Contains(out, "tool_call_id required") {
		t.Fatalf("expected required-arg error, got %q", out)
	}
}

func TestSlash_QuitAliasesExit(t *testing.T) {
	app, _, _, _ := register()
	if app.handlers["exit"] == nil || app.handlers["quit"] == nil {
		t.Errorf("exit/quit not both registered")
	}
}
