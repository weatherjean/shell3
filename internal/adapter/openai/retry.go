package openai

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/openai/openai-go/option"

	"github.com/weatherjean/shell3/pkg/llm"
)

// maxRetries is how many times a failed request is retried. It is set both on
// the client (option.WithMaxRetries) and read by retryObserver to know when an
// attempt is the last one (so it suppresses a spurious "retrying" notice).
const maxRetries = 5

// retryObserver returns SDK middleware that emits a Retry StreamEvent each
// time an attempt fails retryably and the SDK will retry it. The SDK runs the
// middleware once per attempt and tags the request with X-Stainless-Retry-Count
// (the 0-based attempt index); the upcoming retry is therefore index+1. The
// SDK still owns the backoff sleep and the retry itself — this only surfaces it.
func retryObserver(onEvent func(llm.StreamEvent), max int) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		res, err := next(req)
		rc := retryCount(req)
		// Suppress on a canceled context (e.g. user interrupt) and on the
		// final attempt, where shouldRetry no longer leads to a retry.
		if req.Context().Err() == nil && rc < max && isRetryable(res, err) {
			onEvent(llm.StreamEvent{Retry: &llm.RetryNotice{
				Attempt: rc + 1,
				Max:     max,
				Reason:  retryReason(res, err),
			}})
		}
		return res, err
	}
}

// retryCount reads the SDK's per-attempt X-Stainless-Retry-Count header.
func retryCount(req *http.Request) int {
	n, _ := strconv.Atoi(req.Header.Get("X-Stainless-Retry-Count"))
	return n
}

// isRetryable mirrors the openai-go SDK's shouldRetry: connection errors and
// 408/409/429/5xx are retryable, with the x-should-retry header taking
// precedence. Kept in sync with the SDK so the notice matches its behavior.
func isRetryable(res *http.Response, err error) bool {
	if err != nil || res == nil {
		return true
	}
	switch res.Header.Get("x-should-retry") {
	case "true":
		return true
	case "false":
		return false
	}
	return res.StatusCode == http.StatusRequestTimeout ||
		res.StatusCode == http.StatusConflict ||
		res.StatusCode == http.StatusTooManyRequests ||
		res.StatusCode >= http.StatusInternalServerError
}

// retryReason summarizes why an attempt failed, for display.
func retryReason(res *http.Response, err error) string {
	if err != nil {
		return "connection error: " + err.Error()
	}
	if res == nil {
		return "no response"
	}
	return fmt.Sprintf("HTTP %d", res.StatusCode)
}
