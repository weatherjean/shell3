package codex

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestParseStreamReasoningRoundtrip(t *testing.T) {
	sse := `data: {"type":"response.output_item.done","item":{"id":"rs_1","type":"reasoning","encrypted_content":"AAA","summary":[]}}

data: {"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}}

`
	var blob []byte
	var done bool
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if len(ev.ProviderReasoning) > 0 {
			blob = ev.ProviderReasoning
		}
		if ev.Done {
			done = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("missing Done event")
	}
	if !strings.Contains(string(blob), `"encrypted_content":"AAA"`) {
		t.Fatalf("reasoning blob missing encrypted_content: %s", blob)
	}
}
