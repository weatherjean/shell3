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

func TestParseStreamTextDelta(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\", world\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var text strings.Builder
	var done bool
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		text.WriteString(ev.TextDelta)
		if ev.Done {
			done = true
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Hello, world" {
		t.Fatalf("text: got %q", text.String())
	}
	if !done {
		t.Fatal("missing Done event")
	}
}

func TestParseStreamReasoningDeltas(t *testing.T) {
	sse := "data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking\"}\n" +
		"data: {\"type\":\"response.reasoning_text.delta\",\"delta\":\" more\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var reasoning strings.Builder
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		reasoning.WriteString(ev.ReasoningDelta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if reasoning.String() != "thinking more" {
		t.Fatalf("reasoning: got %q", reasoning.String())
	}
}

func TestParseStreamToolCallViaDeltas(t *testing.T) {
	sse := "data: {\"type\":\"response.output_item.added\",\"item_id\":\"i1\",\"item\":{\"id\":\"i1\",\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"bash\"}}\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"i1\",\"delta\":\"{\\\"cmd\\\":\"}\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"i1\",\"delta\":\"\\\"ls\\\"}\"}\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"i1\",\"type\":\"function_call\",\"call_id\":\"c1\",\"name\":\"bash\",\"arguments\":\"\"}}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var calls []*llm.ToolCall
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.ToolCall != nil {
			calls = append(calls, ev.ToolCall)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("name: got %q", calls[0].Name)
	}
	if calls[0].ID != "c1" {
		t.Fatalf("id: got %q", calls[0].ID)
	}
	if !strings.Contains(calls[0].RawArgs, "ls") {
		t.Fatalf("args: got %q", calls[0].RawArgs)
	}
}

func TestParseStreamToolCallDoneOnly(t *testing.T) {
	// Server emits a fully-formed tool call only in output_item.done with no delta events.
	sse := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"i2\",\"type\":\"function_call\",\"call_id\":\"c2\",\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"/tmp/x\\\"}\" }}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var calls []*llm.ToolCall
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.ToolCall != nil {
			calls = append(calls, ev.ToolCall)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "read_file" {
		t.Fatalf("name: got %q", calls[0].Name)
	}
	if !strings.Contains(calls[0].RawArgs, "/tmp/x") {
		t.Fatalf("args: got %q", calls[0].RawArgs)
	}
}

func TestParseStreamFlushOrphanTools(t *testing.T) {
	// Tool call started via output_item.added + deltas but no output_item.done emitted.
	sse := "data: {\"type\":\"response.output_item.added\",\"item_id\":\"i3\",\"item\":{\"id\":\"i3\",\"type\":\"function_call\",\"call_id\":\"c3\",\"name\":\"orphan\"}}\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"i3\",\"delta\":\"{\\\"x\\\":1}\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var calls []*llm.ToolCall
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.ToolCall != nil {
			calls = append(calls, ev.ToolCall)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 flushed orphan tool, got %d", len(calls))
	}
	if calls[0].Name != "orphan" {
		t.Fatalf("name: got %q", calls[0].Name)
	}
}

func TestParseStreamUsage(t *testing.T) {
	sse := "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":20,\"total_tokens\":30}}}\n"

	var usage *llm.Usage
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.Usage != nil {
			usage = ev.Usage
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage == nil || usage.PromptTokens != 10 || usage.CompletionTokens != 20 || usage.TotalTokens != 30 {
		t.Fatalf("usage: got %+v", usage)
	}
}

func TestParseStreamError(t *testing.T) {
	sse := "data: {\"type\":\"error\",\"error\":{\"message\":\"rate limited\",\"code\":\"rate_limit\"}}\n"
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("expected error containing 'rate limited', got %v", err)
	}
}

func TestParseStreamErrorFallback(t *testing.T) {
	sse := "data: {\"type\":\"response.failed\",\"error\":{}}\n"
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {})
	if err == nil || !strings.Contains(err.Error(), "unspecified") {
		t.Fatalf("expected fallback error, got %v", err)
	}
}

func TestParseStreamBadJSONIgnored(t *testing.T) {
	sse := "data: {not valid json}\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var text strings.Builder
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		text.WriteString(ev.TextDelta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "ok" {
		t.Fatalf("expected good line processed after bad JSON, got %q", text.String())
	}
}

func TestParseStreamUnknownTypeIgnored(t *testing.T) {
	sse := "data: {\"type\":\"future.unknown.event\",\"data\":\"whatever\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var done bool
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.Done {
			done = true
		}
	})
	if err != nil {
		t.Fatalf("unexpected error on unknown event type: %v", err)
	}
	if !done {
		t.Fatal("missing Done event")
	}
}

func TestParseStreamNonDataLinesIgnored(t *testing.T) {
	sse := ": keep-alive\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"hi\"}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var text strings.Builder
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		text.WriteString(ev.TextDelta)
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "hi" {
		t.Fatalf("got %q", text.String())
	}
}

func TestParseStreamMultipleToolCalls(t *testing.T) {
	sse := "data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"a\",\"type\":\"function_call\",\"call_id\":\"ca\",\"name\":\"toolA\",\"arguments\":\"{}\"}}\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"b\",\"type\":\"function_call\",\"call_id\":\"cb\",\"name\":\"toolB\",\"arguments\":\"{}\"}}\n" +
		"data: {\"type\":\"response.completed\",\"response\":{}}\n"

	var calls []*llm.ToolCall
	err := parseStream(strings.NewReader(sse), func(ev llm.StreamEvent) {
		if ev.ToolCall != nil {
			calls = append(calls, ev.ToolCall)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(calls))
	}
	names := map[string]bool{}
	for _, c := range calls {
		names[c.Name] = true
	}
	if !names["toolA"] || !names["toolB"] {
		t.Fatalf("expected toolA and toolB, got %v", names)
	}
}
