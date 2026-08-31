package openai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestStreamStripsLeakedThinkTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"index":0,"delta":{"content":"</think>"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":" world"}}]}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	var text strings.Builder
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) { text.WriteString(ev.TextDelta) })
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := text.String(); got != "Hello world" {
		t.Fatalf("content: got %q, want %q", got, "Hello world")
	}
}

func TestStreamStripsInlineThinkBlock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"index":0,"delta":{"reasoning":"The user wants a greeting."}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"<think>\nThe user wants a greeting.\n"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"</think>\n\n"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"Hello there!"}}]}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	var text, reasoning strings.Builder
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			text.WriteString(ev.TextDelta)
			reasoning.WriteString(ev.ReasoningDelta)
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if got := text.String(); got != "Hello there!" {
		t.Fatalf("content: got %q, want %q", got, "Hello there!")
	}
	if got := reasoning.String(); got != "The user wants a greeting." {
		t.Fatalf("reasoning: got %q", got)
	}
}

func TestStreamReportsLengthTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"1\n2\n3"}}]}`)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	var truncated bool
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			if ev.Truncated {
				truncated = true
			}
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if !truncated {
		t.Fatal("finish_reason=length did not surface as Truncated")
	}
}

func TestStreamCleanStopNotTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			if ev.Truncated {
				t.Error("clean stop reported as truncated")
			}
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
}

func TestStreamStripsBareLeakedTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"</think>"}}]}`)
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"1","function":{"name":"bash","arguments":"{}"}}]}}]}`)
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	var text strings.Builder
	var calls []string
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			text.WriteString(ev.TextDelta)
			if ev.ToolCall != nil {
				calls = append(calls, ev.ToolCall.Name)
			}
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if text.Len() != 0 {
		t.Fatalf("expected no text deltas, got %q", text.String())
	}
	if len(calls) != 1 || calls[0] != "bash" {
		t.Fatalf("tool calls: got %v", calls)
	}
}

func TestStreamAssemblesFragmentedToolMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"ba"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"real-id","function":{"name":"sh","arguments":"{\"command\":"}}]}}]}`,
			`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"pwd\"}"}}]}}]}`,
		} {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	var calls []llm.ToolCall
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			if ev.ToolCall != nil {
				calls = append(calls, *ev.ToolCall)
			}
		})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("tool calls: %+v", calls)
	}
	if got := calls[0]; got.ID != "real-id" || got.Name != "bash" || got.RawArgs != `{"command":"pwd"}` {
		t.Fatalf("fragmented tool call assembled as %+v", got)
	}
}
