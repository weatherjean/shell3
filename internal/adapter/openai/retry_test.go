package openai

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openai/openai-go/option"

	"github.com/weatherjean/shell3/internal/llm"
)

func resp(status int, headers map[string]string) *http.Response {
	h := make(http.Header)
	for k, v := range headers {
		h.Set(k, v)
	}
	return &http.Response{StatusCode: status, Header: h}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		res  *http.Response
		err  error
		want bool
	}{
		{"connection error", nil, errors.New("dial tcp: refused"), true},
		{"nil response", nil, nil, true},
		{"408 timeout", resp(408, nil), nil, true},
		{"409 conflict", resp(409, nil), nil, true},
		{"429 rate limit", resp(429, nil), nil, true},
		{"500 server", resp(500, nil), nil, true},
		{"503 unavailable", resp(503, nil), nil, true},
		{"400 bad request", resp(400, nil), nil, false},
		{"401 unauthorized", resp(401, nil), nil, false},
		{"200 ok", resp(200, nil), nil, false},
		{"x-should-retry true overrides 400", resp(400, map[string]string{"x-should-retry": "true"}), nil, true},
		{"x-should-retry false overrides 503", resp(503, map[string]string{"x-should-retry": "false"}), nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRetryable(c.res, c.err); got != c.want {
				t.Fatalf("isRetryable = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRetryReason(t *testing.T) {
	if got := retryReason(nil, errors.New("boom")); got != "connection error: boom" {
		t.Fatalf("err reason: %q", got)
	}
	if got := retryReason(resp(503, nil), nil); got != "HTTP 503" {
		t.Fatalf("status reason: %q", got)
	}
}

// callObserver invokes the middleware once with a request carrying the given
// retry-count header and a next that returns (res, err).
func callObserver(mw option.Middleware, retryCount string, ctx context.Context, res *http.Response, err error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://x", nil)
	req.Header.Set("X-Stainless-Retry-Count", retryCount)
	next := func(*http.Request) (*http.Response, error) { return res, err }
	_, _ = mw(req, next)
}

func TestRetryObserverEmitsOnRetryableFailure(t *testing.T) {
	var got []llm.RetryNotice
	mw := retryObserver(func(ev llm.StreamEvent) {
		if ev.Retry != nil {
			got = append(got, *ev.Retry)
		}
	}, 5)

	callObserver(mw, "0", context.Background(), resp(503, nil), nil)

	if len(got) != 1 {
		t.Fatalf("expected 1 retry notice, got %d", len(got))
	}
	if got[0].Attempt != 1 || got[0].Max != 5 || got[0].Reason != "HTTP 503" {
		t.Fatalf("notice: %+v", got[0])
	}
}

func TestRetryObserverSuppressesOnLastAttempt(t *testing.T) {
	var n int
	mw := retryObserver(func(ev llm.StreamEvent) {
		if ev.Retry != nil {
			n++
		}
	}, 5)
	// retryCount == maxRetries: the SDK will not retry, so no notice.
	callObserver(mw, "5", context.Background(), resp(503, nil), nil)
	if n != 0 {
		t.Fatalf("expected no notice on final attempt, got %d", n)
	}
}

func TestRetryObserverSilentOnSuccess(t *testing.T) {
	var n int
	mw := retryObserver(func(ev llm.StreamEvent) {
		if ev.Retry != nil {
			n++
		}
	}, 5)
	callObserver(mw, "0", context.Background(), resp(200, nil), nil)
	if n != 0 {
		t.Fatalf("expected no notice on success, got %d", n)
	}
}

func TestRetryObserverSilentOnCanceledContext(t *testing.T) {
	var n int
	mw := retryObserver(func(ev llm.StreamEvent) {
		if ev.Retry != nil {
			n++
		}
	}, 5)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	callObserver(mw, "0", ctx, resp(503, nil), nil)
	if n != 0 {
		t.Fatalf("expected no notice when context canceled, got %d", n)
	}
}
