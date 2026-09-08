package openai

import (
	"encoding/json"
	"strings"

	"github.com/openai/openai-go/v3"
)

// reasoningFields are the non-standard delta keys OpenAI-compatible providers
// use to stream reasoning beside the answer, in preference order. MiniMax sends
// "reasoning" by default and "reasoning_content" under reasoning_split;
// DeepSeek/Moonshot/llama.cpp use "reasoning_content". Only the first non-empty
// one is taken: some gateways populate BOTH with identical text, and reading
// each in turn would emit the reasoning twice.
var reasoningFields = []string{"reasoning_content", "reasoning", "reasoning_text"}

// deltaReasoning returns the reasoning fragment carried by one chunk delta, via
// the SDK's ExtraFields — openai-go parses unknown keys into
// Delta.JSON.ExtraFields, so no second parse of the response body is needed to
// see them (this replaced a tee'd io.Pipe feeding a whole separate SSE scanner
// on its own goroutine).
func deltaReasoning(d openai.ChatCompletionChunkChoiceDelta) string {
	for _, name := range reasoningFields {
		f, ok := d.JSON.ExtraFields[name]
		if !ok {
			continue
		}
		// Extra fields do not satisfy respjson.Field.Valid(); decode Raw()
		// directly. The provider corpus covers these extension fields.
		raw := f.Raw()
		if raw == "" {
			continue
		}
		var s string
		if err := json.Unmarshal([]byte(raw), &s); err != nil || s == "" {
			continue
		}
		return s
	}
	return ""
}

// visibleContent removes content duplicated by a sibling reasoning delta.
func visibleContent(content, reasoning string) string {
	if reasoning == "" || content == "" {
		return content
	}
	if content == reasoning {
		return ""
	}
	if strings.HasSuffix(content, reasoning) {
		return ""
	}
	return content
}
