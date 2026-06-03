package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want *Event // nil = dropped
	}{
		{"token", chat.Event{Kind: chat.EventAssistantToken, Text: "hi"}, &Event{Kind: Token, Text: "hi"}},
		{"reasoning", chat.Event{Kind: chat.EventAssistantReasoning, Text: "think"}, &Event{Kind: Reasoning, Text: "think"}},
		{"tool call", chat.Event{Kind: chat.EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}, &Event{Kind: ToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`}},
		{"tool result", chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"}, &Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"}},
		{"usage", chat.Event{Kind: chat.EventUsage, Usage: &chat.EventUsageData{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}}, &Event{Kind: Usage, PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15}},
		{"done", chat.Event{Kind: chat.EventTurnDone, Usage: &chat.EventUsageData{PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}}, &Event{Kind: Done, PromptTokens: 20, CompletionTokens: 8, TotalTokens: 28}},
		{"retry", chat.Event{Kind: chat.EventRetry, Text: "retrying"}, &Event{Kind: Retry, Text: "retrying"}},
		{"error", chat.Event{Kind: chat.EventError, Text: "boom"}, &Event{Kind: Error}},
		{"session start dropped", chat.Event{Kind: chat.EventSessionStart}, nil},
		{"user message dropped", chat.Event{Kind: chat.EventUserMessage}, nil},
		{"assistant message dropped", chat.Event{Kind: chat.EventAssistantMessage}, nil},
		{"system reminder dropped", chat.Event{Kind: chat.EventSystemReminder}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := translate(tc.in)
			if tc.want == nil {
				if ok {
					t.Fatalf("expected drop, got %+v", got)
				}
				return
			}
			if !ok {
				t.Fatal("expected event, got drop")
			}
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName || got.ToolInput != tc.want.ToolInput ||
				got.ToolOutput != tc.want.ToolOutput || got.PromptTokens != tc.want.PromptTokens ||
				got.CompletionTokens != tc.want.CompletionTokens || got.TotalTokens != tc.want.TotalTokens {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, *tc.want)
			}
			if tc.want.Kind == Error && (got.Err == nil || got.Err.Error() != tc.in.Text) {
				t.Fatalf("error: got Err=%v want %q", got.Err, tc.in.Text)
			}
		})
	}
}
