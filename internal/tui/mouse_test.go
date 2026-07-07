package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/shell3"
)

// mouseModel returns a sized model with n simple user blocks and a known
// viewport offset, so eventLine math is deterministic.
func mouseModel(t *testing.T, blocks, vpOffset int) *model {
	t.Helper()
	m := sized(closedSend(nil))
	for i := 0; i < blocks; i++ {
		m.tr.AddUser("block line")
	}
	m.refresh(false)
	m.vp.SetYOffset(vpOffset)
	return m
}

func click(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}
func motion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}
func release(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func TestEventLine(t *testing.T) {
	m := mouseModel(t, 20, 5)
	if line, ok := m.eventLine(3); !ok || line != 8 {
		t.Fatalf("eventLine(3) = %d,%v want 8,true", line, ok)
	}
	if _, ok := m.eventLine(m.vp.Height() + 1); ok {
		t.Fatal("eventLine past viewport height should be inViewport=false")
	}
}

func TestHandleMouse_DragSetsSelection(t *testing.T) {
	m := mouseModel(t, 20, 0)
	m.handleMouse(click(5, 2))
	if !m.selecting || m.selAnchor != 2 || m.dragged {
		t.Fatalf("after click: selecting=%v anchor=%d dragged=%v", m.selecting, m.selAnchor, m.dragged)
	}
	m.handleMouse(motion(5, 6))
	if !m.dragged || !m.hasSel || m.selHead != 6 {
		t.Fatalf("after motion: dragged=%v hasSel=%v head=%d", m.dragged, m.hasSel, m.selHead)
	}
	if lo, hi := m.selRange(); lo != 2 || hi != 6 {
		t.Fatalf("selRange = %d,%d want 2,6", lo, hi)
	}
	m.handleMouse(release(5, 6))
	if m.selecting {
		t.Fatal("release should end selecting")
	}
}

func TestRenderBlocks_HighlightsSelectedLines(t *testing.T) {
	tr := NewTranscript()
	tr.AddUser("alpha")
	tr.AddUser("bravo")
	tr.AddUser("charlie")
	// Line 0 is the blank top margin; line 1 is the first user prompt ("alpha").
	withSel, _, total, _ := tr.renderBlocks(40, 1, 1)
	noSel, _, _, _ := tr.renderBlocks(40, -1, -1)
	if total < 4 {
		t.Fatalf("expected >=4 lines, got %d", total)
	}
	wl := strings.Split(withSel, "\n")
	nl := strings.Split(noSel, "\n")
	if wl[1] == nl[1] {
		t.Fatalf("selected line 1 should differ from unselected: %q", wl[1])
	}
	if wl[0] != nl[0] {
		t.Fatalf("unselected line 0 (top margin) changed: %q vs %q", wl[0], nl[0])
	}
	if !strings.Contains(wl[1], "\x1b[") {
		t.Fatalf("selected line lacks ANSI styling: %q", wl[1])
	}
}

func TestSelectedText_StripsGutterAndANSI(t *testing.T) {
	m := sized(closedSend(nil))
	m.renderedLines = []string{
		"  \x1b[31mhello\x1b[0m",
		"  world   ",
	}
	m.hasSel = true
	m.selAnchor, m.selHead = 0, 1
	if got := m.selectedText(); got != "hello\nworld" {
		t.Fatalf("selectedText = %q, want \"hello\\nworld\"", got)
	}
}

func TestFinishSelection_CopiesAndNotices(t *testing.T) {
	m := sized(closedSend(nil))
	m.renderedLines = []string{"  line one", "  line two"}
	m.hasSel = true
	m.selAnchor, m.selHead = 0, 1
	_, cmd := m.finishSelection()
	if cmd == nil {
		t.Fatal("finishSelection should return a copy command")
	}
	if m.notice != "copied 2 line(s)" {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestHandleClick_TogglesFoldableBlock(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	m.tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "out"})
	ti := -1
	for i, it := range m.tr.items {
		if it.Kind == ItemTool {
			ti = i
		}
	}
	if ti < 0 {
		t.Fatal("no tool item built")
	}
	m.tr.items[ti].Folded = false
	m.refresh(false)
	y := m.blockStarts[ti] - m.vp.YOffset()
	m.dragged = false
	m.handleClick(y)
	if !m.tr.items[ti].Folded {
		t.Fatal("click on a foldable block should fold it")
	}
}

func TestHandleClick_ClearsSelectionOnNonFoldable(t *testing.T) {
	m := mouseModel(t, 5, 0) // user blocks are not foldable
	m.hasSel = true
	m.selAnchor, m.selHead = 0, 1
	m.handleClick(0)
	if m.hasSel {
		t.Fatal("click on a non-foldable block should clear the selection")
	}
}

func TestSelection_ExcludesMetaLines(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.Reasoning, Text: "secret"})
	m.tr.Apply(shell3.Event{Kind: shell3.Token, Text: "real answer"}) // folds the reasoning
	m.refresh(false)
	m.hasSel = true
	m.selAnchor, m.selHead = 0, m.totalLines-1
	got := m.selectedText()
	if strings.Contains(got, "thinking") || strings.Contains(got, "secret") {
		t.Fatalf("reasoning/thinking should be excluded from copy: %q", got)
	}
	if !strings.Contains(got, "real answer") {
		t.Fatalf("real content should be copied: %q", got)
	}
}

