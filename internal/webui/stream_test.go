//go:build unix

package webui

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/shell3"
)

// chunks parses the SSE body a stream produced into decoded protocol chunks.
func chunks(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(body, "\n") {
		data, ok := strings.CutPrefix(line, "data: ")
		if !ok || data == "[DONE]" {
			continue
		}
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Fatalf("bad chunk %q: %v", data, err)
		}
		out = append(out, chunk)
	}
	return out
}

func types(chunks []map[string]any) []string {
	out := make([]string, 0, len(chunks))
	for _, c := range chunks {
		out = append(out, c["type"].(string))
	}
	return out
}

func drain(t *testing.T, events []shell3.Event) (string, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	stream, ok := newStreamWriter(rec)
	if !ok {
		t.Fatal("recorder should support flushing")
	}
	ch := make(chan shell3.Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	_, errText := pumpUsage(stream, ch, nil)
	stream.finish()
	return rec.Body.String(), errText != ""
}

func TestPumpBracketsTextDeltas(t *testing.T) {
	body, sawError := drain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "Hello "},
		{Kind: shell3.Token, Text: "world"},
	})
	if sawError {
		t.Error("no error event was sent, but pump reported one")
	}

	got := types(chunks(t, body))
	want := []string{"text-start", "text-delta", "text-delta", "text-end", "finish"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chunk types = %v, want %v", got, want)
	}
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Error("stream must terminate with [DONE]")
	}
}

// A tool call mid-reply must close the open text block, so the client renders
// prose, then the tool, then a fresh block of prose.
func TestPumpClosesTextBlockAroundToolCall(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "checking"},
		{Kind: shell3.ToolCall, ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls"}`},
		{Kind: shell3.ToolResult, ToolCallID: "c1", ToolOutput: "a.txt"},
		{Kind: shell3.Token, Text: "done"},
	})

	parsed := chunks(t, body)
	got := types(parsed)
	want := []string{
		"text-start", "text-delta", "text-end",
		"tool-input-available", "tool-output-available",
		"text-start", "text-delta", "text-end", "finish",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("chunk types = %v, want %v", got, want)
	}

	// The two text blocks must carry different ids, or the client merges them.
	var ids []string
	for _, c := range parsed {
		if c["type"] == "text-start" {
			ids = append(ids, c["id"].(string))
		}
	}
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Errorf("text block ids = %v, want two distinct ids", ids)
	}

	// Tool args are JSON and must arrive structured, not string-wrapped.
	for _, c := range parsed {
		if c["type"] == "tool-input-available" {
			input, ok := c["input"].(map[string]any)
			if !ok {
				t.Fatalf("tool input = %#v, want an object", c["input"])
			}
			if input["command"] != "ls" {
				t.Errorf("tool input command = %v, want ls", input["command"])
			}
		}
	}
}

func TestPumpMalformedToolArgsFallBackToString(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.ToolCall, ToolCallID: "c1", ToolName: "bash", ToolInput: "not json"},
	})
	for _, c := range chunks(t, body) {
		if c["type"] == "tool-input-available" && c["input"] != "not json" {
			t.Errorf("tool input = %#v, want the raw string", c["input"])
		}
	}
}

// A tool result must pair with its call even when the runtime supplies no id.
func TestPumpPairsUnidentifiedToolCalls(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.ToolCall, ToolName: "bash", ToolInput: "{}"},
		{Kind: shell3.ToolResult, ToolOutput: "ok"},
	})

	var callID, resultID string
	for _, c := range chunks(t, body) {
		switch c["type"] {
		case "tool-input-available":
			callID = c["toolCallId"].(string)
		case "tool-output-available":
			resultID = c["toolCallId"].(string)
		}
	}
	if callID == "" || callID != resultID {
		t.Errorf("call id %q and result id %q must match", callID, resultID)
	}
}

func TestPumpReportsToolErrors(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.ToolCall, ToolCallID: "c1", ToolName: "bash", ToolInput: "{}"},
		{Kind: shell3.ToolResult, ToolCallID: "c1", ToolOutput: "boom", ToolError: true},
	})
	found := false
	for _, c := range chunks(t, body) {
		if c["type"] == "tool-output-error" && c["errorText"] == "boom" {
			found = true
		}
	}
	if !found {
		t.Error("a failed tool must produce tool-output-error")
	}
}

func TestPumpSurfacesTurnErrors(t *testing.T) {
	body, sawError := drain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "partial"},
		{Kind: shell3.Error, Err: errors.New("model refused")},
	})
	if !sawError {
		t.Error("pump must report that an error event arrived")
	}

	parsed := chunks(t, body)
	got := types(parsed)
	// The open text block closes before the error, so no delta is orphaned.
	want := []string{"text-start", "text-delta", "text-end", "error", "finish"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("chunk types = %v, want %v", got, want)
	}
	for _, c := range parsed {
		if c["type"] == "error" && !strings.Contains(c["errorText"].(string), "model refused") {
			t.Errorf("error text = %q, want the underlying message", c["errorText"])
		}
	}
}

