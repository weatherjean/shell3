package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func keyRune(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

func closedSend(record func(string)) func(string) (<-chan shell3.Event, context.CancelFunc) {
	return func(p string) (<-chan shell3.Event, context.CancelFunc) {
		if record != nil {
			record(p)
		}
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
}

func sized(send func(string) (<-chan shell3.Event, context.CancelFunc)) *model {
	m := newModel(send, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m
}

func TestSubmitSendsPromptAndAddsUserItem(t *testing.T) {
	var prompt string
	sent := false
	m := sized(closedSend(func(p string) { prompt, sent = p, true }))
	m.ta.SetValue("hello world")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if !sent || prompt != "hello world" {
		t.Fatalf("submit should send the prompt: sent=%v prompt=%q", sent, prompt)
	}
	// items[0] is the session-start banner; the user prompt follows it.
	last := m.tr.items[m.tr.count()-1]
	if m.tr.count() < 2 || last.Kind != ItemUser || last.Text != "hello world" {
		t.Fatalf("submit should add a user item: %+v", m.tr.items)
	}
	if m.tr.items[0].Kind != ItemBanner {
		t.Fatalf("first item should be the session-start banner, got %v", m.tr.items[0].Kind)
	}
	if !m.busy {
		t.Fatal("should be busy after submit")
	}
	if m.ta.Value() != "" {
		t.Fatalf("input should reset after submit, got %q", m.ta.Value())
	}
}

func TestEscEntersNormalKeepingDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("draft text")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // esc → NORMAL, draft kept
	if m.mode != modeNormal {
		t.Fatal("esc should enter NORMAL")
	}
	if m.ta.Value() != "draft text" {
		t.Fatalf("esc must NOT clear the draft, got %q", m.ta.Value())
	}
}

func TestNormalDDClearsInput(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("draft text")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if m.ta.Value() != "" {
		t.Fatalf("dd should clear the input, got %q", m.ta.Value())
	}
}

func TestEmptySubmitIsNoop(t *testing.T) {
	sent := false
	m := sized(closedSend(func(string) { sent = true }))
	m.ta.SetValue("   ")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if sent {
		t.Fatal("blank submit should not send")
	}
}

func TestNormalEnterTogglesFold(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	m.tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "out"})
	if !m.tr.items[0].Folded {
		t.Fatal("tool block should start folded")
	}
	// Esc twice to reach NORMAL with cursor on the (only) block.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL
	if m.mode != modeNormal {
		t.Fatalf("expected NORMAL, got %v", m.mode)
	}
	m.cursorLine = 0 // cursor on the (only) tool block
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.tr.items[0].Folded {
		t.Fatal("Enter in NORMAL should unfold the cursor block")
	}
}

