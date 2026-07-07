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
	// At most one modal replaces the transcript (see modal.go) — help,
	// background, confirm, or the ctrl+p command palette.
	if kind := m.currentModal(); kind != modalNone {
		base = m.placeModal(m.renderModal(kind))
	}
	v.Content = base
	return v
}

// refresh rebuilds the viewport content. It preserves the current scroll
// position unless forceBottom is set or follow is locked (streaming content
// sticks to the bottom until the user scrolls away — see syncFollow).
func (m *model) refresh(forceBottom bool) {
	// Before any message, fill the viewport with the centered welcome card.
	if m.tr.count() == 0 {
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
	// follow locks the view to the bottom as content streams; the palette's
	// "follow" command re-locks it after scrolling away.
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
	row := func(name, desc string) string {
		return stUserPrompt.Render(fmt.Sprintf("%-8s", name)) + stFgDim.Render(desc)
	}
	lines = append(lines,
		row("type", "the input is always live — enter sends"),
		row("ctrl+p", "command palette (agents, background jobs, …)"),
		row("mouse", "drag selects + copies · click folds · wheel scrolls"),
		"",
		stDim.Render("?")+stFgDim.Render(" help")+stDim.Render("   ·   tab")+stFgDim.Render(" switch agent"),
	)
	return lipgloss.NewStyle().
		Padding(1, 4).
		Render(strings.Join(lines, "\n"))
}

// footerSeg is one visual chunk of the footer: its styled (rendered) form for
// display, paired with the plain-text form uiSnapshot reports — computed
// together in buildFooter so the two can never drift apart.
type footerSeg struct {
	plain    string
	rendered string
}

func plainSegs(segs []footerSeg) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.plain
	}
	return out
}

func renderedSegs(segs []footerSeg) []string {
	out := make([]string, len(segs))
	for i, s := range segs {
		out[i] = s.rendered
	}
	return out
}

// buildFooter computes the footer's left and right segments. renderFooter
// joins the rendered form for display; uiSnapshot reports the plain form —
// both read off this one computation.
func (m *model) buildFooter() (left, right []footerSeg) {
	// Left: the model with its context-window fill (ctx: x%), then the transient
	// last-action notice (primary, auto-hidden after noticeTTL), then the live
	// turn state (quit-armed prompt / thinking shimmer).
	if model := m.modelName; model != "" {
		// Context-window fill sits right after the model name.
		if m.tokens > 0 && m.contextWindow > 0 {
			model += fmt.Sprintf("  (ctx: %d%%)", m.tokens*100/m.contextWindow)
		}
		left = append(left, footerSeg{model, stDim.Render(model)})
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

	// Right side, left-to-right: "? help" and "ctrl+p commands" hints (only at
	// rest, to declutter the footer while actively typing), the "!" danger pill
	// when the shell is unsafe, the live subprocess count (bg: N), then the brand
	// snail glued to the active agent badge (Tab cycles the agent).
	if strings.TrimSpace(m.ta.Value()) == "" {
		right = append(right, footerSeg{"? help", stDim.Render("? help")})
		right = append(right, footerSeg{"ctrl+p commands", stDim.Render("ctrl+p commands")})
	}
	// "!" when the shell is unsafe: runtime :disable_safety, or on_tool_call not
	// enabled in the lua config (unsafe by default).
	if m.safetyOff || !m.safetyConfigured {
		right = append(right, footerSeg{"!", stYolo.Render(" ! ")})
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
