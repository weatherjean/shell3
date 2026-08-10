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
func runFilter(reasoning bool, deltas ...string) string {
	f := thinkLeakFilter{reasoningSeen: reasoning}
	var out strings.Builder
	for _, d := range deltas {
		out.WriteString(f.filter(d))
	}
	out.WriteString(f.flush())
	return out.String()
}

func TestThinkLeakFilter(t *testing.T) {
	cases := []struct {
		name      string
		reasoning bool
		deltas    []string
		want      string
	}{
		{"bare tag only (MiniMax tool-call turn)", false, []string{"</think>"}, ""},
		{"tag then text in one delta", false, []string{"</think>\n\nHello"}, "Hello"},
		{"tag split across deltas", false, []string{"</", "think>", "\n\n", "Hel", "lo"}, "Hello"},
		{"whitespace before tag", false, []string{" \n", "</think>", "hi"}, "hi"},
		{"normal text untouched", false, []string{"Hello", " world"}, "Hello world"},
		{"tag mid-message untouched", false, []string{"see the </think> tag"}, "see the </think> tag"},
		{"diverging prefix emitted verbatim", false, []string{"</thin", "gs to do"}, "</things to do"},
		{"stream ends while holding partial", false, []string{"</thi"}, "</thi"},
		{"empty stream", false, nil, ""},
		// The wider net: a reasoning stream was present, so a tag near the
		// start of content is the provider's reasoning tail bleeding over.
		{"glued tail dropped (reasoning seen)", true, []string{"state.</think>", "\n\nHello"}, "Hello"},
		{"glued tail split across deltas", true, []string{"state.</", "think>\nHello"}, "Hello"},
		{"bare tag still dropped when armed", true, []string{"</think>", "hi"}, "hi"},
		{"clean content passes when armed", true, []string{"Hello there, this is a perfectly ordinary reply that keeps going past the scan window."}, "Hello there, this is a perfectly ordinary reply that keeps going past the scan window."},
		{"short clean content flushes when armed", true, []string{"Sure."}, "Sure."},
		{"late tag passes when armed", true, []string{strings.Repeat("a", 100) + "</think>tail"}, strings.Repeat("a", 100) + "</think>tail"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := runFilter(tc.reasoning, tc.deltas...); got != tc.want {
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
