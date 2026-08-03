package runs

import (
	"encoding/json"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// ParseMessages leniently parses a JSONL transcript blob (one llm.Message per
// line — the format Store.Transcript emits), skipping blank and malformed
// lines. For transcript renderers.
func ParseMessages(raw string) []llm.Message {
	var out []llm.Message
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var m llm.Message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}
