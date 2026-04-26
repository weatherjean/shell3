package chat

import (
	"strings"

	"github.com/weatherjean/shell3/internal/patchwidgets"
)

// terminalReleaser releases the TUI terminal for the duration of fn.
// Implemented by *patchapp.App (which has WithReleasedTerminal).
type terminalReleaser interface {
	WithReleasedTerminal(fn func())
}

// pickModel releases the TUI terminal, runs the patchwidgets list selector
// over models, and returns the selected name. The bool is false on cancel,
// timeout, error, or an empty pool.
func pickModel(app terminalReleaser, models []string, current string) (string, bool) {
	if len(models) == 0 {
		return "", false
	}
	if len(models) == 1 {
		return models[0], true
	}

	choices := make([]patchwidgets.PickChoice, 0, len(models))
	for _, m := range models {
		hint := ""
		if m == current {
			hint = "current"
		}
		choices = append(choices, patchwidgets.PickChoice{Value: m, Hint: hint})
	}

	var (
		res patchwidgets.Result
		err error
	)
	app.WithReleasedTerminal(func() {
		res, err = patchwidgets.Pick(patchwidgets.PickSpec{
			Input:   "Switch model",
			Choices: choices,
			Default: current,
			Filter:  len(models) > 6,
		})
	})
	if err != nil || !res.OK {
		return "", false
	}
	v, _ := res.Value.(string)
	return v, v != ""
}

// currentModel extracts the model segment from a status line shaped as
// "<provider> │ <model>". Returns "" if no separator is found.
func currentModel(statusLine string) string {
	parts := strings.SplitN(statusLine, " │ ", 2)
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
