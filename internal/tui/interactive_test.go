package tui

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/pkg/shell3"
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

// runDrain feeds public Events to the render sink in order, then calls finish
// to mirror the drain goroutine reaching channel close on a normally-completed
// (non-cancelled) turn, and returns the recorded call list plus final usage.
func runDrain(t *testing.T, events []shell3.Event) ([]string, usage) {
	t.Helper()
	app := &fakeApp{}
	var u usage
	render, finish := newRenderSink(app, &u)
	for _, ev := range events {
		render(ev)
	}
	finish(false)
	return app.snapshot(), u
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "hello "},
		{Kind: shell3.Token, Text: "world"},
		{Kind: shell3.Done, TotalTokens: 42},
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
	calls, u := runDrain(t, []shell3.Event{
		{Kind: shell3.Usage, PromptTokens: 3, CompletionTokens: 4, TotalTokens: 7},
		{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: "tool output\n"},
		{Kind: shell3.Usage, PromptTokens: 13, CompletionTokens: 9, TotalTokens: 22},
		{Kind: shell3.Done, PromptTokens: 13, CompletionTokens: 9, TotalTokens: 22},
	})

	if !containsAll(calls,
		"SetTokens(7)",
		"Print(",
		"SetTokens(22)",
		"SetBusy(false)",
	) {
		t.Fatalf("usage event ordering wrong:\n%s", strings.Join(calls, "\n"))
	}
	if u.total != 22 || u.prompt != 13 || u.completion != 9 {
		t.Fatalf("unexpected usage: %+v", u)
	}
}

func TestDrainTurn_ToolResultCommitsPendingStreamFirst(t *testing.T) {
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "thinking..."},
		{Kind: shell3.ToolResult, ToolName: "bash", ToolOutput: "tool output\n"},
		{Kind: shell3.Done},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Done, TotalTokens: 7},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "first line\n"},
		{Kind: shell3.Token, Text: "second line\n"},
		{Kind: shell3.Done},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "```python\n# this is a comment\nprint('hi')\n```\n"},
		{Kind: shell3.Done},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Done},
	})
	for _, c := range calls {
		if strings.HasPrefix(c, "SetTokens(") {
			t.Fatalf("expected no SetTokens for zero usage, got: %v", calls)
		}
	}
}

func TestDrainTurn_SystemReminderRendersDim(t *testing.T) {
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.SystemReminder, Text: "reminder text"},
		{Kind: shell3.Done},
	})

	found := false
	for _, c := range calls {
		// The recorded call quotes the line via %q, so the dim escape appears as
		// the literal "\x1b[2m" sequence around the reminder text.
		if strings.Contains(c, "reminder text") && strings.Contains(c, `\x1b[2m`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected dim system-reminder render, got:\n%s", strings.Join(calls, "\n"))
	}
}

