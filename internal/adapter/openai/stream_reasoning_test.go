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

// ---- thinkLeakFilter unit tests ----

// run feeds deltas through a fresh filter and returns the concatenated output,
// including the end-of-stream flush. reasoning arms the wider glued-tail net
// (set when the stream also carried a split reasoning field).
// ---- integration: Stream drops a leaked leading </think> ----

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

// MiniMax-M3 over the OpenAI-compatible endpoint streams its reasoning twice:
// once in delta.reasoning and again inline in delta.content wrapped in
// <think>…</think>. Only the real reply may reach the caller as text.
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

// finish_reason "length" means the provider cut the response at the output
// token cap. It must surface, not pass as a complete reply.
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

// A normal stop must NOT be reported as truncated.
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

// A leaked tag that is the ENTIRE content (tool-call turn) must yield no
// text deltas at all — not even whitespace.
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
