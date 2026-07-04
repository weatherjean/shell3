package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// appendLine marshals v and appends it as a single JSON line to path,
// creating the file when missing. what names the record kind in errors
// ("message", "reminder").
func appendLine(path, what string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("runs: marshal %s: %w", what, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("runs: open %s: %w", what, err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("runs: append %s: %w", what, err)
	}
	return nil
}

// decodeLines unmarshals each non-empty line of a JSONL blob into T. strict
// mode returns the first unmarshal error; lenient mode skips malformed lines
// (a live transcript can end in a half-written tail line).
func decodeLines[T any](raw string, strict bool) ([]T, error) {
	var out []T
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v T
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			if strict {
				return nil, err
			}
			continue
		}
		out = append(out, v)
	}
	return out, nil
}

// ParseMessages leniently parses a messages.jsonl blob (one llm.Message per
// line), skipping blank and malformed lines. For transcript renderers that
// must tolerate a still-streaming file; the strict counterpart is
// Store.LoadMessages.
func ParseMessages(raw string) []llm.Message {
	msgs, _ := decodeLines[llm.Message](raw, false)
	return msgs
}