func TestNormalLineScrollAndBlockJump(t *testing.T) {
	m := sized(closedSend(nil))
	for i := 0; i < 40; i++ {
		m.tr.AddUser(fmt.Sprintf("line %d", i))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL, builds blockStarts
	if m.mode != modeNormal {
		t.Fatal("expected NORMAL")
	}
	m.Update(keyRune('g'))
	m.Update(keyRune('g')) // top
	if m.cursorLine != 0 || m.vp.YOffset() != 0 {
		t.Fatalf("gg should put cursor and view at top, line=%d offset=%d", m.cursorLine, m.vp.YOffset())
	}
	m.Update(keyRune('j')) // line cursor down
	if m.cursorLine != 1 {
		t.Fatalf("j should move the line cursor by one, line=%d", m.cursorLine)
	}
	// Pushing the cursor past the viewport must scroll the view.
	for i := 0; i < 30; i++ {
		m.Update(keyRune('j'))
	}
	if m.vp.YOffset() == 0 {
		t.Fatal("cursor moving past the screen should scroll the view")
	}
	// } jumps the cursor to the next block's first line.
	m.cursorLine = 0
	m.refresh(false)
	m.Update(keyRune('}'))
	if m.cursorLine != m.blockStarts[1] {
		t.Fatalf("} should jump to next block start: line=%d want=%d", m.cursorLine, m.blockStarts[1])
	}
}

func TestInputGrowsWithNewlinesAndShrinksViewport(t *testing.T) {
	m := sized(closedSend(nil))
	base := m.vp.Height()
	// Exceed the 3-row minimum so the input genuinely grows.
	m.ta.SetValue("l1\nl2\nl3\nl4\nl5\nl6")
	m.relayout()
	if m.ta.Height() < 6 {
		t.Fatalf("textarea should auto-grow to >=6 rows, got %d", m.ta.Height())
	}
	if m.vp.Height() >= base {
		t.Fatalf("viewport should shrink as input grows: now %d, was %d", m.vp.Height(), base)
	}
	// Total rows still fit the terminal: vp + input + footer == height.
	if m.vp.Height()+m.ta.Height()+1 != m.height {
		t.Fatalf("rows must sum to height: %d+%d+1 != %d", m.vp.Height(), m.ta.Height(), m.height)
	}
}

func TestHelpOverlayOpensAndCloses(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40}) // tall enough for the full overlay
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})     // → NORMAL
	m.Update(keyRune('?'))
	if !m.helpOpen {
		t.Fatal("? should open the help overlay")
	}
	plain := stripANSI(m.View().Content)
	for _, want := range []string{"shell3 — keys", "NORMAL", "fold / unfold block", ":clear"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("help overlay missing %q", want)
		}
	}
	m.Update(keyRune('j')) // any key closes
	if m.helpOpen {
		t.Fatal("any key should dismiss the help overlay")
	}
}

// fakeCmds is a minimal sessionCmds for agent-cycle tests.
type fakeCmds struct {
	names  []string
	active string
	status string
	queued bool
}

func (f *fakeCmds) Clear() error                { return nil }
func (f *fakeCmds) Rollback() (bool, error)     { return false, nil }
func (f *fakeCmds) Prune(string) (string, bool) { return "", false }
func (f *fakeCmds) AgentNames() []string        { return f.names }
func (f *fakeCmds) ActiveAgent() string         { return f.active }
func (f *fakeCmds) Snapshot() shell3.Snapshot {
	return shell3.Snapshot{Agent: f.active, StatusLine: f.status}
}
func (f *fakeCmds) HasQueuedInput() bool { return f.queued }
func (f *fakeCmds) SwitchAgent(name string) error {
	f.active = name
	return nil
}