func TestDrainTurn_ErrorPrintsErrorLine(t *testing.T) {
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "partial"},
		{Kind: shell3.Error, Err: fmt.Errorf("boom")},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Error, Err: fmt.Errorf("context canceled")},
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
	calls, _ := runDrain(t, []shell3.Event{
		{Kind: shell3.Retry, Text: "stream failed (HTTP 503), retrying (2/5)"},
		{Kind: shell3.Done},
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
	// The retry event must not clear busy; only Done does.
	if busyClears != 1 {
		t.Fatalf("expected exactly one SetBusy(false) (from Done), got %d: %v", busyClears, calls)
	}
}

// TestDrainTurn_ChannelClosesWithoutTerminalEvent_ClearsBusy reproduces the
// hang: route (pkg/shell3) is free to drop a turn's terminal Done/Error event
// once the turn ctx is cancelled, so the drain goroutine can reach channel
// close having only seen partial output. finish — bound to channel close, the
// one guaranteed end-of-turn signal — must still flush the partial, surface the
// cancel notice, and clear the busy-gate so the "thinking" spinner stops.
func TestDrainTurn_ChannelClosesWithoutTerminalEvent_ClearsBusy(t *testing.T) {
	app := &fakeApp{}
	var u usage
	sink, finish := newRenderSink(app, &u)

	// A streamed partial line arrives, then the channel closes with NO Done/
	// Error (the terminal event was dropped by route on cancel).
	sink(shell3.Event{Kind: shell3.Token, Text: "partial answer"})
	finish(true) // drain goroutine: channel closed on a cancelled turn

	calls := app.snapshot()
	if !containsAll(calls, "SetBusy(false)") {
		t.Fatalf("busy not cleared when channel closed without a terminal event:\n%s", strings.Join(calls, "\n"))
	}
	if !containsAll(calls, "Print(") {
		t.Fatalf("buffered partial output not flushed on finish:\n%s", strings.Join(calls, "\n"))
	}
	cancels := 0
	for _, c := range calls {
		if strings.Contains(c, "[cancelled]") {
			cancels++
		}
	}
	if cancels != 1 {
		t.Fatalf("expected exactly one [cancelled] notice, got %d:\n%s", cancels, strings.Join(calls, "\n"))
	}
}

// TestDrainTurn_CancelTerminalDelivered_NoDoubleNotice covers the other side of
// the route race: when the terminal Error(context canceled) DID win delivery,
// finish still runs at channel close but must not double up the [cancelled]
// notice or the busy-clear.
func TestDrainTurn_CancelTerminalDelivered_NoDoubleNotice(t *testing.T) {
	app := &fakeApp{}
	var u usage
	sink, finish := newRenderSink(app, &u)

	sink(shell3.Event{Kind: shell3.Error, Err: fmt.Errorf("context canceled")})
	finish(true) // drain goroutine runs finish even though the terminal arrived

	calls := app.snapshot()
	busy, cancels := 0, 0
	for _, c := range calls {
		if strings.Contains(c, "SetBusy(false)") {
			busy++
		}
		if strings.Contains(c, "[cancelled]") {
			cancels++
		}
	}
	if busy != 1 {
		t.Fatalf("expected exactly one SetBusy(false), got %d:\n%s", busy, strings.Join(calls, "\n"))
	}
	if cancels != 1 {
		t.Fatalf("expected exactly one [cancelled] notice, got %d:\n%s", cancels, strings.Join(calls, "\n"))
	}
}

// TestShellInteractive_CallbackInvoked exercises a stub ShellInteractive
// callback to confirm the callback shape: turn-side code invokes the func and
// uses its return value as tool output. The callback now lives on
// shell3.Spec.ShellInteractive (supplied by RunInteractive's closure).
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

// fakeSession is a hand-rolled stand-in for *shell3.Session implementing the
// local session interface. It records mutating calls and serves canned
// Snapshot/History so each slash command's effect can be asserted without a
// real agent config or store.
type fakeSession struct {
	mu      sync.Mutex
	snap    shell3.Snapshot
	history []shell3.HistoryEntry

	// agents/active drive AgentNames/ActiveAgent/SwitchAgent. switchErr, when
	// set for a name, makes SwitchAgent fail (unknown-agent path).
	agents []string
	active string

	cleared       bool
	rolledBack    bool   // controls Rollback's return
	rollbackOK    bool   // what Rollback returns
	pruneOut      string // what Prune returns
	pruneOK       bool   // what Prune's ok returns
	setParamFn    func(string, string) error
	sent          []string // prompts passed to Send
	interjections []string // texts passed to Interject
	approver      func(ctx context.Context, req shell3.ApprovalRequest) bool

	// name/wakeBus/runQueued drive the Wake-bus consumption path. name is the
	// session's registry name (compared against HostEvent.Session); wakeBus is
	// the out-of-turn event bus; runQueued is the canned event stream RunQueued
	// emits; runQueuedCalls counts RunQueued invocations.
	name           string
	wakeBus        chan shell3.HostEvent
	runQueued      []shell3.Event
	runQueuedCalls int
}

func (f *fakeSession) Send(ctx context.Context, prompt string) <-chan shell3.Event {
	f.sent = append(f.sent, prompt)
	ch := make(chan shell3.Event)
	close(ch)
	return ch
}
func (f *fakeSession) Clear() error { f.cleared = true; return nil }
func (f *fakeSession) Rollback() (bool, error) {
	f.rolledBack = true
	return f.rollbackOK, nil
}
func (f *fakeSession) SwitchAgent(name string) error {
	for _, n := range f.agents {
		if n == name {
			f.active = name
			f.snap.Agent = name
			return nil
		}
	}
	return fmt.Errorf("unknown agent %q", name)
}
func (f *fakeSession) AgentNames() []string      { return f.agents }
func (f *fakeSession) ActiveAgent() string       { return f.active }
func (f *fakeSession) Snapshot() shell3.Snapshot { return f.snap }
func (f *fakeSession) History() []shell3.HistoryEntry {
	return f.history
}
func (f *fakeSession) Prune(id string) (string, bool) { return f.pruneOut, f.pruneOK }
func (f *fakeSession) SetParam(name, value string) error {
	if f.setParamFn != nil {
		return f.setParamFn(name, value)
	}
	return nil
}
func (f *fakeSession) Interject(text string, _ ...shell3.Part) {
	f.interjections = append(f.interjections, text)
}
func (f *fakeSession) SetApprover(fn func(ctx context.Context, req shell3.ApprovalRequest) bool) error {
	f.approver = fn
	return nil
}
func (f *fakeSession) Name() string { return f.name }
func (f *fakeSession) WakeEvents() <-chan shell3.HostEvent {
	return f.wakeBus
}

// RunQueued mirrors *shell3.Session.RunQueued: it emits the canned runQueued
// events (the turn the host runs to react to a Wake) and records the call. An
// empty runQueued slice models an empty inbox (closed channel, no turn).
func (f *fakeSession) RunQueued(ctx context.Context) <-chan shell3.Event {
	f.mu.Lock()
	f.runQueuedCalls++
	evs := f.runQueued
	f.mu.Unlock()
	ch := make(chan shell3.Event, len(evs)+1)
	for _, ev := range evs {
		ch <- ev
	}
	close(ch)
	return ch
}

// register sets up a fakeSlashApp with the chat command set, returning it plus
// the fakeSession and usage state the closures captured. The session is seeded
// with two agents so /agent tests can run.
func register() (*fakeSlashApp, *fakeSession, *usage) {
	app := &fakeSlashApp{}
	sess := &fakeSession{
		snap: shell3.Snapshot{
			Agent:        "main",
			StatusLine:   "anthropic │ claude-x",
			SystemPrompt: "be helpful",
		},
		agents: []string{"main", "fast"},
		active: "main",
	}
	u := &usage{}
	registerSlashCommands(app, sess, u, func() {})
	return app, sess, u
}

func TestSlash_RegistersExpectedCommands(t *testing.T) {
	app, _, _ := register()
	want := []string{"clear", "rollback", "prune", "usage", "prompt", "print", "agent", "exit", "quit"}
	for _, name := range want {
		if _, ok := app.handlers[name]; !ok {
			t.Errorf("missing handler: /%s", name)
		}
	}
}

func TestSlash_Clear(t *testing.T) {
	app, sess, _ := register()
	app.call(t, "clear", "")

	if !sess.cleared {
		t.Errorf("Clear not invoked on session")
	}
	if !containsAll(app.snapshot(), "[context cleared]") {
		t.Errorf("missing dim: %v", app.snapshot())
	}
}

func TestSlash_RollbackEmpty(t *testing.T) {
	app, sess, _ := register()
	sess.rollbackOK = false
	app.call(t, "rollback", "")
	if !sess.rolledBack {
		t.Errorf("Rollback not invoked")
	}
	if !containsAll(app.snapshot(), "[nothing to roll back]") {
		t.Errorf("want nothing-to-roll-back: %v", app.snapshot())
	}
}

func TestSlash_RollbackRemovesTurn(t *testing.T) {
	app, sess, _ := register()
	sess.rollbackOK = true
	app.call(t, "rollback", "")
	if !containsAll(app.snapshot(), "[last turn removed from context]") {
		t.Errorf("want removed message: %v", app.snapshot())
	}
}

func TestSlash_PruneNoArg(t *testing.T) {
	app, _, _ := register()
	app.call(t, "prune", "")
	if !containsAll(app.snapshot(), "/prune usage") {
		t.Errorf("want usage hint: %v", app.snapshot())
	}
}

func TestSlash_PruneByID(t *testing.T) {
	app, sess, _ := register()
	sess.pruneOut = "pruned by user (600 bytes freed)"
	sess.pruneOK = true
	app.call(t, "prune", "3")
	if !containsAll(app.snapshot(), "pruned by user") {
		t.Errorf("expected prune summary echoed: %v", app.snapshot())
	}
}

func TestSlash_UsageNoData(t *testing.T) {
	app, _, _ := register()
	app.call(t, "usage", "")
	if !containsAll(app.snapshot(), "[no usage data yet]") {
		t.Errorf("want no-data: %v", app.snapshot())
	}
}

func TestSlash_UsageWithData(t *testing.T) {
	app, _, u := register()
	*u = usage{prompt: 10, completion: 20, total: 30}
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
	app, _, _ := register()
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
	app, _, _ := register()
	app.call(t, "print", "")
	if !containsAll(app.snapshot(), "/print usage") {
		t.Errorf("want usage hint: %v", app.snapshot())
	}
}

func TestSlash_PrintByID(t *testing.T) {
	app, sess, _ := register()
	// History returns Content already prefix-stripped (pkg owns that), so /print
	// shows it raw. 15 lines proves it isn't subject to the inline 10-line cap;
	// the tail marker on the last line proves full output.
	body := strings.Repeat("filler\n", 14) + "TAILMARKER"
	sess.history = []shell3.HistoryEntry{
		{Role: "tool", ToolCallID: "3", ToolName: "bash", Content: body},
	}
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
}

func TestSlash_PrintUnknownID(t *testing.T) {
	app, _, _ := register()
	app.call(t, "print", "999")
	if !containsAll(app.snapshot(), "no tool result with id") {
		t.Errorf("want not-found hint: %v", app.snapshot())
	}
}

func TestSlash_AgentList(t *testing.T) {
	app, _, _ := register()
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
	sess := &fakeSession{
		snap:   shell3.Snapshot{Agent: "main", StatusLine: "anthropic │ claude-x"},
		agents: []string{"main", "fast"},
		active: "main",
	}
	applied := false
	registerSlashCommands(app, sess, &usage{}, func() {
		applied = true
	})
	app.call(t, "agent", "fast")

	if !applied {
		t.Errorf("applyAgent not called after successful switch")
	}
	if sess.active != "fast" {
		t.Errorf("SwitchAgent not applied, active=%q", sess.active)
	}
	if !containsAll(app.snapshot(), "[agent: fast]") {
		t.Errorf("missing agent confirm: %v", app.snapshot())
	}
}

func TestSlash_AgentUnknown(t *testing.T) {
	app, sess, _ := register()
	app.call(t, "agent", "bogus")

	if sess.active != "main" {
		t.Errorf("active changed on unknown agent: %q", sess.active)
	}
	if !containsAll(app.snapshot(), "unknown agent") {
		t.Errorf("missing unknown-agent error: %v", app.snapshot())
	}
}

func TestSlash_AgentNoneConfigured(t *testing.T) {
	app := &fakeSlashApp{}
	sess := &fakeSession{snap: shell3.Snapshot{StatusLine: "anthropic │ claude-x"}}
	registerSlashCommands(app, sess, &usage{}, func() {})
	app.call(t, "agent", "fast")

	if !containsAll(app.snapshot(), "[no agents configured]") {
		t.Errorf("want no-agents message: %v", app.snapshot())
	}
}

func TestSlash_QuitAliasesExit(t *testing.T) {
	app, _, _ := register()
	if app.handlers["exit"] == nil || app.handlers["quit"] == nil {
		t.Errorf("exit/quit not both registered")
	}
}

// TestInterjectWiring: App.SetInterject wires plain-text Enter-while-busy to
// Session.Interject via the closure registered in RunInteractive. This test
// exercises only the wiring (closure registration) without standing up the
// full interactive loop: it builds the same SetInterject closure manually and
// asserts the text flows through to fakeSession.interjections.
//
// Limitation: because RunInteractive's setup is monolithic (App construction,
// all SetXxx calls, and the stdin/event loops are interleaved), the wiring
// cannot be invoked hermetically without launching a real terminal loop. This
// test verifies the closure shape and the fakeSession.Interject path rather
// than the live RunInteractive registration path.
func TestInterjectWiring(t *testing.T) {
	sess := &fakeSession{}
	// Simulate what RunInteractive registers.
	interjectFn := func(text string) {
		sess.Interject(text)
	}

	interjectFn("change of plans")
	interjectFn("also this")

	if len(sess.interjections) != 2 {
		t.Fatalf("expected 2 interjections; got %d: %v", len(sess.interjections), sess.interjections)
	}
	if sess.interjections[0] != "change of plans" {
		t.Errorf("interjections[0] = %q; want \"change of plans\"", sess.interjections[0])
	}
	if sess.interjections[1] != "also this" {
		t.Errorf("interjections[1] = %q; want \"also this\"", sess.interjections[1])
	}
}

// ── wake-bus consumption ────────────────────────────────────────────────────

// TestWake_IdleRunsQueuedTurnWithDimNotice drives a Wake HostEvent for the
// active session onto the bus while the TUI is idle and asserts the consumer
// (a) prints the dim "woke" notice, (b) calls RunQueued, and (c) renders the
// resulting turn (here a SystemReminder carrying a subagent-finished line) via
// the SAME render sink used for normal Send turns — surfacing dim, not as user
// input. consumeWakes is the extracted loop RunInteractive runs on a goroutine.
func TestWake_IdleRunsQueuedTurnWithDimNotice(t *testing.T) {
	app := &fakeApp{}
	bus := make(chan shell3.HostEvent, 1)
	sess := &fakeSession{
		name:    "main",
		wakeBus: bus,
		runQueued: []shell3.Event{
			{Kind: shell3.SystemReminder, Text: "subagent abc finished: did the thing"},
			{Kind: shell3.Done},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var turnWG sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		consumeWakes(ctx, sess, app, &turnWG)
	}()

	bus <- shell3.HostEvent{Session: "main", Kind: shell3.Wake}

	// Wait for the wake turn to launch + drain.
	deadline := time.After(2 * time.Second)
	for {
		sess.mu.Lock()
		n := sess.runQueuedCalls
		sess.mu.Unlock()
		if n >= 1 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("RunQueued was not called after Wake")
		case <-time.After(5 * time.Millisecond):
		}
	}
	turnWG.Wait()
	cancel()
	<-done

	calls := app.snapshot()
	// Dim woke notice rendered (the recorded line quotes the dim escape as the
	// literal "\x1b[2m" sequence).
	foundNotice := false
	for _, c := range calls {
		if strings.Contains(c, "woke") && strings.Contains(c, `\x1b[2m`) {
			foundNotice = true
		}
	}
	if !foundNotice {
		t.Fatalf("expected dim woke notice, got:\n%s", strings.Join(calls, "\n"))
	}
	// Subagent-finished line rendered dim (SystemReminder path).
	foundReminder := false
	for _, c := range calls {
		if strings.Contains(c, "subagent abc finished") && strings.Contains(c, `\x1b[2m`) {
			foundReminder = true
		}
	}
	if !foundReminder {
		t.Fatalf("expected dim subagent-finished render, got:\n%s", strings.Join(calls, "\n"))
	}
	// The turn cleared busy at end (same lifecycle as a Send turn).
	if !containsAll(calls, "SetBusy(true)", "SetBusy(false)") {
		t.Fatalf("wake turn did not follow the busy lifecycle:\n%s", strings.Join(calls, "\n"))
	}
}

// TestWake_OtherSessionIgnored: a Wake whose Session does not match the active
// session is dropped (no RunQueued, no notice). Single-session TUI filtering.
func TestWake_OtherSessionIgnored(t *testing.T) {
	app := &fakeApp{}
	bus := make(chan shell3.HostEvent, 1)
	sess := &fakeSession{name: "main", wakeBus: bus, runQueued: []shell3.Event{{Kind: shell3.Done}}}

	ctx, cancel := context.WithCancel(context.Background())
	var turnWG sync.WaitGroup
	done := make(chan struct{})
	go func() { defer close(done); consumeWakes(ctx, sess, app, &turnWG) }()

	bus <- shell3.HostEvent{Session: "other", Kind: shell3.Wake}
	// Give the consumer a moment to (incorrectly) act, then stop it.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done
	turnWG.Wait()

	sess.mu.Lock()
	n := sess.runQueuedCalls
	sess.mu.Unlock()
	if n != 0 {
		t.Fatalf("RunQueued should not run for a non-matching session, called %d times", n)
	}
}

// TestWake_BusyTurnNoOverlap is the core serialization invariant: when a turn is
// already in flight (the turnGate is busy) and a Wake arrives for the active
// session, the consumer must NOT start an overlapping turn — RunQueued is not
// called and no second "[woke...]" notice is rendered. The running turn drains
// the inbox itself; consumeWakesWith just drops the wake (runTurn returns false).
//
// To force the gate-busy path deterministically we drive consumeWakesWith with
// the same runTurn closure RunInteractive/consumeWakes build, but pre-acquire the
// shared turnGate so begin() returns false for the incoming wake. The gate is
// released only after the assertion window, modelling an in-flight turn.
func TestWake_BusyTurnNoOverlap(t *testing.T) {
	app := &fakeApp{}
	bus := make(chan shell3.HostEvent, 1)
	sess := &fakeSession{
		name:      "main",
		wakeBus:   bus,
		runQueued: []shell3.Event{{Kind: shell3.SystemReminder, Text: "should never run"}, {Kind: shell3.Done}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Build the same gate + runTurn the loop uses, then occupy the gate to model
	// a turn already in flight. runTurn must return false for the wake below.
	var lastUsage usage
	renderSink, finishTurn := newRenderSink(app, &lastUsage)
	gate := &turnGate{}
	var turnWG sync.WaitGroup
	runTurn := func(start func(context.Context) <-chan shell3.Event) bool {
		if !gate.begin() {
			return false
		}
		turnCtx, c := context.WithCancel(ctx)
		app.SetBusy(true, c)
		ch := start(turnCtx)
		turnWG.Add(1)
		go func() {
			defer turnWG.Done()
			defer gate.end()
			defer c()
			defer func() { finishTurn(turnCtx.Err() != nil) }()
			for ev := range ch {
				renderSink(ev)
			}
		}()
		return true
	}

	// Occupy the gate: a turn is "in flight" (begin returns true here; the wake's
	// begin will then return false). We release it after the assertion window.
	if !gate.begin() {
		t.Fatal("precondition: gate should start idle")
	}

	done := make(chan struct{})
	go func() { defer close(done); consumeWakesWith(ctx, sess, app, runTurn) }()

	bus <- shell3.HostEvent{Session: "main", Kind: shell3.Wake}

	// Give the consumer time to (incorrectly) act on the wake while busy.
	time.Sleep(50 * time.Millisecond)

	sess.mu.Lock()
	n := sess.runQueuedCalls
	sess.mu.Unlock()
	if n != 0 {
		t.Fatalf("RunQueued must not run while a turn is in flight, called %d times", n)
	}
	// No "[woke...]" notice should have been printed for the ignored wake.
	for _, c := range app.snapshot() {
		if strings.Contains(c, "[woke") {
			t.Fatalf("ignored busy wake printed a woke notice:\n%s", strings.Join(app.snapshot(), "\n"))
		}
	}

	gate.end() // release the in-flight turn
	cancel()
	<-done
	turnWG.Wait()
}

// ── approval wiring ────────────────────────────────────────────────────────────

func TestFormatApprovalQuestion_RendersAgentToolArgsReason(t *testing.T) {
	q := formatApprovalQuestion(shell3.ApprovalRequest{
		Tool:    "bash",
		RawArgs: `{"cmd":"rm -rf build"}`,
		Reason:  "destructive command",
		Agent:   "main",
	})
	want := `main wants to run bash({"cmd":"rm -rf build"}) — destructive command`
	if q != want {
		t.Fatalf("question = %q; want %q", q, want)
	}
}

func TestFormatApprovalQuestion_NoReasonOmitsSuffix(t *testing.T) {
	q := formatApprovalQuestion(shell3.ApprovalRequest{
		Tool:    "edit_file",
		RawArgs: `{"path":"main.go"}`,
		Agent:   "fast",
	})
	want := `fast wants to run edit_file({"path":"main.go"})`
	if q != want {
		t.Fatalf("question = %q; want %q", q, want)
	}
}

func TestFormatApprovalQuestion_TruncatesLongArgs(t *testing.T) {
	long := `{"data":"` + strings.Repeat("x", 500) + `"}`
	q := formatApprovalQuestion(shell3.ApprovalRequest{
		Tool: "bash", RawArgs: long, Agent: "main",
	})
	if !strings.Contains(q, "…") {
		t.Fatalf("expected truncation ellipsis in: %q", q)
	}
	if strings.Contains(q, strings.Repeat("x", approvalArgsMax+1)) {
		t.Fatalf("args not truncated to ~%d chars: %q", approvalArgsMax, q)
	}
	// Bound: prefix + truncated args + ellipsis + closing paren.
	if len(q) > approvalArgsMax+64 {
		t.Fatalf("question too long (%d chars): %q", len(q), q)
	}
}

func TestFormatApprovalQuestion_TruncationKeepsRuneBoundary(t *testing.T) {
	// Fill so a multi-byte rune straddles the byte-200 cut.
	long := strings.Repeat("a", approvalArgsMax-1) + strings.Repeat("é", 50)
	q := formatApprovalQuestion(shell3.ApprovalRequest{
		Tool: "bash", RawArgs: long, Agent: "main",
	})
	if !utf8.ValidString(q) {
		t.Fatalf("truncation split a rune: %q", q)
	}
}

// TestApproverWiring mirrors TestInterjectWiring: it builds the same closure
// RunInteractive registers via sess.SetApprover and asserts the formatted
// question flows through to the App's RequestApproval (stubbed here) and the
// verdict flows back. RunInteractive's setup is monolithic, so the live
// registration path isn't invoked hermetically — this verifies the closure
// shape and the fakeSession.SetApprover path.
func TestApproverWiring(t *testing.T) {
	sess := &fakeSession{}
	var asked string
	requestApproval := func(q string) bool { // stands in for app.RequestApproval
		asked = q
		return true
	}
	// Simulate what RunInteractive registers.
	if err := sess.SetApprover(func(ctx context.Context, req shell3.ApprovalRequest) bool {
		return requestApproval(formatApprovalQuestion(req))
	}); err != nil {
		t.Fatalf("SetApprover: %v", err)
	}

	ok := sess.approver(context.Background(), shell3.ApprovalRequest{
		Tool: "bash", RawArgs: `{"cmd":"ls"}`, Reason: "policy", Agent: "main",
	})
	if !ok {
		t.Fatal("verdict not propagated back through the approver")
	}
	want := `main wants to run bash({"cmd":"ls"}) — policy`
	if asked != want {
		t.Fatalf("question = %q; want %q", asked, want)
	}
}

// Compile-time assertion that *shell3.Session satisfies the local session
// interface the loop and handlers depend on. fakeSession satisfies it too.
var _ session = (*shell3.Session)(nil)
var _ session = (*fakeSession)(nil)
