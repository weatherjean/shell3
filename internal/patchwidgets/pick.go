package patchwidgets

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// Pick runs an interactive list selector. It blocks until the user picks
// (Enter), cancels (Esc / Ctrl+C), or the timeout elapses.
//
// If spec.Filter is true, typing characters narrows the list by case-
// insensitive substring match against value+label+hint.
func Pick(spec PickSpec) (Result, error) {
	if err := spec.Validate(); err != nil {
		return Result{}, err
	}

	t, err := openTTY()
	if err != nil {
		return Result{}, err
	}
	defer t.Close()

	r := patchtui.New()
	r.SetOutput(t.f)
	defer r.Erase()

	// Visible cursor is owned by the renderer's CursorMarker; while in pick
	// mode we want it hidden because no input field is showing. Keep it
	// parked at the title row and rely on highlight.
	cursor := indexOfDefault(spec.Choices, spec.Default)
	var filter []rune

	render := func() {
		visible := filteredIndexes(spec.Choices, string(filter))
		if cursor >= len(visible) {
			cursor = len(visible) - 1
		}
		if cursor < 0 && len(visible) > 0 {
			cursor = 0
		}

		var lines []string
		hint := fmt.Sprintf("(%d)", len(visible))
		lines = append(lines, titleLine(spec.Input, hint)+patchtui.CursorMarker)

		if spec.Filter {
			lines = append(lines, "  "+muted("/")+string(filter))
		}

		for row, idx := range visible {
			c := spec.Choices[idx]
			line := renderChoice(c, row == cursor)
			lines = append(lines, line)
		}
		if len(visible) == 0 {
			lines = append(lines, "  "+muted("(no matches)"))
		}

		bindings := []string{"↑/↓ move", "enter select", "esc cancel"}
		if spec.Filter {
			bindings = append([]string{"type to filter"}, bindings...)
		}
		lines = append(lines, hintLine(bindings...))
		paintFrame(t, r, lines)
	}

	render()

	deadline := computeDeadline(spec.TimeoutSeconds)
	for {
		k, err := t.readKey(remaining(deadline))
		if err != nil {
			return eofResult(), nil
		}
		visible := filteredIndexes(spec.Choices, string(filter))
		switch k.kind {
		case keyEnter:
			if len(visible) == 0 {
				continue
			}
			if cursor < 0 {
				cursor = 0
			}
			origIdx := visible[cursor]
			c := spec.Choices[origIdx]
			return okIndex(c.Value, origIdx), nil
		case keyEscape, keyCtrlC:
			return cancelResult(), nil
		case keyTimeout:
			return timeoutResult(), nil
		case keyEOF:
			return eofResult(), nil
		case keyUp:
			if cursor > 0 {
				cursor--
				render()
			}
		case keyDown:
			if cursor < len(visible)-1 {
				cursor++
				render()
			}
		case keyHome:
			cursor = 0
			render()
		case keyEnd:
			cursor = len(visible) - 1
			render()
		case keyBackspace:
			if spec.Filter && len(filter) > 0 {
				filter = filter[:len(filter)-1]
				cursor = 0
				render()
			}
		case keyCtrlU:
			if spec.Filter && len(filter) > 0 {
				filter = filter[:0]
				cursor = 0
				render()
			}
		case keyChar:
			if spec.Filter {
				filter = append(filter, k.r)
				cursor = 0
				render()
			}
		}
	}
}

func indexOfDefault(choices []PickChoice, def string) int {
	if def == "" {
		return 0
	}
	for i, c := range choices {
		if c.Value == def {
			return i
		}
	}
	return 0
}

// filteredIndexes returns the indexes of choices that match filter
// (case-insensitive substring), preserving original order. An empty filter
// returns every index.
func filteredIndexes(choices []PickChoice, filter string) []int {
	out := make([]int, 0, len(choices))
	if filter == "" {
		for i := range choices {
			out = append(out, i)
		}
		return out
	}
	lf := strings.ToLower(filter)
	for i, c := range choices {
		hay := strings.ToLower(c.Value + " " + c.Label + " " + c.Hint)
		if strings.Contains(hay, lf) {
			out = append(out, i)
		}
	}
	return out
}

// renderChoice formats one row of the list. The selected row gets a yellow
// arrow + bold label; others are dimmed.
func renderChoice(c PickChoice, selected bool) string {
	label := c.Display()
	if selected {
		line := boldP("›") + " " + label
		if c.Hint != "" {
			line += "  " + muted(c.Hint)
		}
		return line
	}
	line := "  " + dim(label)
	if c.Hint != "" {
		line += "  " + muted(c.Hint)
	}
	return line
}
