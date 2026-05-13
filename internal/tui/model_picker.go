package tui

import (
	"strings"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/internal/patchwidgets"
)

// terminalReleaser releases the TUI terminal for the duration of fn.
// Implemented by *patchapp.App (which has WithReleasedTerminal).
type terminalReleaser interface {
	WithReleasedTerminal(fn func())
}

const modelChoiceSep = "\x1f"

func encodeChoice(c chat.ModelChoice) string { return c.Provider + modelChoiceSep + c.Model }

func decodeChoice(v string) (chat.ModelChoice, bool) {
	parts := strings.SplitN(v, modelChoiceSep, 2)
	if len(parts) != 2 {
		return chat.ModelChoice{}, false
	}
	return chat.ModelChoice{Provider: parts[0], Model: parts[1]}, true
}

// pickModel releases the TUI terminal, runs the patchwidgets list selector
// over models, and returns the selected ModelChoice. The bool is false on
// cancel, timeout, error, or an empty pool.
func pickModel(app terminalReleaser, models []chat.ModelChoice, curProvider, curModel string) (chat.ModelChoice, bool) {
	if len(models) == 0 {
		return chat.ModelChoice{}, false
	}
	if len(models) == 1 {
		return models[0], true
	}

	choices := make([]patchwidgets.PickChoice, 0, len(models))
	var defVal string
	for _, m := range models {
		val := encodeChoice(m)
		hint := m.Provider
		if m.Provider == curProvider && m.Model == curModel {
			hint = m.Provider + " · current"
			defVal = val
		}
		choices = append(choices, patchwidgets.PickChoice{
			Value: val,
			Label: m.Model,
			Hint:  hint,
		})
	}

	var (
		res patchwidgets.Result
		err error
	)
	app.WithReleasedTerminal(func() {
		res, err = patchwidgets.Pick(patchwidgets.PickSpec{
			Input:   "Switch model",
			Choices: choices,
			Default: defVal,
			Filter:  len(models) > 6,
		})
	})
	if err != nil || !res.OK {
		return chat.ModelChoice{}, false
	}
	v, _ := res.Value.(string)
	return decodeChoice(v)
}