// Host narration (retries, compaction) streams as TRANSIENT data parts: the
// protocol's channel for status the user should see live but that must never
// enter the message history. System reminders stay out of the chat entirely.
func TestPumpStreamsHostNarrationAsTransientData(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.Retry, Text: "attempt 2"},
		{Kind: shell3.Compacted, Text: "summarized 40 messages"},
		{Kind: shell3.SystemReminder, Text: "reminder"},
	})

	var notices []map[string]any
	for _, c := range chunks(t, body) {
		if c["type"] == "data-notice" {
			notices = append(notices, c)
		}
	}
	if len(notices) != 2 {
		t.Fatalf("data-notice count = %d, want 2 (retry + compacted)", len(notices))
	}
	for _, n := range notices {
		if n["transient"] != true {
			t.Errorf("notice %v must be transient, or it pollutes the transcript", n)
		}
		data, ok := n["data"].(map[string]any)
		if !ok {
			t.Fatalf("notice data = %#v, want an object", n["data"])
		}
		if data["kind"] != "retry" && data["kind"] != "compacted" {
			t.Errorf("notice kind = %v, want retry or compacted", data["kind"])
		}
	}
	if strings.Contains(body, "reminder") {
		t.Error("system reminders must not reach the chat stream")
	}
}

// Token usage streams as message metadata, the protocol's per-message facts
// channel — assistant-ui's useThreadTokenUsage reads message.metadata.usage.
// Done carries the final totals and must win over interim Usage events.
func TestPumpEmitsUsageAsMessageMetadata(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.Usage, PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
		{Kind: shell3.Done, PromptTokens: 100, CompletionTokens: 25, TotalTokens: 125, CachedTokens: 80},
	})

	var last map[string]any
	for _, c := range chunks(t, body) {
		if c["type"] == "message-metadata" {
			last = c
		}
	}
	if last == nil {
		t.Fatal("no message-metadata chunk in the stream")
	}
	usage, ok := last["messageMetadata"].(map[string]any)["usage"].(map[string]any)
	if !ok {
		t.Fatalf("messageMetadata = %#v, want {usage: {...}}", last["messageMetadata"])
	}
	if usage["inputTokens"] != float64(100) || usage["outputTokens"] != float64(25) ||
		usage["totalTokens"] != float64(125) || usage["cachedInputTokens"] != float64(80) {
		t.Errorf("usage = %#v, want the Done event's totals", usage)
	}
}

func TestPumpReasoningUsesItsOwnBlock(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.Reasoning, Text: "thinking"},
		{Kind: shell3.Token, Text: "answer"},
	})
	got := types(chunks(t, body))
	want := []string{
		"reasoning-start", "reasoning-delta", "reasoning-end",
		"text-start", "text-delta", "text-end", "finish",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("chunk types = %v, want %v", got, want)
	}
}

func TestLastUserTextReadsNewestUserMessage(t *testing.T) {
	req := chatRequest{Messages: []chatMessage{
		{Role: "user", Parts: []chatPart{{Type: "text", Text: "first"}}},
		{Role: "assistant", Parts: []chatPart{{Type: "text", Text: "reply"}}},
		{Role: "user", Parts: []chatPart{
			{Type: "text", Text: "second"},
			{Type: "text", Text: "line"},
		}},
	}}
	if got := req.lastUserText(); got != "second\nline" {
		t.Errorf("lastUserText() = %q, want %q", got, "second\nline")
	}
}

func TestLastUserTextEmptyWithoutUserMessage(t *testing.T) {
	req := chatRequest{Messages: []chatMessage{
		{Role: "assistant", Parts: []chatPart{{Type: "text", Text: "hi"}}},
	}}
	if got := req.lastUserText(); got != "" {
		t.Errorf("lastUserText() = %q, want empty", got)
	}
}

// A turn stopped mid-tool leaves its last call unanswered. The client reads an
// unanswered call as one IT has to resolve, and offers the user Allow/Deny for
// a command that was already killed — so the stream has to close the call out.
func TestPumpAnswersToolCallsLeftOpen(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.ToolCall, ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"sleep 40"}`},
	})

	got := types(chunks(t, body))
	want := []string{"tool-input-available", "tool-output-available", "finish"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("chunk types = %v, want %v", got, want)
	}
}

// ...but a call that WAS answered must not be answered twice.
func TestPumpDoesNotReanswerFinishedToolCalls(t *testing.T) {
	body, _ := drain(t, []shell3.Event{
		{Kind: shell3.ToolCall, ToolCallID: "c1", ToolName: "bash", ToolInput: `{}`},
		{Kind: shell3.ToolResult, ToolCallID: "c1", ToolOutput: "done"},
	})

	outputs := 0
	for _, c := range chunks(t, body) {
		if c["type"] == "tool-output-available" {
			outputs++
		}
	}
	if outputs != 1 {
		t.Errorf("tool-output-available count = %d, want 1", outputs)
	}
}

// Pressing stop is not a failure. Reporting "context canceled" in the chat's
// error style tells the user something broke, when they are the one who
// stopped it.
func TestPumpReportsACancelledTurnAsAStop(t *testing.T) {
	body, sawError := drain(t, []shell3.Event{
		{Kind: shell3.Token, Text: "partial"},
		{Kind: shell3.Error, Err: context.Canceled},
	})
	if sawError {
		t.Error("a cancelled turn must not count as a failed one")
	}
	if strings.Contains(body, "context canceled") {
		t.Error("the raw cancellation error must not reach the chat")
	}
	if !strings.Contains(body, "Stopped") {
		t.Errorf("body should say the turn was stopped: %s", body)
	}
}
