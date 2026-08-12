package openai

import (
	"encoding/json"
	"strings"

	"github.com/openai/openai-go"
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
		// Deliberately NOT gated on f.Valid(): respjson.Field.Valid() reports
		// whether a field KNOWN to the struct was present and well-typed, and
		// it is always false for an extra field (verified against openai-go
		// v1.12.0 — an extra whose Raw() is `"dup"` still reports Valid() ==
		// false). Raw() carries the JSON regardless, so decode that and let a
		// decode failure be the rejection.
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

// visibleContent decides how much of a chunk's `content` is answer text, given
// the reasoning fragment that arrived on the SAME delta.
//
// MiniMax on api.minimax.io streams reasoning and answer BOTH through
// `content`, and the only thing distinguishing them is whether the sibling
// reasoning field is set on that delta. Verified across the captured corpus in
// testdata/minimax: in default mode 39 of 40 "content+reasoning" chunks carry
// content byte-identical to the reasoning, and the fortieth is the boundary
// chunk whose content is the reasoning plus its opening "<think>" wrapper.
// Appending every content delta blindly — which is what this adapter used to do
// — therefore interleaves the model's thinking into its own reply, which is why
// answers arrived duplicated and spliced mid-sentence.
//
// The rule: when a delta carries reasoning, its content is that same reasoning
// unless it demonstrably continues past it. Returning "" for the duplicate case
// is what keeps thinking out of the answer.
func visibleContent(content, reasoning string) string {
	if reasoning == "" || content == "" {
		return content
	}
	if content == reasoning {
		return "" // pure duplicate: the common case
	}
	// Boundary chunk: content is the reasoning with a "<think>" wrapper glued
	// on. Suffix-match rather than equality so the wrapper is dropped with it.
	if strings.HasSuffix(content, reasoning) {
		return ""
	}
	// Content genuinely diverges from the reasoning on this delta — keep it and
	// let the leading-tag filter downstream decide about any wrapper.
	return content
}
