//go:build unix

package telegram

import (
	"context"
	"fmt"
)

// contextMilestoneFor returns the highest user-facing fullness threshold
// reached. The model receives finer 10% reminders internally; the chat gets
// only the two useful human milestones.
func contextMilestoneFor(pct int) int {
	switch {
	case pct >= 75:
		return 75
	case pct >= 50:
		return 50
	default:
		return 0
	}
}

func (c *conversation) contextWindow() int {
	sess := c.session()
	if sess == nil {
		return 0
	}
	return sess.Snapshot().ContextWindow
}

// observeContextUsage posts each milestone once per growth cycle. Provider
// prompt usage is the context gauge: unlike cumulative spend, it represents
// how full the next request is. The post is silent and host-generated, so it
// costs no model turn and never enters conversation history.
func (c *conversation) observeContextUsage(ctx context.Context, promptTokens int) {
	window := c.contextWindow()
	if promptTokens <= 0 || window <= 0 {
		return
	}
	pct := promptTokens * 100 / window
	milestone := contextMilestoneFor(pct)
	c.mu.Lock()
	if milestone == 0 || milestone <= c.contextMilestone {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	text := fmt.Sprintf("🧠 context milestone: %d%% full (%d / %d tokens)",
		milestone, promptTokens, window)
	if c.postChunk(ctx, nil, "", text, SendOpt{Silent: true}) == nil {
		c.mu.Lock()
		if milestone > c.contextMilestone {
			c.contextMilestone = milestone
		}
		c.mu.Unlock()
	}
}

// observeCompaction announces the boundary and re-baselines milestones from
// the compacted size. A large preserved tail may still be over a threshold;
// recording it prevents an immediate redundant milestone post in this turn.
func (c *conversation) observeCompaction(ctx context.Context, promptTokens int) {
	window := c.contextWindow()
	pct := 0
	if promptTokens > 0 && window > 0 {
		pct = promptTokens * 100 / window
	}
	c.mu.Lock()
	c.contextMilestone = contextMilestoneFor(pct)
	c.mu.Unlock()

	text := "🧠 conversation compacted"
	if promptTokens > 0 && window > 0 {
		text = fmt.Sprintf("🧠 conversation compacted — context is now %d%% full (%d / %d tokens)",
			pct, promptTokens, window)
	} else if promptTokens > 0 {
		text = fmt.Sprintf("🧠 conversation compacted — context is now about %d tokens", promptTokens)
	}
	_ = c.postChunk(ctx, nil, "", text, SendOpt{Silent: true})
}
