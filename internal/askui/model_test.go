package askui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/weatherjean/shell3/internal/shell3"
)

type fakeCmds struct {
	queued bool
	jobs   []shell3.JobInfo
}

func (f *fakeCmds) HasQueuedInput() bool   { return f.queued }
func (f *fakeCmds) Jobs() []shell3.JobInfo { return f.jobs }

func testModel(t *testing.T, cmds sessionCmds) (*model, chan shell3.Event, *[]string, *int) {
	t.Helper()
	ch := make(chan shell3.Event, 32)
	var sent []string
	canceled := 0
	m := newModel(func(p string) (<-chan shell3.Event, context.CancelFunc) {
		sent = append(sent, p)
		return ch, func() { canceled++ }
	}, cmds, "main", "openai │ gpt-x │ high")
	m.contextWindow = 1000
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return m, ch, &sent, &canceled
}

func typeKeys(m *model, keys ...string) {
	for _, k := range keys {
		switch k {
		case "enter":
			m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		case "ctrl+c":
			m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		case "ctrl+o":
			m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
		default:
			for _, r := range k {
				m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
		}
	}
}

// TestSubmitStartsTurn: typing and pressing enter sends the text as a turn,
// clears the input, and shows the transcript's first block.
func TestSubmitStartsTurn(t *testing.T) {
	m, _, sent, _ := testModel(t, &fakeCmds{})
	typeKeys(m, "hi", "enter")

	if !slices.Equal(*sent, []string{"hi"}) {
		t.Fatalf("sent = %v, want [hi]", *sent)
	}
	snap := m.uiSnapshot()
	if snap.input != "" {
		t.Errorf("input should clear on submit, got %q", snap.input)
	}
	if !snap.busy {
		t.Error("model should be busy while the turn runs")
	}
	if snap.blockCount != 1 {
		t.Errorf("blockCount = %d, want 1 (the user message)", snap.blockCount)
	}
}

func TestSubmitEmptyIsNoop(t *testing.T) {
	m, _, sent, _ := testModel(t, &fakeCmds{})
	typeKeys(m, "   ", "enter")
	if len(*sent) != 0 {
		t.Fatalf("blank input started a turn: %v", *sent)
	}
}

func TestSubmitMidTurnSteers(t *testing.T) {
	m, _, sent, _ := testModel(t, &fakeCmds{})
	var steered []string
	m.steer = func(s string) { steered = append(steered, s) }

	typeKeys(m, "first", "enter")
	typeKeys(m, "also this", "enter")

	if !slices.Equal(*sent, []string{"first"}) {
		t.Fatalf("a mid-turn message started a second turn: %v", *sent)
	}
	if !slices.Equal(steered, []string{"also this"}) {
		t.Fatalf("steered = %v, want [also this]", steered)
	}
	if got := m.uiSnapshot().blockCount; got != 2 {
		t.Errorf("blockCount = %d, want 2 (user + steer)", got)
	}
}

// TestCtrlCCancelsThenQuits pins the two-stage interrupt: during a turn ctrl+c
// cancels the turn (never quits), and only a later press with nothing running
// arms and then confirms the quit.
func TestCtrlCCancelsThenQuits(t *testing.T) {
	m, ch, _, canceled := testModel(t, &fakeCmds{})
	typeKeys(m, "go", "enter")

	typeKeys(m, "ctrl+c")
	if *canceled != 1 {
		t.Fatalf("ctrl+c during a turn should cancel it (canceled=%d)", *canceled)
	}
	if m.uiSnapshot().quitArmed {
		t.Error("ctrl+c during a turn must not arm the quit prompt")
	}

	close(ch) // the canceled turn's channel closes
	m.Update(eventMsg{ok: false})
	if m.uiSnapshot().busy {
		t.Fatal("model still busy after the turn channel closed")
	}

	typeKeys(m, "ctrl+c")
	if !m.uiSnapshot().quitArmed {
		t.Fatal("first idle ctrl+c should arm the quit prompt")
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("second ctrl+c should return a quit command")
	}
}

// TestCanceledTurnRendersMarker: a canceled turn ends with a clean "canceled"
// block, not a red context.Canceled error — the raw error is suppressed.
func TestCanceledTurnRendersMarker(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	typeKeys(m, "go", "enter")
	typeKeys(m, "ctrl+c")

	m.Update(eventMsg{ok: true, ev: shell3.Event{Kind: shell3.Error, Err: context.Canceled}})
	m.Update(eventMsg{ok: false})

	last := m.tr.items[len(m.tr.items)-1]
	if last.Kind != itemNotice || last.Notice != noticeReminder || !strings.Contains(last.Text, "canceled") {
		t.Fatalf("last block = %+v, want the clean canceled marker", last)
	}
	for _, it := range m.tr.items {
		if it.Kind == itemNotice && it.Notice == noticeError {
			t.Fatal("the raw context.Canceled error must not render as an error block")
		}
	}
}

func TestQueuedInputRunsFollowUpTurn(t *testing.T) {
	cmds := &fakeCmds{queued: true}
	m, _, _, _ := testModel(t, cmds)
	ran := 0
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran++
		return make(chan shell3.Event), func() {}
	}
	typeKeys(m, "go", "enter")
	m.Update(eventMsg{ok: false})

	if ran != 1 {
		t.Fatalf("queued input ran %d follow-up turns, want 1", ran)
	}
	if !m.uiSnapshot().busy {
		t.Error("the follow-up turn should leave the model busy")
	}
}

