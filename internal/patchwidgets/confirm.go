package patchwidgets

import (
	"errors"
	"fmt"
	"io"

	"github.com/weatherjean/shell3/internal/patchtui"
)

// Confirm runs an interactive yes/no prompt. It blocks until the user
// submits (Enter / y / n), cancels (Esc / Ctrl+C), or the timeout elapses.
//
// y/Y selects yes, n/N selects no, Tab/←/→ toggles. Enter on bare input
// returns spec.Default ("yes" or "no"; empty defaults to "no").
func Confirm(spec ConfirmSpec) (Result, error) {
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

	yes := spec.Default == "yes"

	render := func() {
		var yesBtn, noBtn string
		if yes {
			yesBtn = boldP("[ yes ]")
			noBtn = "  " + dim("no") + "  "
		} else {
			yesBtn = "  " + dim("yes") + "  "
			noBtn = boldP("[ no ]")
		}
		frame := []string{
			titleLine(spec.Input, "") + patchtui.CursorMarker,
			"  " + yesBtn + "    " + noBtn,
			hintLine("y/n select", "tab toggle", "enter confirm", "esc cancel"),
		}
		paintFrame(r, frame)
	}

	render()

	deadline := computeDeadline(spec.TimeoutSeconds)
	for {
		k, err := t.readKey(remaining(deadline))
		if err != nil {
			if errors.Is(err, io.EOF) {
				return eofResult(), nil
			}
			return Result{}, fmt.Errorf("patchwidgets: read tty: %w", err)
		}
		switch k.kind {
		case keyEnter:
			return okResult(yes), nil
		case keyEscape, keyCtrlC:
			return cancelResult(), nil
		case keyTimeout:
			return timeoutResult(), nil
		case keyEOF:
			return eofResult(), nil
		case keyTab, keyLeft, keyRight:
			yes = !yes
			render()
		case keyChar:
			switch k.r {
			case 'y', 'Y':
				return okResult(true), nil
			case 'n', 'N':
				return okResult(false), nil
			}
		}
	}
}
