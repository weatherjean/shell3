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

const modelChoiceSep = "\x1f"

func encodeChoice(c ModelChoice) string { return c.Provider + modelChoiceSep + c.Model }

func decodeChoice(v string) (ModelChoice, bool) {
	parts := strings.SplitN(v, modelChoiceSep, 2)
	if len(parts) != 2 {
		return ModelChoice{}, false
	}
	return ModelChoice{Provider: parts[0], Model: parts[1]}, true
}

// pickModel releases the TUI terminal, runs the patchwidgets list selector
// over models, and returns the selected ModelChoice. The bool is false on
// cancel, timeout, error, or an empty pool.
func pickModel(app terminalReleaser, models []ModelChoice, curProvider, curModel string) (ModelChoice, bool) {
	if len(models) == 0 {
		return ModelChoice{}, false
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
		return ModelChoice{}, false
	}
	v, _ := res.Value.(string)
	return decodeChoice(v)
}

// splitStatus extracts (provider, model) from a status line shaped as
// "<provider> │ <model>". Returns ("", "") if no separator is found.
func splitStatus(statusLine string) (string, string) {
	parts := strings.SplitN(statusLine, " │ ", 2)
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// resolveModelArg parses /model arg as either "provider/model" or bare "model".
// Bare model resolves within curProvider first, then any provider.
func resolveModelArg(models []ModelChoice, arg, curProvider string) (ModelChoice, bool) {
	if i := strings.IndexByte(arg, '/'); i > 0 {
		prov, model := arg[:i], arg[i+1:]
		for _, m := range models {
			if m.Provider == prov && m.Model == model {
				return m, true
			}
		}
		return ModelChoice{Provider: prov, Model: model}, true
	}
	for _, m := range models {
		if m.Provider == curProvider && m.Model == arg {
			return m, true
		}
	}
	for _, m := range models {
		if m.Model == arg {
			return m, true
		}
	}
	if curProvider != "" {
		return ModelChoice{Provider: curProvider, Model: arg}, true
	}
	return ModelChoice{}, false
}
