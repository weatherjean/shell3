package askui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// handleMouse drives line-level selection, click-to-fold, and wheel scroll.
//
// The mouse is captured (see View), so the terminal's own click-drag selection
// is unavailable while the app runs — which is why the app implements
// selection itself rather than leaving the user with only shift-drag. Scroll
// and select therefore work at the same time: the wheel scrolls, a drag
// selects and edge-scrolls past the visible page, and release copies.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	e := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(e)
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		line, ok, _ := m.eventLine(e.Y)
		if !ok {
			return m, nil
		}
		m.selecting = true
		m.dragged = false
		m.selAnchor = line
		m.selHead = line
		if m.hasSel { // starting a new gesture clears the old highlight
			m.hasSel = false
			m.refresh(false)
		}
		return m, nil
	case tea.MouseMotionMsg:
		if !m.selecting || e.Button != tea.MouseLeft {
			return m, nil
		}
		// Scroll first when the drag reaches an edge, then map the pointer
		// (clamped into the viewport) against the NEW offset — so the selection
		// end tracks content scrolling under it instead of freezing one line
		// short.
		m.edgeScroll(e.Y)
		y := min(max(e.Y, 0), m.vp.Height()-1)
		if line, ok, _ := m.eventLine(y); ok {
			m.selHead = line
		}
		// Only a span beyond the anchor line is a drag; a jittery click that
		// stays on one line still folds (decided on release).
		if m.selHead != m.selAnchor {
			m.dragged = true
			m.hasSel = true
		}
		m.refresh(false)
		return m, nil
	case tea.MouseReleaseMsg:
		if !m.selecting {
			return m, nil
		}
		m.selecting = false
		if m.dragged && m.selHead != m.selAnchor {
			return m.finishSelection()
		}
		return m.handleClick(e.Y)
	}
	return m, nil
}

// handleWheel scrolls the transcript viewport. Scrolling up breaks autoscroll
// (follow); reaching the bottom re-engages it.
func (m *model) handleWheel(e tea.Mouse) (tea.Model, tea.Cmd) {
	switch e.Button {
	case tea.MouseWheelUp:
		m.vp.ScrollUp(3)
		m.follow = false
	case tea.MouseWheelDown:
		m.vp.ScrollDown(3)
		m.syncFollow()
	}
	return m, nil
}

// edgeScroll nudges the viewport by one line when a drag reaches the top or
// bottom edge, so a selection can extend past the visible page. The caller
// refreshes after, preserving the new offset, and the next motion event at the
// edge maps to a further line.
func (m *model) edgeScroll(y int) {
	switch {
	case y <= 0:
		m.vp.ScrollUp(1)
		m.follow = false
	case y >= m.vp.Height()-1:
		m.vp.ScrollDown(1)
	}
}

// selRange returns the selection bounds low..high (inclusive), regardless of
// drag direction.
func (m *model) selRange() (lo, hi int) {
	lo, hi = m.selAnchor, m.selHead
	if lo > hi {
		lo, hi = hi, lo
	}
	return lo, hi
}

// selectedText returns the plain text of the current selection: the flattened
// lines in range, with the 2-cell gutter and all ANSI stripped and trailing
// spaces trimmed per line. Empty when there is no selection.
func (m *model) selectedText() string {
	if !m.hasSel || len(m.renderedLines) == 0 {
		return ""
	}
	lo, hi := m.selRange()
	var b strings.Builder
	for i := lo; i <= hi && i < len(m.renderedLines); i++ {
		// Copy exactly what is highlighted: excluded lines (system reminders,
		// the thinking indicator, inter-block separators) are not highlighted,
		// so they are not copied either (WYSIWYG). Thinking *content* stays
		// selectable; only its indicator line is excluded.
		if i < len(m.selExcluded) && m.selExcluded[i] {
			continue
		}
		s := ansi.Strip(m.renderedLines[i])
		r := []rune(s)
		if len(r) >= 2 {
			r = r[2:] // drop the 2-column gutter
		}
		b.WriteString(strings.TrimRight(string(r), " "))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// finishSelection copies the dragged selection and reports it. The highlight
// stays visible until the next click so the user sees what was copied.
func (m *model) finishSelection() (tea.Model, tea.Cmd) {
	text := m.selectedText()
	if text == "" {
		return m, nil
	}
	m.notice = fmt.Sprintf("copied %d line(s)", strings.Count(text, "\n")+1)
	return m, copyToClipboard(text)
}

// handleClick handles a plain (non-drag) left click: toggle a foldable block at
// the clicked line, otherwise just clear any existing selection. A click always
// dismisses the prior selection highlight.
func (m *model) handleClick(y int) (tea.Model, tea.Cmd) {
	m.hasSel = false
	// onContent, not just ok: a click in the blank area below a short
	// transcript clamps to the last line, and folding the last block because a
	// click landed nowhere near it is a surprise, not a shortcut.
	line, ok, onContent := m.eventLine(y)
	if !ok || !onContent {
		m.refresh(false)
		return m, nil
	}
	if b := m.blockAtLine(line); b >= 0 && b < len(m.tr.items) {
		m.tr.toggleFold(b) // a non-foldable block (user text, a reply) is a no-op
	}
	m.refresh(false)
	return m, nil
}

// eventLine maps a mouse screen-Y to a transcript content-line index.
// inViewport is false when the event is below the transcript (in the input or
// footer). The viewport is the top region of the screen, so content line =
// YOffset + y.
//
// A y past the last content line — the blank area under a short transcript —
// is CLAMPED to the last line rather than rejected, so a drag that runs off
// the end still selects to the end. onContent reports whether the clamp fired,
// which is what separates the two gestures: a drag wants the clamp, a click in
// empty space must NOT fold the last block just because it was nearest.
func (m *model) eventLine(y int) (line int, inViewport, onContent bool) {
	if y < 0 || y >= m.vp.Height() {
		return 0, false, false
	}
	line = max(m.vp.YOffset()+y, 0)
	onContent = true
	if m.totalLines > 0 && line >= m.totalLines {
		line = m.totalLines - 1
		onContent = false
	}
	return line, true, onContent
}

// blockAtLine maps a content line to the block (item index) that owns it.
func (m *model) blockAtLine(line int) int {
	b := 0
	for i, s := range m.blockStarts {
		if s <= line {
			b = i
		} else {
			break
		}
	}
	return b
}
