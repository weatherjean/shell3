package patchwidgets

import (
	"time"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// Ask runs an interactive single-line text prompt. It blocks until the
// user submits (Enter), cancels (Esc / Ctrl+C), or the timeout elapses.
//
// Submitting an empty line returns spec.Default if it is non-empty;
// otherwise the empty string is accepted as the value.
func Ask(spec AskSpec) (Result, error) {
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

	var input []rune
	cursor := 0

	render := func() {
		var line string
		if len(input) == 0 && spec.Placeholder != "" {
			line = "  " + dim(spec.Placeholder) + patchtui.CursorMarker
		} else {
			line = "  " + string(input[:cursor]) + patchtui.CursorMarker + string(input[cursor:])
		}
		hint := ""
		if spec.Default != "" {
			hint = "(default: " + spec.Default + ")"
		}
		frame := []string{
			titleLine(spec.Input, hint),
			line,
			hintLine("enter submit", "esc cancel"),
		}
		paintFrame(t, r, frame)
	}

	render()

	deadline := computeDeadline(spec.TimeoutSeconds)
	for {
		k, err := t.readKey(remaining(deadline))
		if err != nil {
			return eofResult(), nil
		}
		switch k.kind {
		case keyEnter:
			value := string(input)
			if value == "" && spec.Default != "" {
				value = spec.Default
			}
			return okResult(value), nil
		case keyEscape, keyCtrlC:
			return cancelResult(), nil
		case keyTimeout:
			return timeoutResult(), nil
		case keyEOF:
			return eofResult(), nil
		case keyBackspace:
			if cursor > 0 {
				input = append(input[:cursor-1], input[cursor:]...)
				cursor--
				render()
			}
		case keyCtrlU:
			input = input[:0]
			cursor = 0
			render()
		case keyCtrlW:
			trimmed := trimToWord(input[:cursor])
			input = append(trimmed, input[cursor:]...)
			cursor = len(trimmed)
			render()
		case keyLeft:
			if cursor > 0 {
				cursor--
				render()
			}
		case keyRight:
			if cursor < len(input) {
				cursor++
				render()
			}
		case keyHome:
			cursor = 0
			render()
		case keyEnd:
			cursor = len(input)
			render()
		case keyChar:
			input = append(input[:cursor], append([]rune{k.r}, input[cursor:]...)...)
			cursor++
			render()
		}
	}
}

// computeDeadline returns the absolute deadline for a timeout in seconds,
// or the zero time if there is no timeout.
func computeDeadline(timeoutSeconds int) time.Time {
	if timeoutSeconds <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(timeoutSeconds) * time.Second)
}

// remaining returns the time left until deadline, or 0 to indicate no
// deadline. If the deadline has passed it returns a tiny positive value
// so the read returns immediately as a timeout.
func remaining(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	d := time.Until(deadline)
	if d <= 0 {
		return time.Millisecond
	}
	return d
}

// paintFrame renders frame to the tty via the renderer (which has been
// pre-pointed at the tty file via [patchtui.Renderer.SetOutput]).
func paintFrame(_ *tty, r *patchtui.Renderer, frame []string) {
	r.Render(frame)
}
