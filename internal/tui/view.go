package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func (m *model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion // capture mouse for select/copy + click-to-collapse
	v.WindowTitle = "shell3"
	if !m.ready || m.width <= 0 {
		return v
	}
	// Render the input first: ta.View() refreshes the textarea's internal scroll
	// state, which inputScrollIndicator then reads for the same frame (no 1-frame
	// lag on the ▲/▼ markers).
	taView := m.ta.View()
	base := lipgloss.JoinVertical(lipgloss.Left,
		m.vp.View(),
		m.inputScrollIndicator(), // blank, or ▲/▼ when the input scrolls off-screen
		taView,
		m.renderFooter(),
	)
	switch {
	case m.confirm != nil:
		base = m.placeModal(m.confirmBox())
	case m.helpOpen:
		base = m.placeModal(m.helpBox())
	case m.bg.open:
		base = m.placeModal(m.backgroundBox())
	case m.mode == modeCommand:
		// The command palette floats just above the input so the typed line in
		// the footer stays visible.
		base = overlayAbove(base, m.commandPalette(), m.vp.Height())
	}
	v.Content = base
	return v
}

// refresh rebuilds the viewport content. It preserves the scroll position in
// NORMAL (so line-scrolling and streaming don't fight); in INSERT it follows
// the bottom when already there (or forced).
func (m *model) refresh(forceBottom bool) {
	// Before any message, fill the viewport with the centered welcome card.
	if m.tr.count() == 0 {
		card := lipgloss.Place(m.vp.Width(), m.vp.Height(),
			lipgloss.Center, lipgloss.Center, m.welcomeCard())
		m.vp.SetContent(card)
		m.blockStarts = nil
		m.totalLines = 0
		m.cursorLine = 0
		return
	}
	off := m.vp.YOffset()
	selLo, selHi := -1, -1
	if m.hasSel {
		selLo, selHi = m.selRange()
	}
	content, starts, total, excluded := m.tr.renderBlocks(m.cursorLine, m.mode == modeNormal, m.vp.Width(), selLo, selHi)
	m.blockStarts = starts
	m.totalLines = total
	m.renderedLines = strings.Split(content, "\n")
	m.selExcluded = excluded
	if m.cursorLine >= total {
		m.cursorLine = total - 1
	}
	if m.cursorLine < 0 {
		m.cursorLine = 0
	}
	m.vp.SetContent(content)
	// follow locks the view to the bottom as content streams; navigation up
	// clears it (see moveLine/jumpBlock), G re-locks it.
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
	// footer + blank spacer + at least 3 transcript rows.
	m.ta.MaxHeight = max(min(m.height-2-3, inputMaxRows), 1)
	// DynamicHeight sizes the textarea itself; read it back for layout.
	ih := max(m.ta.Height(), 1)
	// footer (1) + one blank spacer line above the input (1).
	vpH := max(m.height-2-ih, 1)
	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpH)
	m.refresh(false)
}

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
// message is sent. lipgloss.Place centers
// it within the viewport in refresh().
func (m *model) welcomeCard() string {
	// A config-supplied card (shell3.welcome) replaces the built-in one verbatim,
	// so any ANSI escapes it embeds render in the terminal's own colors. refresh()
	// still centers it in the viewport.
	if m.welcome != "" {
		return m.welcome
	}
	title := stBrand.Render("๑ï shell3") + "  " + stDim.Render("/ˈʃɛli/")
	sub := stFgDim.Render("minimal Unix-composable coding agent")
	lines := []string{title, sub, ""}
	if m.agentName != "" {
		lines = append(lines, stUserPrompt.Render("agent")+"  "+stUserText.Render(m.agentName), "")
	}
	mode := func(name, desc string) string {
		return stUserPrompt.Render(fmt.Sprintf("%-8s", name)) + stFgDim.Render(desc)
	}
	lines = append(lines,
		mode("INSERT", "type, enter sends  ·  esc → NORMAL"),
		mode("NORMAL", "j/k move · click/enter folds · drag or y copies · i types"),
		mode("COMMAND", ": commands (:clear :agent :q …)"),
		"",
		stDim.Render("?")+stFgDim.Render(" help")+stDim.Render("   ·   tab")+stFgDim.Render(" switch agent"),
	)
	return lipgloss.NewStyle().
		Padding(1, 4).
		Render(strings.Join(lines, "\n"))
}

