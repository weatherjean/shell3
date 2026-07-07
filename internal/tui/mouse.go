package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// handleMouse drives line-level selection, click-to-collapse, and wheel scroll.
// It is active in every mode — the mouse acts on the transcript while the
// keyboard does its mode-specific thing.
func (m *model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	e := msg.Mouse()
	// The background-jobs modal owns the mouse while open: the wheel scrolls it, and
	// clicks/drags don't reach the (hidden) transcript underneath.
	if m.bg.open {
		if _, ok := msg.(tea.MouseWheelMsg); ok {
			m.handleBackgroundWheel(e)
		}
		return m, nil
	}
	switch msg.(type) {
	case tea.MouseWheelMsg:
		return m.handleWheel(e)
	case tea.MouseClickMsg:
		if e.Button != tea.MouseLeft {
			return m, nil
		}
		line, ok := m.eventLine(e.Y)
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
		// Scroll first when the drag reaches an edge, then map the pointer (clamped
		// into the viewport) against the new offset — so the selection end tracks
		// content scrolling under it instead of freezing one line short.
		m.edgeScroll(e.Y)
		y := e.Y
		if y < 0 {
			y = 0
		}
		if y >= m.vp.Height() {
			y = m.vp.Height() - 1
		}
		if line, ok := m.eventLine(y); ok {
			m.selHead = line
		}
		// Only a span beyond the anchor line is a drag; a jittery click that stays
		// on one line still folds (decided on release).
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
// bottom edge, so a selection can extend past the visible page (edge-scroll
// during drag). The caller refreshes after, preserving the new offset, and the
// next motion event at the edge maps to a further line.
func (m *model) edgeScroll(y int) {
	switch {
	case y <= 0:
		m.vp.ScrollUp(1)
		m.follow = false
	case y >= m.vp.Height()-1:
		m.vp.ScrollDown(1)
	}
}

// syncFollow re-derives the autoscroll lock from the viewport: new output is
// followed only while the view is parked at the bottom.
func (m *model) syncFollow() { m.follow = m.vp.AtBottom() }

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
		// Copy exactly what is highlighted: excluded lines (banner, system
		// reminders, the thinking indicator, inter-block separators) are not
		// highlighted, so they are not copied either (WYSIWYG). Thinking *content*
		// stays selectable; only its indicator line is excluded.
		if i < len(m.selExcluded) && m.selExcluded[i] {
			continue
		}
		s := ansi.Strip(m.renderedLines[i])
		r := []rune(s)
		if len(r) >= 2 {
			r = r[2:] // drop the gutter ("▌ " or "  ")
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
	n := strings.Count(text, "\n") + 1
	m.notice = fmt.Sprintf("copied %d line(s)", n)
	return m, copyToClipboard(text)
}

// handleClick handles a plain (non-drag) left click: toggle a foldable block at
// the clicked line, otherwise just clear any existing selection. A click always
// dismisses the prior selection highlight.
func (m *model) handleClick(y int) (tea.Model, tea.Cmd) {
	m.hasSel = false
	line, ok := m.eventLine(y)
	if !ok {
		m.refresh(false)
		return m, nil
	}
	b := m.blockAtLine(line)
	if b >= 0 && b < len(m.tr.items) && m.tr.items[b].foldable() {
		m.tr.ToggleFold(b)
	}
	m.refresh(false)
	return m, nil
}

// eventLine maps a mouse screen-Y to a transcript content-line index. inViewport
// is false when the event is below the transcript (in the input or footer). The
// viewport is the top region of the screen, so content line = YOffset + y.
func (m *model) eventLine(y int) (line int, inViewport bool) {
	if y < 0 || y >= m.vp.Height() {
		return 0, false
	}
	line = m.vp.YOffset() + y
	if line < 0 {
		line = 0
	}
	if m.totalLines > 0 && line >= m.totalLines {
		line = m.totalLines - 1
	}
	return line, true
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