func TestTabCyclesAgent(t *testing.T) {
	fc := &fakeCmds{names: []string{"main", "research", "build"}, active: "main"}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "research" || m.agentName != "research" {
		t.Fatalf("tab should cycle to research, got active=%q name=%q", fc.active, m.agentName)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "main" {
		t.Fatalf("tab should wrap back to main, got %q", fc.active)
	}
}

func TestTabIsNoopWhileBusy(t *testing.T) {
	fc := &fakeCmds{names: []string{"main", "research"}, active: "main"}
	m := newModel(closedSend(nil), fc, "main", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.busy = true
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if fc.active != "main" {
		t.Fatalf("tab must not switch agents while busy, got %q", fc.active)
	}
}

func TestFooterShowsActiveAgent(t *testing.T) {
	m := sized(closedSend(nil))
	m.agentName = "research"
	if !strings.Contains(stripANSI(m.renderFooter()), "research") {
		t.Fatalf("footer should show active agent, got %q", stripANSI(m.renderFooter()))
	}
}

func TestInsertQuestionMarkOpensHelpWhenEmpty(t *testing.T) {
	m := sized(closedSend(nil))
	// Empty input: '?' opens help instead of typing.
	m.Update(keyRune('?'))
	if !m.helpOpen {
		t.Fatal("? on empty input should open help in INSERT")
	}
	if m.ta.Value() != "" {
		t.Fatalf("? should not be typed when opening help, got %q", m.ta.Value())
	}
}

func TestInsertQuestionMarkTypesWhenNotEmpty(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("how")
	m.Update(keyRune('?'))
	if m.helpOpen {
		t.Fatal("? with text present should NOT open help")
	}
	if m.ta.Value() != "how?" {
		t.Fatalf("? should type normally with text present, got %q", m.ta.Value())
	}
}

func TestWelcomeCardShowsThenHides(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if !strings.Contains(stripANSI(m.View().Content), "shell3") {
		t.Fatal("welcome card should render before the first message")
	}
	if !strings.Contains(stripANSI(m.View().Content), "build") {
		t.Fatal("welcome card should show the active agent")
	}
	// After a message the card is gone (transcript drives the viewport).
	m.tr.AddUser("hi")
	m.refresh(true)
	if strings.Contains(stripANSI(m.vp.View()), "Unix-composable") {
		t.Fatal("welcome card should disappear once the transcript is non-empty")
	}
}

func TestCtrlUClearsInput(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("draft text")
	m.Update(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.ta.Value() != "" {
		t.Fatalf("ctrl+u should clear the input, got %q", m.ta.Value())
	}
}

func TestEditFileOutputColorizedAsDiff(t *testing.T) {
	out := colorizeDiff("@@ -0,0 +1,2 @@\n+added line\n-removed line\n context")
	plain := stripANSI(out)
	for _, want := range []string{"@@ -0,0 +1,2 @@", "+added line", "-removed line", "context"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("diff text must survive colorization, missing %q", want)
		}
	}
	if !strings.Contains(out, "\x1b[") {
		t.Fatal("diff output should contain ANSI color codes")
	}
}

func TestModelLabelExtractsModel(t *testing.T) {
	if got := modelLabel("openai │ gpt-x │ high"); got != "gpt-x" {
		t.Fatalf("modelLabel = %q, want gpt-x", got)
	}
	if got := modelLabel("solo"); got != "solo" {
		t.Fatalf("single-segment modelLabel = %q, want solo", got)
	}
	if got := modelLabel(""); got != "" {
		t.Fatalf("empty modelLabel = %q", got)
	}
}

func TestFooterShowsModelAndAgentOnce(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "openai │ gpt-x │ high")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	foot := stripANSI(m.renderFooter())
	if strings.Count(foot, "build") != 1 {
		t.Fatalf("agent should appear exactly once (the pill): %q", foot)
	}
	if !strings.Contains(foot, "gpt-x") {
		t.Fatalf("footer should show the model: %q", foot)
	}
	if strings.Contains(foot, "openai") || strings.Contains(foot, "high") {
		t.Fatalf("footer should not show the provider/effort triplet: %q", foot)
	}
}

func TestCommandPaletteFilters(t *testing.T) {
	m := sized(closedSend(nil))
	m.mode = modeCommand
	m.cmdline = "ag"
	box := stripANSI(m.commandPalette())
	if !strings.Contains(box, ":agent") || !strings.Contains(box, ":agents") {
		t.Fatalf("palette should list agent commands for 'ag':\n%s", box)
	}
	if strings.Contains(box, ":clear") {
		t.Fatalf("palette should filter out non-matching commands:\n%s", box)
	}
}

func TestFollowBreaksOnScrollUpAndRelocksOnG(t *testing.T) {
	m := sized(closedSend(nil))
	for i := 0; i < 60; i++ {
		m.tr.AddUser(fmt.Sprintf("line %d", i))
	}
	m.follow = true
	m.refresh(true)
	if !m.vp.AtBottom() {
		t.Fatal("should start at the bottom")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL
	m.Update(keyRune('g'))                         // gg → top, scrolls the view off the bottom
	m.Update(keyRune('g'))
	if m.follow {
		t.Fatal("scrolling up should break the autoscroll lock")
	}
	if !strings.Contains(stripANSI(m.renderFooter()), "↓ G to follow") {
		t.Fatalf("footer should show the not-at-bottom indicator: %q", stripANSI(m.renderFooter()))
	}
	m.Update(keyRune('G')) // shift+g → relock + bottom
	if !m.follow || !m.vp.AtBottom() {
		t.Fatal("G should re-lock autoscroll and jump to the bottom")
	}
}

func TestCloseBraceOnLastBlockJumpsToBottom(t *testing.T) {
	m := sized(closedSend(nil))
	for i := 0; i < 40; i++ {
		m.tr.AddUser(fmt.Sprintf("line %d", i))
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape}) // → NORMAL (cursor at last block)
	m.cursorLine = m.blockStarts[len(m.blockStarts)-1]
	m.refresh(false)
	m.Update(keyRune('}')) // already on last block → jump to bottom
	if m.cursorLine != m.totalLines-1 || !m.vp.AtBottom() {
		t.Fatalf("} on the last block should jump to the bottom: line=%d total=%d atBottom=%v",
			m.cursorLine, m.totalLines, m.vp.AtBottom())
	}
}

func TestMidTurnSubmitSteersInsteadOfRefusing(t *testing.T) {
	var steered []string
	m := sized(closedSend(nil))
	m.steer = func(s string) { steered = append(steered, s) }
	m.busy = true
	m.ta.SetValue("also handle the edge case")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})

	if len(steered) != 1 || steered[0] != "also handle the edge case" {
		t.Fatalf("submit while busy should steer the turn: %v", steered)
	}
	if !m.busy {
		t.Fatal("steering must not end the running turn")
	}
	if m.ta.Value() != "" {
		t.Fatalf("input should clear after steering, got %q", m.ta.Value())
	}
	last := m.tr.items[m.tr.count()-1]
	if !last.Steer || last.Text != "also handle the edge case" {
		t.Fatalf("a steer item should be shown: %+v", last)
	}
}