func TestSelection_CopiesThinkingContentNotIndicator(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.Reasoning, Text: "deep thought"}) // unfolded (no Token after)
	m.refresh(false)
	m.hasSel = true
	m.selAnchor, m.selHead = 0, m.totalLines-1
	got := m.selectedText()
	if !strings.Contains(got, "deep thought") {
		t.Fatalf("thinking content should be copyable: %q", got)
	}
	if strings.Contains(got, "thinking") {
		t.Fatalf("the thinking indicator line should be excluded: %q", got)
	}
}

func TestCloseStreaming_DropsWhitespaceAssistant(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: " \n"}) // whitespace-only
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	for _, it := range tr.items {
		if it.Kind == ItemAssistant {
			t.Fatal("whitespace-only assistant block should be dropped before the tool")
		}
	}
}

func TestCloseStreaming_KeepsRealAssistant(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.Token, Text: "real text"})
	tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	found := false
	for _, it := range tr.items {
		if it.Kind == ItemAssistant && it.Text == "real text" {
			found = true
		}
	}
	if !found {
		t.Fatal("real assistant text should be kept")
	}
}

func TestReminder_RendersFoldedVisible(t *testing.T) {
	tr := NewTranscript()
	tr.Apply(shell3.Event{Kind: shell3.SystemReminder, Text: "<system-reminder>\nhi there\n</system-reminder>"})
	it := tr.items[len(tr.items)-1]
	if !it.foldable() || !it.Folded {
		t.Fatalf("reminder should be foldable and start folded: foldable=%v folded=%v", it.foldable(), it.Folded)
	}
	out, _, _, _ := tr.renderBlocks(80, -1, -1)
	if !strings.Contains(out, "reminder") {
		t.Fatalf("reminder should render a visible indicator: %q", out)
	}
	if strings.Contains(out, "system-reminder>") {
		t.Fatalf("wrapper tags should be stripped: %q", out)
	}
}

func TestHandleMouse_JitterClickStillFolds(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.Apply(shell3.Event{Kind: shell3.ToolCall, ToolName: "bash", ToolCallID: "1", ToolInput: "ls"})
	m.tr.Apply(shell3.Event{Kind: shell3.ToolResult, ToolCallID: "1", ToolOutput: "out"})
	ti := -1
	for i, it := range m.tr.items {
		if it.Kind == ItemTool {
			ti = i
		}
	}
	if ti < 0 {
		t.Fatal("no tool item built")
	}
	m.tr.items[ti].Folded = false
	m.refresh(false)
	y := m.blockStarts[ti] - m.vp.YOffset()
	m.handleMouse(click(5, y))
	m.handleMouse(motion(7, y)) // x-only jitter on the same content line
	if m.dragged {
		t.Fatal("a same-line motion must not be treated as a drag")
	}
	m.handleMouse(release(7, y))
	if !m.tr.items[ti].Folded {
		t.Fatal("a jittery click on a foldable block should fold it, not copy a line")
	}
}

func TestHandleMouse_EdgeScrollExtendsSelection(t *testing.T) {
	m := mouseModel(t, 100, 0)
	m.follow = false
	h := m.vp.Height()
	m.handleMouse(click(5, 0))
	before := m.vp.YOffset()
	m.handleMouse(motion(5, h-1)) // bottom edge → scroll down and extend
	if m.vp.YOffset() <= before {
		t.Fatalf("drag at the bottom edge should scroll the viewport: %d -> %d", before, m.vp.YOffset())
	}
	if !m.dragged || !m.hasSel || m.selHead <= m.selAnchor {
		t.Fatalf("edge drag should extend the selection past the anchor: head=%d anchor=%d", m.selHead, m.selAnchor)
	}
}

func TestHandleMouse_UpwardDragSwapsRange(t *testing.T) {
	m := mouseModel(t, 20, 0)
	m.follow = false
	m.handleMouse(click(5, 6))
	m.handleMouse(motion(5, 2)) // drag upward, above the anchor
	if !m.dragged || m.selHead != 2 {
		t.Fatalf("upward drag: dragged=%v head=%d (want true,2)", m.dragged, m.selHead)
	}
	if lo, hi := m.selRange(); lo != 2 || hi != 6 {
		t.Fatalf("selRange should swap to 2,6, got %d,%d", lo, hi)
	}
}

func TestSelection_ExcludesBlockSeparators(t *testing.T) {
	m := sized(closedSend(nil))
	m.tr.AddUser("alpha")
	m.tr.AddUser("bravo")
	m.refresh(false)
	m.hasSel = true
	m.selAnchor, m.selHead = 0, m.totalLines-1
	got := m.selectedText()
	// The blank separator line between the two blocks is never highlighted, so it
	// must not appear in the copy (which would show as a blank line + inflate the
	// "copied N line(s)" count).
	if strings.Contains(got, "\n\n") {
		t.Fatalf("inter-block separator should be excluded from copy: %q", got)
	}
	if !strings.Contains(got, "alpha") || !strings.Contains(got, "bravo") {
		t.Fatalf("both blocks should be copied: %q", got)
	}
}

func TestHandleWheel_Scrolls(t *testing.T) {
	m := mouseModel(t, 100, 20)
	m.handleWheel(tea.Mouse{Button: tea.MouseWheelUp})
	if m.vp.YOffset() >= 20 {
		t.Fatalf("wheel up should scroll up; offset=%d", m.vp.YOffset())
	}
	before := m.vp.YOffset()
	m.handleWheel(tea.Mouse{Button: tea.MouseWheelDown})
	if m.vp.YOffset() <= before {
		t.Fatalf("wheel down should scroll down; offset=%d", m.vp.YOffset())
	}
}
