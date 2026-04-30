// Package models provides context window lookups from a bundled snapshot of
// the models.dev database (https://github.com/sst/models.dev).
// Run `make models-snapshot` to refresh snapshot.json from the live API.
package models

import (
	_ "embed"
	"encoding/json"
)

//go:embed snapshot.json
var snapshotJSON []byte

// contextWindows maps model ID → context window size in tokens.
// Keys include both bare names (e.g. "gpt-4o") and provider-prefixed
// names (e.g. "openai/gpt-4o") as returned by models.dev.
var contextWindows map[string]int

func init() {
	_ = json.Unmarshal(snapshotJSON, &contextWindows)
}

// ContextWindow returns the context window size for the given model ID,
// or 0 if unknown. Tries bare name first, then common provider prefixes.
func ContextWindow(model string) int {
	if n, ok := contextWindows[model]; ok {
		return n
	}
	for _, prefix := range []string{"openai/", "anthropic/", "google/", "meta/"} {
		if n, ok := contextWindows[prefix+model]; ok {
			return n
		}
	}
	return 0
}