func (m *model) renderFooter() string {
	if m.mode == modeCommand {
		return stModeCommand.Render(":" + m.cmdline + "█")
	}
	var mode string
	switch m.mode {
	case modeNormal:
		mode = stModeNormal.Render(" N ")
	default:
		mode = stModeInsert.Render(" I ")
	}
	// Left: mode pill, then the model with its context-window fill (ctx: x%), then
	// the transient last-action notice (primary, auto-hidden after noticeTTL), then
	// the live turn state (quit-armed prompt / thinking shimmer).
	left := mode
	if model := m.modelName; model != "" {
		// Context-window fill sits right after the model name.
		if m.tokens > 0 && m.contextWindow > 0 {
			model += fmt.Sprintf("  (ctx: %d%%)", m.tokens*100/m.contextWindow)
		}
		left += " " + stDim.Render(model)
	}
	switch {
	case m.quitArmed:
		// Ctrl+C once: red middle bar telling you to press again.
		left += " " + stCtrlCArmed.Render(" press ctrl+c again to quit ")
	default:
		if n := m.activeNotice(); n != "" {
			left += " " + stNotice.Render(n)
		}
		if m.busy {
			// Thinking: white text on an animated rainbow background (no spinner).
			left += " " + rainbowBg(" thinking ", m.spinner)
		}
	}

	// Right side, left-to-right: "? help" hint (only at rest), the "!" danger pill
	// when the shell is unsafe, the live subprocess count (bg: N), then the brand
	// snail glued to the active agent badge (Tab cycles the agent).
	var seg []string
	if strings.TrimSpace(m.ta.Value()) == "" {
		seg = append(seg, stDim.Render("? help"))
	}
	// "!" when the shell is unsafe: runtime :disable_safety, or on_tool_call not
	// enabled in the lua config (unsafe by default).
	if m.safetyOff || !m.safetyConfigured {
		seg = append(seg, stYolo.Render(" ! "))
	}
	if m.bgCount > 0 {
		seg = append(seg, stBgCount.Render(fmt.Sprintf(" bg: %d ", m.bgCount)))
	}
	// Snail brand + agent badge form one visual unit (no gap between them).
	badge := stSnail.Render(" ๑ï ")
	if m.agentName != "" {
		badge += agentBadge(m.agentName)
	}
	seg = append(seg, badge)

	right := strings.Join(seg, "  ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return left // no room; drop the right side rather than wrap
	}
	return left + strings.Repeat(" ", gap) + right
}

// placeModal centers a modal box on the otherwise-blank screen, replacing the
// transcript while the modal is open.
func (m *model) placeModal(box string) string {
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center, box)
}

// modalWidth clamps a preferred modal content width to [minModalWidth,max] and
// to what the terminal can actually fit, so a narrow window never overflows the
// edge.
func (m *model) modalWidth(preferred, maxW int) int {
	w := min(max(preferred, minModalWidth), maxW)
	return max(min(w, m.width-4), 1)
}

// overlayAbove pastes box's lines onto base ending at row vpBottom-1 (left
// aligned), so a panel can float just above the input row.
func overlayAbove(base, box string, vpBottom int) string {
	bl := strings.Split(base, "\n")
	xl := strings.Split(box, "\n")
	start := vpBottom - len(xl)
	if start < 0 {
		start = 0
	}
	for i, line := range xl {
		if r := start + i; r >= 0 && r < len(bl) {
			bl[r] = line
		}
	}
	return strings.Join(bl, "\n")
}
