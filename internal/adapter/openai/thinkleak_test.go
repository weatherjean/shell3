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
// including the end-of-stream flush.
func runFilter(deltas ...string) string {
	var f thinkLeakFilter
	var out strings.Builder
	for _, d := range deltas {
		out.WriteString(f.filter(d))
	}
	out.WriteString(f.flush())
	return out.String()
}

func TestThinkLeakFilter(t *testing.T) {
	cases := []struct {
		name   string
		deltas []string
		want   string
	}{
		{"bare tag only (MiniMax tool-call turn)", []string{"</think>"}, ""},
		{"tag then text in one delta", []string{"</think>\n\nHello"}, "Hello"},
		{"tag split across deltas", []string{"</", "think>", "\n\n", "Hel", "lo"}, "Hello"},
		{"whitespace before tag", []string{" \n", "</think>", "hi"}, "hi"},
		{"normal text untouched", []string{"Hello", " world"}, "Hello world"},
		{"tag mid-message untouched", []string{"see the </think> tag"}, "see the </think> tag"},
		{"diverging prefix emitted verbatim", []string{"</thin", "gs to do"}, "</things to do"},
		{"stream ends while holding partial", []string{"</thi"}, "</thi"},
		{"empty stream", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runFilter(tc.deltas...); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

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
