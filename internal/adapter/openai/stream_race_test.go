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

// TestStreamOnEventSingleGoroutine asserts onEvent is invoked from a single
// goroutine. The SSE response interleaves reasoning fields (extracted by the
// tap goroutine) with content (parsed by the SDK main loop); before the fix
// both goroutines called onEvent, so writing a shared unguarded builder on
// every call races under -race. After the fix all onEvent calls are funneled
// onto the Stream goroutine. Run with: go test -race.
func TestStreamOnEventSingleGoroutine(t *testing.T) {
	const n = 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		write := func(s string) {
			fmt.Fprintf(w, "data: %s\n\n", s)
			if fl != nil {
				fl.Flush()
			}
		}
		for i := 0; i < n; i++ {
			write(`{"choices":[{"index":0,"delta":{"reasoning":"r"}}]}`)
			write(`{"choices":[{"index":0,"delta":{"content":"c"}}]}`)
		}
		write(`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
		write("[DONE]")
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")

	// shared is written on EVERY onEvent call with no lock: data-race-free only
	// if onEvent is single-goroutine.
	var shared strings.Builder
	var reasoning, content strings.Builder
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {
			shared.WriteString("x")
			switch {
			case ev.ReasoningDelta != "":
				reasoning.WriteString(ev.ReasoningDelta)
			case ev.TextDelta != "":
				content.WriteString(ev.TextDelta)
			}
		})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	// No reasoning or content lost by the funnel.
	if reasoning.Len() != n {
		t.Errorf("reasoning delivered = %d, want %d", reasoning.Len(), n)
	}
	if content.Len() != n {
		t.Errorf("content delivered = %d, want %d", content.Len(), n)
	}
}
