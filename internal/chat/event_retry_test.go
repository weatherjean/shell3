package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestEmitRetry(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitRetry(s, &llm.RetryNotice{Attempt: 2, Max: 5, Reason: "HTTP 503"})
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventRetry {
		t.Fatalf("retry event missing: %+v", got)
	}
	if got[0].Text != "stream failed (HTTP 503), retrying (2/5)" {
		t.Fatalf("retry text: %q", got[0].Text)
	}
}

// retryOnlyClient is a fake LLMClient that emits a single Retry event then
// completes the stream, exercising the streamOnce relay path.
type retryOnlyClient struct{ notice llm.RetryNotice }

func (c retryOnlyClient) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	onEvent(llm.StreamEvent{Retry: &c.notice})
	onEvent(llm.StreamEvent{Done: true})
	return nil
}

func TestStreamOnceRelaysRetry(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	client := retryOnlyClient{notice: llm.RetryNotice{Attempt: 1, Max: 5, Reason: "HTTP 429"}}
	_, _, _, _, err := streamOnce(context.Background(), client, nil, nil, s)
	if err != nil {
		t.Fatalf("streamOnce err: %v", err)
	}
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventRetry {
		t.Fatalf("expected EventRetry from relay, got %+v", got)
	}
	if got[0].Text != "stream failed (HTTP 429), retrying (1/5)" {
		t.Fatalf("relayed retry text: %q", got[0].Text)
	}
}
