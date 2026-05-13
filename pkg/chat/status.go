package chat

import "strings"

// SplitStatus extracts (provider, model) from a status line shaped as
// "<provider> │ <model>" or "<provider> │ <model> │ <effort>". Returns
// ("", "") if no separator is found.
func SplitStatus(statusLine string) (string, string) {
	parts := strings.SplitN(statusLine, " │ ", 3)
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// FormatStatus builds a status line "<provider> │ <model>" with " │ <effort>"
// appended when effort is non-empty.
func FormatStatus(provider, model, effort string) string {
	out := provider + " │ " + model
	if effort != "" {
		out += " │ " + effort
	}
	return out
}

// ResolveModelArg parses /model arg as either "provider/model" or bare "model".
// Bare model resolves within curProvider first, then any provider.
func ResolveModelArg(models []ModelChoice, arg, curProvider string) (ModelChoice, bool) {
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