func TestQueuedSteeringAutoRunsAtTurnEnd(t *testing.T) {
	fc := &fakeCmds{queued: true}
	ran := false
	m := newModel(closedSend(nil), fc, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran = true
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
	m.busy = true
	m.Update(eventMsg{ok: false}) // turn ends with steering still queued
	if !ran {
		t.Fatal("queued steering should auto-run a follow-up turn at turn end")
	}
	if !m.busy {
		t.Fatal("the follow-up turn should mark the model busy again")
	}
}

func TestCancelDuringThinkingShowsMarkerAndFolds(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	m.cancel = func() {}
	// Mid-thinking: an open, unfolded reasoning block, no tokens yet.
	m.tr.Apply(shell3.Event{Kind: shell3.Reasoning, Text: "considering the request"})
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}) // ctrl+c → cancel
	if !m.canceling {
		t.Fatal("ctrl+c while busy should set canceling")
	}
	// Turn ends by channel close WITHOUT an Error event (the thinking-cancel case).
	m.Update(eventMsg{ok: false})
	plain := stripANSI(mustRender(m.tr))
	if !strings.Contains(plain, "⊘ canceled") {
		t.Fatalf("canceled marker should render even when only thinking was on screen:\n%s", plain)
	}
	if !m.tr.items[0].Folded {
		t.Fatal("the half-streamed thinking block should fold on cancel")
	}
	if m.canceling || m.busy {
		t.Fatal("canceling/busy should be cleared after the turn ends")
	}
}

func TestCanceledErrorEventSuppressed(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	// An Error(context.Canceled) must NOT become a red "✗ context canceled".
	m.Update(eventMsg{ok: true, ev: shell3.Event{Kind: shell3.Error, Err: context.Canceled}, ch: nil})
	plain := stripANSI(mustRender(m.tr))
	if strings.Contains(plain, "context canceled") || strings.Contains(plain, "✗") {
		t.Fatalf("raw context-canceled error must be suppressed:\n%s", plain)
	}
}

func TestFooterThinkingHasRainbowBackground(t *testing.T) {
	m := sized(closedSend(nil))
	m.busy = true
	foot := m.renderFooter()
	if !strings.Contains(stripANSI(foot), "thinking") {
		t.Fatal("busy footer should say thinking")
	}
	if !strings.Contains(foot, "\x1b[48;2;") {
		t.Fatal("thinking indicator should have a truecolor (rainbow) background")
	}
}

func TestFooterQuitArmedShowsRedBar(t *testing.T) {
	m := sized(closedSend(nil))
	m.quitArmed = true
	foot := m.renderFooter()
	if !strings.Contains(stripANSI(foot), "press ctrl+c again to quit") {
		t.Fatal("quit-armed footer should prompt to press again")
	}
	if !strings.Contains(foot, "48;2;185;28;28") { // cRed background
		t.Fatalf("quit-armed bar should have a red background:\n%q", foot)
	}
}

func TestEditorResultLoadsIntoDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.enterNormal()
	m.Update(openEditorMsg{text: "a big\nmulti-line prompt\n"})
	if m.ta.Value() != "a big\nmulti-line prompt" {
		t.Fatalf("editor result should load into the draft, got %q", m.ta.Value())
	}
	if m.mode != modeInsert {
		t.Fatal("loading an edited prompt should return to INSERT")
	}
}

func TestEditorResultErrorShowsNotice(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("keep me")
	m.Update(openEditorMsg{err: fmt.Errorf("boom")})
	if m.ta.Value() != "keep me" {
		t.Fatal("a failed editor run must not clobber the draft")
	}
	if !strings.Contains(m.notice, "boom") {
		t.Fatalf("editor error should surface in the notice, got %q", m.notice)
	}
}

func TestResolveEditorPrefersEnv(t *testing.T) {
	t.Setenv("VISUAL", "")
	t.Setenv("EDITOR", "myeditor --wait")
	if got := resolveEditor(); got != "myeditor --wait" {
		t.Fatalf("resolveEditor should return $EDITOR verbatim (args preserved), got %q", got)
	}
	t.Setenv("VISUAL", "vis")
	if got := resolveEditor(); got != "vis" {
		t.Fatalf("resolveEditor should prefer $VISUAL, got %q", got)
	}
}

func TestBashSafetyConfirmDefaultsYesOnEnter(t *testing.T) {
	m := sized(closedSend(nil))
	reply := make(chan bool, 1)
	m.Update(confirmMsg{req: &confirmReq{command: "rm -rf /tmp/x", reply: reply}})
	if m.confirm == nil || !m.confirmYes {
		t.Fatal("confirm modal should open with Yes selected")
	}
	// Modal renders over the transcript with the command + buttons.
	plain := stripANSI(m.View().Content)
	for _, want := range []string{"bash_safety", "rm -rf /tmp/x", "Yes", "No"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("confirm modal missing %q", want)
		}
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}) // Enter on default Yes → allow
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("Enter on default Yes should allow")
		}
	default:
		t.Fatal("Enter should have replied to the asker")
	}
	if m.confirm != nil {
		t.Fatal("modal should dismiss after answering")
	}
}

func TestBashSafetyConfirmDenyKeys(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{
		{Code: 'n'},
		{Code: tea.KeyEscape},
	} {
		m := sized(closedSend(nil))
		reply := make(chan bool, 1)
		m.Update(confirmMsg{req: &confirmReq{command: "curl evil", reply: reply}})
		m.Update(key)
		select {
		case ok := <-reply:
			if ok {
				t.Fatalf("key %v should deny", key)
			}
		default:
			t.Fatalf("key %v should reply deny", key)
		}
	}
}

func TestDisableSafetyAutoAllowsAndShowsBang(t *testing.T) {
	m := newModel(closedSend(nil), nil, "build", "openai │ gpt-x")
	m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	// Toggle safety off via the command.
	m.runCommand("disable_safety")
	if !m.safetyOff {
		t.Fatal(":disable_safety should turn safety off")
	}
	if !strings.Contains(stripANSI(m.renderFooter()), "!") {
		t.Fatal("footer should show the ! indicator when safety is off")
	}
	// A bash_safety ask now auto-allows without showing the modal.
	reply := make(chan bool, 1)
	m.Update(confirmMsg{req: &confirmReq{command: "rm x", reply: reply}})
	if m.confirm != nil {
		t.Fatal("no modal should appear when safety is off")
	}
	select {
	case ok := <-reply:
		if !ok {
			t.Fatal("ask should auto-allow when safety is off")
		}
	default:
		t.Fatal("ask should have been auto-answered")
	}
	// Toggle back on.
	m.runCommand("disable_safety")
	if m.safetyOff {
		t.Fatal(":disable_safety should toggle back on")
	}
}