func TestWakeDrainsCompletion(t *testing.T) {
	cmds := &fakeCmds{queued: true}
	m, _, _, _ := testModel(t, cmds)
	m.sessionID = "sess-1"
	ran := 0
	m.runQueued = func() (<-chan shell3.Event, context.CancelFunc) {
		ran++
		return make(chan shell3.Event), func() {}
	}

	m.Update(wakeMsg{ev: shell3.HostEvent{Session: "other", Kind: shell3.Wake}})
	if ran != 0 {
		t.Fatalf("a wake for another session drained this one's inbox")
	}

	m.Update(wakeMsg{ev: shell3.HostEvent{Session: "sess-1", Kind: shell3.Wake}})
	if ran != 1 {
		t.Fatalf("wake ran %d turns, want 1", ran)
	}
}

func TestFooterSegments(t *testing.T) {
	cmds := &fakeCmds{jobs: []shell3.JobInfo{{Done: false}, {Done: true}}}
	m, _, _, _ := testModel(t, cmds)
	m.tokens = 250
	m.refreshJobCount()

	footer := strings.Join(m.uiSnapshot().footer, " | ")
	for _, want := range []string{"gpt-x", "ctx: 25%", "bg: 1", "main"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q missing %q", footer, want)
		}
	}
	if strings.Contains(footer, "bg: 2") {
		t.Error("a finished job must not inflate the bg count")
	}
}

// TestFoldKey: ctrl+o is an all-or-nothing toggle — it collapses whatever is
// still open, and expands everything once nothing is. There is no per-block
// binding, so this one key is the ONLY way to reach a folded block's contents.
func TestFoldKey(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.apply(shell3.Event{Kind: shell3.Reasoning, Text: "pondering"})
	// The tool call closes the streaming reasoning block, folding it; edit_file
	// itself starts expanded.
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "edit_file", ToolCallID: "1"})
	if !m.tr.items[0].Folded || m.tr.items[1].Folded {
		t.Fatalf("setup: want folded reasoning + expanded edit_file, got %v/%v",
			m.tr.items[0].Folded, m.tr.items[1].Folded)
	}

	typeKeys(m, "ctrl+o")
	if m.tr.anyUnfolded() {
		t.Error("ctrl+o with something open should collapse every foldable block")
	}
	typeKeys(m, "ctrl+o")
	if m.tr.items[0].Folded || m.tr.items[1].Folded {
		t.Error("ctrl+o with nothing open should expand everything")
	}
	// An OLD block (not just the newest) must come back — the whole point of
	// the all-or-nothing binding.
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "2"})
	typeKeys(m, "ctrl+o")
	typeKeys(m, "ctrl+o")
	if m.tr.items[0].Folded {
		t.Error("the oldest block must unfold too, not only the newest")
	}
}

func TestMouseWheelScrolls(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	for i := 0; i < 80; i++ {
		m.tr.addInfo("line")
	}
	m.refresh(true)
	bottom := m.vp.YOffset()

	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if m.vp.YOffset() >= bottom {
		t.Fatalf("wheel up did not scroll (offset %d, was %d)", m.vp.YOffset(), bottom)
	}
	if m.uiSnapshot().follow {
		t.Error("scrolling up with the wheel should release follow")
	}
	m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if m.vp.YOffset() <= bottom-3-1 {
		t.Errorf("wheel down did not scroll back (offset %d)", m.vp.YOffset())
	}
}

func TestMouseCaptureIsOn(t *testing.T) {
	if got := m0(t).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("MouseMode = %v, want cell-motion (wheel events, no per-pixel motion)", got)
	}
}

func m0(t *testing.T) *model {
	t.Helper()
	m, _, _, _ := testModel(t, &fakeCmds{})
	return m
}

