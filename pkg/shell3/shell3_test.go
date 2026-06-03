package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/persona"
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

func TestRunConfig_StreamsToDone(t *testing.T) {
	client := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hello"},
			{TextDelta: " world"},
		},
	})
	cfg := chat.Config{
		LLM:         client,
		Personality: persona.Persona{Name: "test"},
		WorkDir:     t.TempDir(),
	}

	var calls int
	events := runConfig(context.Background(), cfg, "hi", func() { calls++ })

	var text string
	var sawDone bool
	for ev := range events {
		switch ev.Kind {
		case Token:
			text += ev.Text
		case Done:
			sawDone = true
		}
	}
	if text != "hello world" {
		t.Fatalf("text = %q, want %q", text, "hello world")
	}
	if !sawDone {
		t.Fatal("never saw Done before channel closed")
	}
	if calls != 1 {
		t.Fatalf("cleanup called %d times, want 1", calls)
	}
}

func TestRunConfig_MapsToolResult(t *testing.T) {
	// First model call invokes a custom tool; second call ends the turn.
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "1", Name: "echo_tool", RawArgs: "{}"}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "done"},
		}},
	)
	cfg := chat.Config{
		LLM: client,
		Personality: persona.Persona{
			Name:  "test",
			Tools: []llm.ToolDefinition{{Name: "echo_tool", Description: "echo"}},
		},
		WorkDir:         t.TempDir(),
		CustomTool:      func(ctx context.Context, name, args string) (string, error) { return "echoed", nil },
		CustomToolNames: map[string]bool{"echo_tool": true},
	}

	events := runConfig(context.Background(), cfg, "hi", func() {})

	var tools []Event
	for ev := range events {
		if ev.Kind == ToolResult {
			tools = append(tools, ev)
		}
	}
	if len(tools) != 1 {
		t.Fatalf("got %d ToolResult events, want 1", len(tools))
	}
	if tools[0].ToolName != "echo_tool" || tools[0].ToolOutput != "echoed" {
		t.Fatalf("tool event = %+v, want name=echo_tool output=echoed", tools[0])
	}
}

func TestRun_BadConfig_Errors(t *testing.T) {
	// Point at a temp dir with no shell3.lua — Run must fail to start:
	// non-nil error AND nil channel (nothing ran).
	tmp := t.TempDir()
	ch, err := Run(context.Background(), Spec{
		Prompt:     "hi",
		ConfigPath: tmp + "/shell3.lua",
		WorkDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
	if ch != nil {
		t.Fatal("expected nil channel on start failure")
	}
}