func TestCommandTabComplete(t *testing.T) {
	m := sized(closedSend(nil))
	m.mode = modeCommand
	// ":dis" → only disable_safety matches → full completion.
	m.cmdline = "dis"
	m.handleCommandKey("tab")
	if m.cmdline != "disable_safety" {
		t.Fatalf("tab should complete a unique match, got %q", m.cmdline)
	}
	// ":a" → agent/agents → completes to common prefix "agent".
	m.cmdline = "a"
	m.handleCommandKey("tab")
	if m.cmdline != "agent" {
		t.Fatalf("tab should extend to the common prefix, got %q", m.cmdline)
	}
	// Not in command position (has a space) → no-op.
	m.cmdline = "agent fo"
	m.handleCommandKey("tab")
	if m.cmdline != "agent fo" {
		t.Fatalf("tab must not touch an argument, got %q", m.cmdline)
	}
}

func TestStartSpinNoDuplicateChain(t *testing.T) {
	m := sized(closedSend(nil))
	if m.startSpin() == nil {
		t.Fatal("first startSpin should start a tick chain")
	}
	if m.startSpin() != nil {
		t.Fatal("a second startSpin while already spinning must be a no-op (no duplicate chain)")
	}
	// A tick that fires while not busy ends the chain.
	m.busy = false
	m.Update(spinnerTickMsg{})
	if m.spinning {
		t.Fatal("a tick with !busy should clear spinning")
	}
	if m.startSpin() == nil {
		t.Fatal("after the chain ends, startSpin should be able to start again")
	}
}

func TestApplyAgentRefreshesStatusAndContext(t *testing.T) {
	fc := &fakeCmds{active: "b", status: "openai │ gpt-b │ low"}
	m := newModel(closedSend(nil), fc, "a", "openai │ gpt-a │ high")
	m.applyAgent()
	if m.agentName != "b" || m.statusMsg != "openai │ gpt-b │ low" {
		t.Fatalf("applyAgent should refresh agent + status from the snapshot: %q / %q", m.agentName, m.statusMsg)
	}
	if modelLabel(m.statusMsg) != "gpt-b" {
		t.Fatalf("footer model label should track the new agent, got %q", modelLabel(m.statusMsg))
	}
}

func TestHandleWakeDrainsQueuedWhenIdle(t *testing.T) {
	fc := &fakeCmds{queued: true}
	ran := false
	m := newModel(closedSend(nil), fc, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.sessionName = "main"
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran = true
		ch := make(chan shell3.Event)
		close(ch)
		return ch, func() {}
	}
	// Wake for this session while idle → drain the queued inbox.
	m.handleWake(shell3.HostEvent{Session: "main", Kind: shell3.Wake})
	if !ran || !m.busy {
		t.Fatal("an idle wake for this session with queued input should run a follow-up turn")
	}
	// Wake while busy → no-op.
	m.busy = true
	ran = false
	m.handleWake(shell3.HostEvent{Session: "main", Kind: shell3.Wake})
	if ran {
		t.Fatal("a wake while busy must be ignored (the turn drains its own inbox)")
	}
	// Wake for a different session → no-op.
	m.busy = false
	m.handleWake(shell3.HostEvent{Session: "other", Kind: shell3.Wake})
	if ran {
		t.Fatal("a wake naming a different session must be ignored")
	}
}

func TestEditorEmptySaveKeepsDraft(t *testing.T) {
	m := sized(closedSend(nil))
	m.ta.SetValue("keep me")
	m.Update(openEditorMsg{text: "   \n\n"})
	if m.ta.Value() != "keep me" {
		t.Fatalf("an empty editor save must not wipe the draft, got %q", m.ta.Value())
	}
}

func TestFooterShowsContextWindowFraction(t *testing.T) {
	m := sized(closedSend(nil))
	m.tokens, m.contextWindow = 5000, 10000
	if !strings.Contains(stripANSI(m.renderFooter()), "5000/10000 (50%)") {
		t.Fatalf("footer should show the context-window fraction: %q", stripANSI(m.renderFooter()))
	}
}

func TestCtrlCRequiresTwoPresses(t *testing.T) {
	m := sized(closedSend(nil))
	m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !m.quitArmed {
		t.Fatal("first ctrl+c should arm quit, not exit")
	}
	// A different key disarms.
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.quitArmed {
		t.Fatal("a non-ctrl+c key should disarm quit")
	}
}