// TestToolBlockPairing: a ToolResult attaches to its open call by id (one
// block, with status), and a stray result with no matching call still renders
// rather than vanishing.
func TestToolBlockPairing(t *testing.T) {
	tr := newTranscript()
	tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "a", ToolInput: `{"command":"ls"}`})
	tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolCallID: "a", ToolOutput: "file.txt"})
	if len(tr.items) != 1 {
		t.Fatalf("call+result should be one block, got %d", len(tr.items))
	}
	if it := tr.items[0]; !it.ToolDone || it.ToolOutput != "file.txt" {
		t.Fatalf("result did not attach to its call: %+v", it)
	}

	tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolName: "bash", ToolCallID: "orphan", ToolOutput: "late"})
	if len(tr.items) != 2 {
		t.Fatalf("an unmatched result must still render, got %d blocks", len(tr.items))
	}
}

// TestReasoningFoldsOnReply: a thinking block streams open and collapses the
// moment the answer starts, so a finished turn isn't a wall of reasoning.
func TestReasoningFoldsOnReply(t *testing.T) {
	tr := newTranscript()
	tr.apply(shell3.Event{Kind: shell3.Reasoning, Text: "hmm"})
	if tr.items[0].Folded {
		t.Error("thinking should stream unfolded")
	}
	tr.apply(shell3.Event{Kind: shell3.Token, Text: "answer"})
	if !tr.items[0].Folded {
		t.Error("thinking should fold once the reply starts")
	}
}

// TestEmptyAssistantBlockDropped: models often emit a stray space before a tool
// call; glamour renders it to nothing, which would read as a blank gap.
func TestEmptyAssistantBlockDropped(t *testing.T) {
	tr := newTranscript()
	tr.apply(shell3.Event{Kind: shell3.Token, Text: " \n"})
	tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "a"})
	if len(tr.items) != 1 || tr.items[0].Kind != itemTool {
		t.Fatalf("blank assistant block survived: %+v", tr.items)
	}
}

func TestReminderHeldDuringStream(t *testing.T) {
	tr := newTranscript()
	tr.apply(shell3.Event{Kind: shell3.Token, Text: "part one"})
	tr.apply(shell3.Event{Kind: shell3.SystemReminder, Text: "<system-reminder>ping</system-reminder>"})
	if len(tr.items) != 1 {
		t.Fatalf("reminder split the streaming answer: %d blocks", len(tr.items))
	}
	tr.apply(shell3.Event{Kind: shell3.Done})
	if len(tr.items) != 2 || tr.items[1].Kind != itemNotice {
		t.Fatalf("reminder was not flushed after the answer: %+v", tr.items)
	}
}

// TestErrorCarriesRecoveryHint: an error block must include the runtime's
// recovery hint when it has one — the plain renderer prints it, and the UI
// dropping it would make the two views disagree about the same failure.
func TestErrorCarriesRecoveryHint(t *testing.T) {
	err := errors.New("boom")
	tr := newTranscript()
	tr.apply(shell3.Event{Kind: shell3.Error, Err: err})
	got := tr.items[0].Text
	want := "boom"
	if hint := shell3.RecoveryHint(err); hint != "" {
		want += "\n" + hint
	}
	if got != want {
		t.Errorf("error text = %q, want %q", got, want)
	}
}

func TestScrollReleasesFollow(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	for i := 0; i < 60; i++ {
		m.tr.addInfo("line")
	}
	m.refresh(true)
	if !m.uiSnapshot().follow {
		t.Fatal("a fresh refresh should be following the bottom")
	}
	m.vp.ScrollUp(10)
	m.syncFollow()
	if m.uiSnapshot().follow {
		t.Error("scrolling up should release follow")
	}
	m.vp.GotoBottom()
	m.syncFollow()
	if !m.uiSnapshot().follow {
		t.Error("scrolling back to the bottom should re-lock follow")
	}
}

func TestWelcomeCardBeforeFirstMessage(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	if got := m.uiSnapshot().blockCount; got != 0 {
		t.Fatalf("blockCount = %d, want 0 before the first message", got)
	}
	if !strings.Contains(m.welcomeCard(), "separate from the Telegram chat") {
		t.Error("the welcome card should state that ask has its own conversation")
	}
}

