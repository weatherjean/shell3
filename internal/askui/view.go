package askui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	// Capture the mouse: the alternate screen has no scrollback of its own, so
	// an uncaptured wheel does nothing at all. Capture also takes the
	// terminal's own click-drag selection away, so the app implements
	// selection itself (mouse.go) rather than leaving shift-drag as the only
	// way to copy — wheel scroll, drag-select with edge scrolling, and
	// click-to-fold all work at once. Cell-motion, not all-motion: button and
	// wheel events plus drag motion, not a report per pixel of idle movement.
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "shell3 ask"
	if !m.ready || m.width <= 0 {
		return v
	}
	// Render the input first: ta.View() refreshes the textarea's internal scroll
	// state, which inputScrollIndicator then reads for the same frame (no
	// 1-frame lag on the ▲/▼ markers).
	taView := m.ta.View()
	v.Content = lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.inputScrollIndicator(), // blank, or ▲/▼ when the input scrolls off-screen
		taView,
		m.renderFooter(),
	)
	return v
}

// refresh rebuilds the viewport content. It preserves the current scroll
// position unless forceBottom is set or follow is locked (streaming content
// sticks to the bottom until the user scrolls away).
func (m *model) refresh(forceBottom bool) {
	// Before any message, fill the viewport with the centered welcome card.
	if m.tr.count() == 0 {
		m.renderedLines, m.selExcluded = nil, nil
		card := lipgloss.Place(m.vp.Width(), m.vp.Height(),
			lipgloss.Center, lipgloss.Center, m.welcomeCard())
		m.vp.SetContent(card)
		m.blockStarts = nil
		m.totalLines = 0
		return
	}
	off := m.vp.YOffset()
	selLo, selHi := -1, -1
	if m.hasSel {
		selLo, selHi = m.selRange()
	}
	content, starts, total, excluded := m.tr.renderBlocks(m.vp.Width(), selLo, selHi)
	m.blockStarts = starts
	m.totalLines = total
	m.renderedLines = strings.Split(content, "\n")
	m.selExcluded = excluded
	m.vp.SetContent(content)
	// follow locks the view to the bottom as content streams; scrolling away
	// releases it (see handleWheel / handleKey).
	if forceBottom || m.follow {
		m.vp.GotoBottom()
	} else {
		m.vp.SetYOffset(off)
	}
}

func (m *model) relayout() {
	if m.width <= 0 || m.height <= 0 {
		return
	}
	m.ta.SetWidth(m.width)
	// Cap the input's max height to fit this terminal — leave the footer plus a
	// few transcript rows — so a tall paste/draft can't overflow the layout and
	// freeze input. Content beyond this scrolls inside the textarea.
	m.ta.MaxHeight = max(min(m.height-2-3, inputMaxRows), 1)
	// DynamicHeight sizes the textarea itself; read it back for layout.
	ih := max(m.ta.Height(), 1)
	// footer (1) + one blank spacer line above the input (1).
	vpH := max(m.height-2-ih, 1)
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpH)
	m.refresh(false)
}

// syncFollow re-reads whether the viewport sits at the bottom after a scroll,
// so streaming content re-locks to the bottom once the user scrolls back down
// and stays put while they read further up.
func (m *model) syncFollow() { m.follow = m.vp.AtBottom() }

// inputScrollIndicator is the one-line gutter above the input. It is blank
// unless the input has grown past its visible height, in which case it shows a
// dim, right-aligned ▲ (more above), ▼ (more below), or ▲▼ — so a long
// paste/draft doesn't silently hide content off the top or bottom.
//
// Overflow is measured by logical line count vs the visible height: the
// textarea's ScrollPercent is unreliable here (it pads its viewport content to
// the view height, so a fitting single line reports a non-1.0 percent and would
// show a spurious ▼). Logical lines undercount only when a line soft-wraps, so
// at worst an arrow is omitted — never shown for input that actually fits.
func (m *model) inputScrollIndicator() string {
	visible := m.ta.Height()
	off := m.ta.ScrollYOffset()
	total := m.ta.LineCount()
	above := off > 0
	below := total > off+visible
	if !above && !below {
		return ""
	}
	marker := "▼"
	switch {
	case above && below:
		marker = "▲▼"
	case above:
		marker = "▲"
	}
	return lipgloss.NewStyle().Width(m.width).Align(lipgloss.Right).Render(stFgDim.Render(marker))
}

