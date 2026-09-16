package openai

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const modelIdleTimeout = 5 * time.Minute

// watchIdle covers connection setup, SDK retries, and streaming. Only model
// progress resets it; transport heartbeats cannot keep a silent turn alive.
func watchIdle(parent context.Context, timeout time.Duration) (context.Context, func(), func()) {
	cause := fmt.Errorf("llm: model produced no progress for %s; the request was stopped. The turn has ended; you can ask to continue: %w", timeout, context.DeadlineExceeded)
	ctx, cancel := context.WithCancelCause(parent)
	var mu sync.Mutex
	deadline := time.Now().Add(timeout)
	stopped := false
	var timer *time.Timer
	mu.Lock()
	timer = time.AfterFunc(timeout, func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		if remaining := time.Until(deadline); remaining > 0 {
			timer.Reset(remaining)
			return
		}
		stopped = true
		cancel(cause)
	})
	mu.Unlock()
	progress := func() {
		mu.Lock()
		defer mu.Unlock()
		if !stopped {
			deadline = time.Now().Add(timeout)
		}
	}
	stop := func() {
		mu.Lock()
		stopped = true
		timer.Stop()
		mu.Unlock()
		cancel(nil)
	}
	return ctx, progress, stop
}