// TestViewRendersWithoutPanic drives a full render at a realistic size with
// every block kind present — the cheapest guard against a layout regression
// that only shows up on a real terminal.
func TestViewRendersWithoutPanic(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.addUser("hello")
	m.tr.apply(shell3.Event{Kind: shell3.Reasoning, Text: "thinking hard"})
	m.tr.apply(shell3.Event{Kind: shell3.Token, Text: "# Heading\n\nsome **markdown**"})
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "edit_file", ToolCallID: "1"})
	m.tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "Edited x.go\n@@ -1 +1 @@\n-old\n+new"})
	m.tr.apply(shell3.Event{Kind: shell3.Compacted, TotalTokens: 100})
	m.refresh(true)

	out := m.View().Content
	if !strings.Contains(out, "edit_file") {
		t.Errorf("rendered view missing the tool block:\n%s", out)
	}
	if strings.Count(out, "\n")+1 > 24 {
		t.Errorf("rendered view is taller than the 24-row terminal:\n%s", out)
	}
}

func TestDragSelectsAndCopies(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.addUser("alpha")
	m.tr.addUser("bravo")
	m.tr.addUser("charlie")
	m.refresh(true)

	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: 0})
	m.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, Y: 4})
	if !m.hasSel {
		t.Fatal("dragging across lines should create a selection")
	}
	got := m.selectedText()
	if !strings.Contains(got, "alpha") {
		t.Errorf("selected text %q should contain the dragged content", got)
	}
	if strings.Contains(got, "\x1b") {
		t.Errorf("selected text still carries ANSI: %q", got)
	}
	if strings.HasPrefix(got, "  ") {
		t.Errorf("selected text still carries the 2-column gutter: %q", got)
	}

	_, cmd := m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, Y: 4})
	if cmd == nil {
		t.Fatal("releasing a drag should return the clipboard command")
	}
	if !strings.Contains(m.uiSnapshot().notice, "copied") {
		t.Errorf("notice = %q, want a copied-N-lines report", m.uiSnapshot().notice)
	}
}

func TestSelectionSkipsMetaLines(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.addUser("keep me")
	m.tr.apply(shell3.Event{Kind: shell3.SystemReminder, Text: "<system-reminder>secret chrome</system-reminder>"})
	m.tr.items[len(m.tr.items)-1].Folded = false
	m.tr.addUser("keep me too")
	m.refresh(true)

	m.hasSel = true
	m.selAnchor, m.selHead = 0, len(m.renderedLines)-1
	got := m.selectedText()
	if !strings.Contains(got, "keep me") {
		t.Fatalf("selection dropped ordinary content: %q", got)
	}
	if strings.Contains(got, "secret chrome") {
		t.Errorf("a system reminder was copied: %q", got)
	}
}

// TestClickFoldsOneBlock: click-to-fold is the per-block path ctrl+o
// deliberately doesn't have — an old bash call five rounds back opens with one
// click and nothing else moves.
func TestClickFoldsOneBlock(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: `{"command":"ls"}`})
	m.tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "a\nb"})
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "2", ToolInput: `{"command":"pwd"}`})
	m.tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "2", ToolOutput: "/tmp"})
	m.refresh(true)

	// Click the FIRST block's header row (line 1: line 0 is the top margin).
	y := m.blockStarts[0] - m.vp.YOffset()
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: y})
	m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, Y: y})

	if m.tr.items[0].Folded {
		t.Error("clicking a folded block should unfold it")
	}
	if !m.tr.items[1].Folded {
		t.Error("clicking one block must not disturb its neighbours")
	}
}

func TestEdgeScrollExtendsSelection(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	for i := 0; i < 80; i++ {
		m.tr.addInfo("line")
	}
	m.refresh(true)
	before := m.vp.YOffset()

	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: 5})
	m.Update(tea.MouseMotionMsg{Button: tea.MouseLeft, Y: 0})
	if m.vp.YOffset() >= before {
		t.Fatalf("dragging to the top edge did not scroll (offset %d, was %d)", m.vp.YOffset(), before)
	}
	if !m.hasSel {
		t.Error("an edge-scrolling drag should still be selecting")
	}
}

// TestClickBelowContentFoldsNothing: a click in the blank area under a short
// transcript clamps to the last content line (the clamp a drag needs to select
// to the end). Folding the last block because a click landed nowhere near it
// is a surprise, not a shortcut.
func TestClickBelowContentFoldsNothing(t *testing.T) {
	m, _, _, _ := testModel(t, &fakeCmds{})
	m.tr.apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: `{"command":"ls"}`})
	m.tr.apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "a"})
	m.refresh(true)
	folded := m.tr.items[0].Folded

	empty := m.vp.Height() - 1 // well past the two-block transcript
	m.Update(tea.MouseClickMsg{Button: tea.MouseLeft, Y: empty})
	m.Update(tea.MouseReleaseMsg{Button: tea.MouseLeft, Y: empty})

	if m.tr.items[0].Folded != folded {
		t.Error("a click in empty space folded the nearest block")
	}
}
