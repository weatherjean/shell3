package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestStreamIdleTimeout(t *testing.T) {
	for _, mode := range []string{"headers", "body", "heartbeat", "partial-tool"} {
		t.Run(mode, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				requests.Add(1)
				if mode == "headers" {
					<-r.Context().Done()
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if mode == "partial-tool" {
					fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{\"}}]}}]}\n\n")
				}
				w.(http.Flusher).Flush()
				ticker := time.NewTicker(10 * time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-r.Context().Done():
						return
					case <-ticker.C:
						if mode == "heartbeat" {
							fmt.Fprint(w, ": keepalive\n\ndata: {\"choices\":[]}\n\n")
							w.(http.Flusher).Flush()
						}
					}
				}
			}))
			defer srv.Close()
			c := NewClient(srv.URL, "test-key", "test-model")
			c.idleTimeout = 100 * time.Millisecond
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err := c.Stream(ctx, nil, nil, func(ev llm.StreamEvent) {
				if ev.ToolCall != nil {
					t.Error("dispatched incomplete tool call")
				}
			})
			if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "model produced no progress") {
				t.Fatalf("expected explicit idle timeout, got %v", err)
			}
			if requests.Load() != 1 {
				t.Fatalf("silently retried timeout: %d requests", requests.Load())
			}
		})
	}
}

func TestModelProgressExtendsIdleDeadline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		for range 8 {
			fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n")
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(30 * time.Millisecond):
			}
		}
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "test-key", "test-model")
	c.idleTimeout = 150 * time.Millisecond
	var answer string
	err := c.Stream(t.Context(), nil, nil, func(ev llm.StreamEvent) { answer += ev.TextDelta })
	if err != nil || answer != "done" {
		t.Fatalf("active stream stopped: %q, %v", answer, err)
	}
}

func TestIdleWatchPreservesUserCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	ctx, _, stop := watchIdle(parent, time.Hour)
	defer stop()
	cancel()
	if !errors.Is(context.Cause(ctx), context.Canceled) {
		t.Fatal("lost parent cancellation")
	}
}
