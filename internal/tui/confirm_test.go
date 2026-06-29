package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// When the Asker abandons a pending ask (ask_timeout fired, or the turn was
// canceled) it sends a confirmAbortMsg. That must dismiss the matching modal so
// the keyboard isn't trapped on a zombie prompt — but only the matching one.
func TestConfirm_AbortDismissesMatchingModal(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	req := &confirmReq{command: "rm -rf x", reason: "matches a bash_safety deny rule: rm", reply: make(chan bool, 1)}
	m.Update(confirmMsg{req: req})
	if m.confirm == nil {
		t.Fatal("confirmMsg should open the modal")
	}
	// An abort for a different (stale) req must not dismiss the current modal.
	m.Update(confirmAbortMsg{req: &confirmReq{}})
	if m.confirm == nil {
		t.Fatal("abort for a different req must not dismiss the live modal")
	}
	// An abort for the pending req dismisses it.
	m.Update(confirmAbortMsg{req: req})
	if m.confirm != nil {
		t.Fatal("abort for the pending req should dismiss the modal (no zombie)")
	}
}

// A command with many wrapped lines must not make the modal taller than the
// screen — it was overflowing vertically (and the corrupted render made esc-deny
// look like it broke the agent).
func TestConfirmBox_FitsHeight(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&sb, "step-%d && ", i)
	}
	for _, h := range []int{24, 16, 40} {
		m := newModel(closedSend(nil), nil, "", "")
		m.Update(tea.WindowSizeMsg{Width: 80, Height: h})
		m.confirm = &confirmReq{command: sb.String(), reason: "matches a bash_safety deny rule: step"}
		box := m.confirmBox()
		if got := strings.Count(box, "\n") + 1; got > h {
			t.Fatalf("height %d: confirm box is %d lines (overflows):\n%s", h, got, stripANSI(box))
		}
		if !strings.Contains(stripANSI(box), "more lines") {
			t.Fatalf("height %d: a truncated box should show a '… more lines' marker", h)
		}
	}
}

// Arrow keys are directional (left=Yes, right=No); tab toggles. Right used to
// toggle, so pressing it twice flipped back to Yes — a two-button modal should
// let an arrow commit to a side.
func TestConfirmNav_DirectionalArrowsTabToggles(t *testing.T) {
	m := newModel(closedSend(nil), nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m.confirm = &confirmReq{command: "rm -rf x", reason: "matches a bash_safety deny rule: rm"}
	m.confirmYes = true
	m.handleConfirmKey("right")
	if m.confirmYes {
		t.Fatal("right should select No")
	}
	m.handleConfirmKey("right") // idempotent: still No, does not flip back
	if m.confirmYes {
		t.Fatal("right pressed twice must stay No")
	}
	m.handleConfirmKey("left")
	if !m.confirmYes {
		t.Fatal("left should select Yes")
	}
	m.handleConfirmKey("tab")
	if m.confirmYes {
		t.Fatal("tab should toggle Yes→No")
	}
}

// The bash_safety confirm modal must never render wider than the terminal: a long
// command (echoed into the gate's reason) used to produce one un-wrapped line that
// overflowed the screen.
func TestConfirmBox_FitsWidth(t *testing.T) {
	long := "find . -maxdepth 4 -name '*.go' -type f -path '*/internal/*' -not -path '*/vendor/*' | xargs rg something-fairly-long-here"
	for _, w := range []int{80, 60, 100} {
		m := newModel(closedSend(nil), nil, "", "")
		m.Update(tea.WindowSizeMsg{Width: w, Height: 24})
		m.confirm = &confirmReq{
			command: long,
			reason:  "matches a bash_safety deny rule: " + long,
		}
		for _, line := range strings.Split(m.confirmBox(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: confirm box line overflows by %d cols: %q", w, got-w, stripANSI(line))
			}
		}
	}
}
