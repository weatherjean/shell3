package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// The screen is exactly: bottom bar (footer) + input + main area (transcript
// viewport) + at most ONE modal on top, replacing the transcript. help
// (helpOpen), the :background modal (bg.open), and the on_tool_call confirm
// prompt (confirm != nil) are three independent features with their own state
// and key handling (helpBox/modals.go, backgroundBox/background.go,
// confirmBox/modals.go) — but they all render through modalBox below, the
// single place that turns a list of lines into the bordered/padded box
// placeModal then centers. No other file builds a modal's outer box.

// modalKind identifies which modal (if any) currently owns the screen. It is
// the single source of truth for both View (what to render) and uiSnapshot
// (what a test/dump reports) — the two can never disagree about what's open.
type modalKind int

const (
	modalNone modalKind = iota
	modalHelp
	modalBackground
	modalConfirm
)

// currentModal reports which modal is open, in priority order (matches
// handleKey's dispatch order: confirm > help > background).
func (m *model) currentModal() modalKind {
	switch {
	case m.confirm != nil:
		return modalConfirm
	case m.helpOpen:
		return modalHelp
	case m.bg.open:
		return modalBackground
	default:
		return modalNone
	}
}

// renderModal builds the content box for the currently open modal. Callers
// center it with placeModal. Returns "" for modalNone (never rendered).
func (m *model) renderModal(kind modalKind) string {
	switch kind {
	case modalConfirm:
		return m.confirmBox()
	case modalHelp:
		return m.helpBox()
	case modalBackground:
		return m.backgroundBox()
	default:
		return ""
	}
}

// modalSelection reports the salient selected row within the open modal, for
// the snapshot: the highlighted job row in :background, which button
// (0=Yes/1=No) in the confirm prompt, or -1 when the open modal (or lack of
// one) has no notion of a selection.
func (m *model) modalSelection() int {
	switch m.currentModal() {
	case modalBackground:
		return m.bg.sel
	case modalConfirm:
		if m.confirmYes {
			return 0
		}
		return 1
	default:
		return -1
	}
}

// modalBox is the ONE styling point every modal renders its final box
// through — same padding/border approach for help, background (list + output
// views), and confirm. width, if >0, fixes the box's content width (the
// background modal's two views need this so long lines wrap consistently
// instead of stretching the box); 0 lets lipgloss size it to the content, which
// is what help/confirm want.
func modalBox(lines []string, vpad, hpad, width int) string {
	st := lipgloss.NewStyle().Padding(vpad, hpad)
	if width > 0 {
		st = st.Width(width)
	}
	return st.Render(strings.Join(lines, "\n"))
}
