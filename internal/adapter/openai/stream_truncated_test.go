package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

// TestStreamTruncatedMidResponse simulates a provider that writes a partial SSE
// response and then closes the connection without a terminating "[DONE]" event
// (e.g. out-of-credits, rate limit, or an upstream proxy/timeout). The OpenAI
// SDK surfaces this as io.ErrUnexpectedEOF. We assert the returned error carries
// a clearer, human-readable message AND still wraps io.ErrUnexpectedEOF so
// errors.Is keeps working for programmatic callers.
func TestStreamTruncatedMidResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fl, _ := w.(http.Flusher)
		// Emit one content chunk, then hijack and slam the connection shut
		// mid-stream with no terminating event — surfaces io.ErrUnexpectedEOF.
		fmt.Fprintf(w, "data: %s\n\n", `{"choices":[{"index":0,"delta":{"content":"hel"}}]}`)
		if fl != nil {
			fl.Flush()
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Errorf("ResponseWriter does not support hijacking")
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Close abruptly without a terminating [DONE].
		_ = conn.Close()
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "test-key", "test-model")
	err := c.Stream(context.Background(),
		[]llm.Message{{Role: llm.RoleUser, Content: "hi"}}, nil,
		func(ev llm.StreamEvent) {})
	if err == nil {
		t.Fatal("expected an error from a truncated stream, got nil")
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected error to wrap io.ErrUnexpectedEOF, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{"ended early", "mid-response", "credits"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message %q missing phrase %q", msg, want)
		}
	}
}

// TestWrapStreamErr unit-tests the error mapping directly: EOF-class errors get
// the clearer truncation message (and stay errors.Is-comparable), while every
// other error keeps the generic "llm: stream:" wrap unchanged.
func TestWrapStreamErr(t *testing.T) {
	t.Run("unexpected EOF -> truncation message", func(t *testing.T) {
		err := wrapStreamErr(io.ErrUnexpectedEOF)
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("lost io.ErrUnexpectedEOF: %v", err)
		}
		for _, want := range []string{"ended early", "mid-response", "credits"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("message %q missing %q", err.Error(), want)
			}
		}
	})

	t.Run("plain EOF -> truncation message", func(t *testing.T) {
		err := wrapStreamErr(io.EOF)
		if !errors.Is(err, io.EOF) {
			t.Fatalf("lost io.EOF: %v", err)
		}
		if !strings.Contains(err.Error(), "ended early") {
			t.Errorf("message %q missing truncation phrase", err.Error())
		}
	})

	t.Run("unrelated error -> generic wrap", func(t *testing.T) {
		sentinel := errors.New("boom non-eof")
		err := wrapStreamErr(sentinel)
		if !errors.Is(err, sentinel) {
			t.Fatalf("lost wrapped error: %v", err)
		}
		if !strings.HasPrefix(err.Error(), "llm: stream:") {
			t.Fatalf("expected generic 'llm: stream:' wrap, got %v", err)
		}
		if strings.Contains(err.Error(), "ended early") {
			t.Fatalf("unrelated error should not get the truncation message: %v", err)
		}
	})
}
