package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

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
			reason:  "not on the bash_safety allowlist: " + long,
		}
		for _, line := range strings.Split(m.confirmBox(), "\n") {
			if got := lipgloss.Width(line); got > w {
				t.Fatalf("width %d: confirm box line overflows by %d cols: %q", w, got-w, stripANSI(line))
			}
		}
	}
}
