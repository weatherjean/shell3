package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// TestStreamRequestParamMapping pins how SetParams values land in the outgoing
// ChatCompletions request body — the RequestParams→SDK mapping in Stream
// (client.go), which no other test covers. It captures the request body from a
// stub SSE server and asserts the JSON the SDK serialized. Cases cover the
// xhigh→high clamp, the "none"/"" effort skip, temperature, parallel_tool_calls,
// and max_completion_tokens.
func TestStreamRequestParamMapping(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	b := func(v bool) *bool { return &v }

	cases := []struct {
		name   string
		params llm.RequestParams
		assert func(t *testing.T, body map[string]any)
	}{
		{
			name:   "xhigh clamps to high",
			params: llm.RequestParams{ReasoningEffort: "xhigh"},
			assert: func(t *testing.T, body map[string]any) {
				if got := body["reasoning_effort"]; got != "high" {
					t.Errorf("reasoning_effort = %v, want high", got)
				}
			},
		},
		{
			name:   "high passes through",
			params: llm.RequestParams{ReasoningEffort: "high"},
			assert: func(t *testing.T, body map[string]any) {
				if got := body["reasoning_effort"]; got != "high" {
					t.Errorf("reasoning_effort = %v, want high", got)
				}
			},
		},
		{
			// "none" hits the eff != "none" skip in Stream, so the field is
			// omitted even though the client default is medium.
			name:   "none is omitted",
			params: llm.RequestParams{ReasoningEffort: "none"},
			assert: func(t *testing.T, body map[string]any) {
				if _, ok := body["reasoning_effort"]; ok {
					t.Errorf("reasoning_effort should be absent for none, body=%v", body)
				}
			},
		},
		{
			name:   "temperature",
			params: llm.RequestParams{Temperature: f(0.25)},
			assert: func(t *testing.T, body map[string]any) {
				if got := body["temperature"]; got != 0.25 {
					t.Errorf("temperature = %v, want 0.25", got)
				}
			},
		},
		{
			name:   "parallel_tool_calls false",
			params: llm.RequestParams{ParallelToolCalls: b(false)},
			assert: func(t *testing.T, body map[string]any) {
				if got := body["parallel_tool_calls"]; got != false {
					t.Errorf("parallel_tool_calls = %v, want false", got)
				}
			},
		},
		{
			name:   "max_completion_tokens",
			params: llm.RequestParams{MaxTokens: 4096},
			assert: func(t *testing.T, body map[string]any) {
				if got := body["max_completion_tokens"]; got != float64(4096) {
					t.Errorf("max_completion_tokens = %v, want 4096", got)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured []byte
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				fl, _ := w.(http.Flusher)
				write := func(s string) {
					fmt.Fprintf(w, "data: %s\n\n", s)
					if fl != nil {
						fl.Flush()
					}
				}
				write(`{"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`)
				write("[DONE]")
			}))
			defer srv.Close()

			c := NewClient(srv.URL, "test-key", "test-model")
			c.SetParams(tc.params)
			if err := c.Stream(context.Background(),
				[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
				func(llm.StreamEvent) {}); err != nil {
				t.Fatalf("Stream: %v", err)
			}

			var body map[string]any
			if err := json.Unmarshal(captured, &body); err != nil {
				t.Fatalf("unmarshal request body %q: %v", captured, err)
			}
			tc.assert(t, body)
		})
	}
}
