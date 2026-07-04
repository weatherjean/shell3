package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// confirmReq is an on_tool_call ask routed to the TUI: the Asker (on the turn
// goroutine) blocks on reply while the model shows a Yes/No modal.
type confirmReq struct {
	command string
	reason  string
	reply   chan bool
}

// confirmMsg delivers a confirmReq into the bubbletea loop via Program.Send.
type confirmMsg struct{ req *confirmReq }

// confirmAbortMsg tells the TUI to dismiss a pending confirm modal that the
// Asker has abandoned (its context was canceled — an ask_timeout fired, or the
// turn was canceled). Without it the modal would linger as a zombie that traps
// every keypress in handleConfirmKey for an ask already resolved as denied.
type confirmAbortMsg struct{ req *confirmReq }

// handleConfirmKey drives the Yes/No on_tool_call modal. Enter confirms the
// selected button (Yes by default); y/n are shortcuts; esc/ctrl+c deny.
func (m *model) handleConfirmKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "left", "h":
		m.confirmYes = true
	case "right", "l":
		m.confirmYes = false
	case "tab":
		m.confirmYes = !m.confirmYes
	case "y", "Y":
		return m.resolveConfirm(true)
	case "n", "N", "esc", "ctrl+c":
		return m.resolveConfirm(false)
	case "enter", " ":
		return m.resolveConfirm(m.confirmYes)
	}
	return m, nil
}

// resolveConfirm replies to the blocked Asker and dismisses the modal.
func (m *model) resolveConfirm(allow bool) (tea.Model, tea.Cmd) {
	if m.confirm != nil {
		m.confirm.reply <- allow // buffered; never blocks
		m.confirm = nil
		if allow {
			m.notice = "command allowed"
		} else {
			m.notice = "command denied"
		}
		m.refresh(false)
	}
	return m, nil
}

// confirmBox renders the on_tool_call Yes/No modal, Yes selected by default.
func (m *model) confirmBox() string {
	selStyle := lipgloss.NewStyle().Foreground(cBlack).Background(cPrimary).Bold(true).Padding(0, 2)
	offStyle := lipgloss.NewStyle().Foreground(cFgDim).Padding(0, 2)
	yes, no := offStyle.Render("Yes"), offStyle.Render("No")
	if m.confirmYes {
		yes = selStyle.Render("Yes")
	} else {
		no = selStyle.Render("No")
	}
	// Content width capped so the box (content + 4-col padding) never exceeds the
	// terminal. Every long/variable line is hard-wrapped to it — a long command,
	// echoed into the gate's reason, otherwise overflows the screen.
	contentW := m.modalWidth(m.width/2, 72)
	if contentW > m.width-4 {
		contentW = m.width - 4
	}
	if contentW < 1 {
		contentW = 1
	}
	wrapLines := func(s string) []string {
		return strings.Split(ansi.Wrap(s, contentW, " "), "\n")
	}
	// Vertical budget so the whole box stays within the screen. Fixed chrome is the
	// header + three blank separators + the buttons row + the footer (6 lines),
	// plus vertical padding (2) = 8. The reason (short — it names the matched rule)
	// gets up to 3 lines; the command gets the rest. Both are truncated with a
	// "… +N more lines" marker rather than overflowing.
	bodyBudget := m.height - 8
	if bodyBudget < 2 {
		bodyBudget = 2
	}
	var reasonKept []string
	reasonMore := 0
	if m.confirm.reason != "" {
		rb := min(3, bodyBudget-1)
		reasonKept, reasonMore = clampLines(wrapLines(m.confirm.reason), rb)
	}
	cmdBudget := bodyBudget - len(reasonKept) - boolToInt(reasonMore > 0)
	if cmdBudget < 1 {
		cmdBudget = 1
	}
	cmdKept, cmdMore := clampLines(wrapLines(m.confirm.command), cmdBudget)

	lines := []string{
		stErr.Render("⚠ command gate") + stDim.Render("  allow this command?"),
		"",
		lipgloss.NewStyle().Foreground(cFg).Render(strings.Join(cmdKept, "\n")),
	}
	if cmdMore > 0 {
		lines = append(lines, stDim.Render(fmt.Sprintf("… +%d more lines", cmdMore)))
	}
	if len(reasonKept) > 0 {
		lines = append(lines, stDim.Render(strings.Join(reasonKept, "\n")))
		if reasonMore > 0 {
			lines = append(lines, stDim.Render(fmt.Sprintf("… +%d more lines", reasonMore)))
		}
	}
	lines = append(lines,
		"",
		yes+"  "+no,
		"",
		stDim.Render(ansi.Wrap("y/enter allow · n/esc deny · ←→ select", contentW, " ")),
	)
	return lipgloss.NewStyle().
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
}

// helpBox renders the keybinding/command reference shown by '?'.
func (m *model) helpBox() string {
	key := lipgloss.NewStyle().Foreground(cPrimary).Bold(true)
	desc := lipgloss.NewStyle().Foreground(cFgDim)
	head := lipgloss.NewStyle().Foreground(cReason).Bold(true)
	row := func(k, d string) string {
		return key.Render(fmt.Sprintf(" %-12s", k)) + desc.Render(d)
	}
	lines := []string{
		stBrand.Render("shell3 — keys"),
		"",
		head.Render("NORMAL"),
		row("j / k", "move cursor"),
		row("{ / }", "previous / next block"),
		row("gg / G", "top / bottom (G locks autoscroll)"),
		row("ctrl+d / u", "half-page down / up"),
		row("enter", "fold / unfold block"),
		row("zM / zR", "fold / unfold all"),
		row("y / dd", "copy block / clear input"),
		row("mouse", "drag selects + copies · click folds · wheel scrolls"),
		row("i / a", "insert mode"),
		row("tab", "cycle agent (any mode)"),
		row(": / ?", "command mode / help"),
		"",
		head.Render("INSERT"),
		row("enter", "send"),
		row("ctrl+j", "newline (shift+enter if supported)"),
		row("ctrl+o", "compose in $EDITOR (:edit)"),
		row("ctrl+u", "clear input"),
		row("esc", "normal mode (keeps draft)"),
		"",
		head.Render("COMMAND"),
	}
	// Command reference derived from exCommands (the single source of truth), so
	// it can never drift from what the palette lists and runCommand handles.
	for _, l := range commandRefLines(4) {
		lines = append(lines, desc.Render(l))
	}
	lines = append(lines,
		"",
		desc.Render(" ctrl+c: cancel turn / quit   ·   any key: close"),
	)
	return lipgloss.NewStyle().
		Padding(0, 1).
		Render(strings.Join(lines, "\n"))
}

// clampLines caps a wrapped block to maxLines, reserving the last line for the
// "… +N more lines" marker the caller renders. Returns the kept lines and the
// count of dropped lines (0 when nothing was truncated).
func clampLines(lines []string, maxLines int) (kept []string, more int) {
	if maxLines < 1 {
		maxLines = 1
	}
	if len(lines) <= maxLines {
		return lines, 0
	}
	return lines[:maxLines-1], len(lines) - (maxLines - 1)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
