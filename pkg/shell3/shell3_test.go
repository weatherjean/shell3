package shell3

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
)

func TestTranslate(t *testing.T) {
	cases := []struct {
		name string
		in   chat.Event
		want *Event // nil means "dropped"
	}{
		{
			name: "token",
			in:   chat.Event{Kind: chat.EventAssistantToken, Text: "hello"},
			want: &Event{Kind: Token, Text: "hello"},
		},
		{
			name: "tool result",
			in:   chat.Event{Kind: chat.EventToolResult, ToolName: "bash", ToolOutput: "ok"},
			want: &Event{Kind: ToolResult, ToolName: "bash", ToolOutput: "ok"},
		},
		{
			name: "error",
			in:   chat.Event{Kind: chat.EventError, Text: "boom"},
			want: &Event{Kind: Error},
		},
		{
			name: "turn done",
			in:   chat.Event{Kind: chat.EventTurnDone},
			want: &Event{Kind: Done},
		},
		{
			name: "reasoning dropped",
			in:   chat.Event{Kind: chat.EventAssistantReasoning, Text: "thinking"},
			want: nil,
		},
		{
			name: "tool call dropped",
			in:   chat.Event{Kind: chat.EventToolCall, ToolName: "bash"},
			want: nil,
		},
		{
			name: "usage dropped",
			in:   chat.Event{Kind: chat.EventUsage},
			want: nil,
		},
		{
			name: "session start dropped",
			in:   chat.Event{Kind: chat.EventSessionStart},
			want: nil,
		},
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
				t.Fatal("expected an event, got drop")
			}
			if got.Kind != tc.want.Kind || got.Text != tc.want.Text ||
				got.ToolName != tc.want.ToolName || got.ToolOutput != tc.want.ToolOutput {
				t.Fatalf("translate(%+v) = %+v, want %+v", tc.in, got, *tc.want)
			}
			if tc.want.Kind == Error {
				if got.Err == nil || got.Err.Error() != tc.in.Text {
					t.Fatalf("error event: got Err=%v, want %q", got.Err, tc.in.Text)
				}
			}
		})
	}
}