// welcomeCard is the centered greeting shown in the viewport before the first
// message is sent. lipgloss.Place centers it within the viewport in refresh().
func (m *model) welcomeCard() string {
	title := stBrand.Render("๑ï shell3") + "  " + stDim.Render("/ˈʃɛli/")
	sub := stFgDim.Render("minimal Unix-composable personal agent")
	lines := []string{title, sub, ""}
	if m.agentName != "" {
		lines = append(lines, stUserPrompt.Render("agent")+"  "+stUserText.Render(m.agentName), "")
	}
	row := func(name, desc string) string {
		return stUserPrompt.Render(fmt.Sprintf("%-10s", name)) + stFgDim.Render(desc)
	}
	lines = append(lines,
		row("type", "the input is always live — enter sends"),
		row("shift+↵", "newline (alt+↵ / ctrl+j also work)"),
		row("ctrl+o", "fold every tool/thinking block · again unfolds them"),
		row("mouse", "wheel scrolls · drag selects + copies · click folds one block"),
		row("scroll", "wheel or pgup/pgdn"),
		row("ctrl+c", "stop the turn · again to quit"),
		"",
		stDim.Render("this conversation is separate from the Telegram chat"),
	)
	return lipgloss.NewStyle().
		Padding(1, 4).
		Render(strings.Join(lines, "\n"))
}

// footerSeg is one visual chunk of the footer, retaining plain text beside its
// rendered form so tests can inspect footer behavior without scraping ANSI.
type footerSeg struct {
	plain    string
	rendered string
}

func renderedSegs(segs []footerSeg) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.rendered
	}
	return out
}

// buildFooter computes the footer's left and right segments.
func (m *model) buildFooter() (left, right []footerSeg) {
	// Left: the model with its context-window fill (ctx: x%), then the transient
	// last-action notice (auto-hidden after noticeTTL), then the live turn state
	// (quit-armed prompt / thinking shimmer).
	if name := m.modelName; name != "" {
		if m.tokens > 0 && m.contextWindow > 0 {
			name += fmt.Sprintf("  (ctx: %d%%)", m.tokens*100/m.contextWindow)
		}
		left = append(left, footerSeg{name, stDim.Render(name)})
	}
	switch {
	case m.quitArmed:
		// Ctrl+C once: red middle bar telling you to press again.
		txt := "press ctrl+c again to quit"
		left = append(left, footerSeg{txt, stCtrlCArmed.Render(" " + txt + " ")})
	default:
		if n := m.activeNotice(); n != "" {
			left = append(left, footerSeg{n, stNotice.Render(n)})
		}
		if m.busy {
			// Thinking: white text on an animated rainbow background (no spinner).
			left = append(left, footerSeg{"thinking", rainbowBg(" thinking ", m.spinner)})
		}
	}

	// Right side, left-to-right: the key hint (only at rest, to declutter the
	// footer while actively typing), the live background-job count (bg: N), then
	// the brand snail glued to the active agent badge.
	if strings.TrimSpace(m.ta.Value()) == "" {
		hint := "ctrl+o fold all"
		right = append(right, footerSeg{hint, stDim.Render(hint)})
	}
	if m.bgCount > 0 {
		txt := fmt.Sprintf("bg: %d", m.bgCount)
		right = append(right, footerSeg{txt, stBgCount.Render(" " + txt + " ")})
	}
	// Snail brand + agent badge form one visual unit (no gap between them).
	badgePlain, badgeRendered := "๑ï", stSnail.Render(" ๑ï ")
	if m.agentName != "" {
		badgePlain += " " + m.agentName
		badgeRendered += agentBadge(m.agentName)
	}
	right = append(right, footerSeg{badgePlain, badgeRendered})
	return left, right
}

func (m *model) renderFooter() string {
	left, right := m.buildFooter()
	leftStr := strings.Join(renderedSegs(left), " ")
	rightStr := strings.Join(renderedSegs(right), "  ")
	gap := m.width - lipgloss.Width(leftStr) - lipgloss.Width(rightStr)
	if gap < 1 {
		return leftStr // no room; drop the right side rather than wrap
	}
	return leftStr + strings.Repeat(" ", gap) + rightStr
}
