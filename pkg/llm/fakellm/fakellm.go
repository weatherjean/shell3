// Package fakellm provides a deterministic chat.LLMClient implementation
// for tests. The Client records the calls it received and replies with
// a scripted sequence of StreamEvents.
package fakellm

import (
	"context"
	"sync"

	"github.com/weatherjean/shell3/pkg/llm"
)

// Script is a scripted reply for one Stream call. Events fire in order
// via onEvent. Err is returned from Stream after events finish.
type Script struct {
	Events []llm.StreamEvent
	Err    error
}

// Call records one Stream invocation.
type Call struct {
	Msgs  []llm.Message
	Tools []llm.ToolDefinition
}

// Client implements chat.LLMClient (and llm.Provider's Streamer contract).
// Each call to Stream consumes one Script from Scripts (in order). If
// Scripts is exhausted, the last script repeats.
type Client struct {
	mu      sync.Mutex
	Scripts []Script
	calls   int
	Calls   []Call
}

// New returns a Client preloaded with the given scripts.
func New(scripts ...Script) *Client {
	return &Client{Scripts: scripts}
}

// Stream emits the next scripted event sequence via onEvent and returns the
// script's configured error. Honors ctx cancellation between events.
func (c *Client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	c.mu.Lock()
	c.Calls = append(c.Calls, Call{Msgs: msgs, Tools: tools})
	idx := c.calls
	c.calls++
	if len(c.Scripts) == 0 {
		c.mu.Unlock()
		return nil
	}
	if idx >= len(c.Scripts) {
		idx = len(c.Scripts) - 1
	}
	script := c.Scripts[idx]
	c.mu.Unlock()

	for _, ev := range script.Events {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		onEvent(ev)
	}
	return script.Err
}

// CallCount returns the number of Stream calls received.
func (c *Client) CallCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}
