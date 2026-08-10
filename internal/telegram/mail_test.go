//go:build unix

package telegram

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// gatedFirstClient blocks its FIRST Stream call until Release is closed
// (signalling Started), then — and on every later call — replies via the
// scripted client. It gives tests a real mid-turn window that still ends.
type gatedFirstClient struct {
	inner   *fakellm.Client
	Started chan struct{}
	Release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	calls   int
}

func newGatedFirst(replies ...string) *gatedFirstClient {
	scripts := make([]fakellm.Script, 0, len(replies))
	for _, r := range replies {
		scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: r}}})
	}
	return &gatedFirstClient{
		inner:   fakellm.New(scripts...),
		Started: make(chan struct{}),
		Release: make(chan struct{}),
	}
}

func (g *gatedFirstClient) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	g.mu.Lock()
	first := g.calls == 0
	g.calls++
	g.mu.Unlock()
	if first {
		g.once.Do(func() { close(g.Started) })
		select {
		case <-g.Release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.inner.Stream(ctx, msgs, tools, onEvent)
}

func gatedRuntime(t *testing.T, g *gatedFirstClient) *shell3.Runtime {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM: g, ModeLabel: "code", AgentNames: []string{"code"},
			Headless: o.Headless, Store: st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// A message queued mid-turn drains once the turn ends: it runs its own turn
// and gets its own reply. Sending always succeeds; nothing is dropped.
func TestMailQueueDrainsAfterTurn(t *testing.T) {
	fc := newFakeClient()
	g := newGatedFirst("first reply", "second reply")
	rt := gatedRuntime(t, g)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "one"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	// Arrives mid-turn: queues silently.
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "2", Text: "two"})
	close(g.Release)

	waitFor(t, func() bool {
		all := strings.Join(fc.sentTexts(), "\n")
		return strings.Contains(all, "first reply") && strings.Contains(all, "second reply")
	})
	// The queued message became its own thread: its reply anchors at msg 2.
	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "2" && strings.Contains(r.text, "second reply")
	})
}

// Replies queued into the SAME thread while a turn runs drain as ONE batch
// turn: one model call over the concatenated mail, one reply, anchored at the
// newest message.
func TestMailQueueBatchesRepliesIntoOneTurn(t *testing.T) {
	fc := newFakeClient()
	g := newGatedFirst("first reply", "batched reply")
	rt := gatedRuntime(t, g)
	b := newBot(t, fc, rt)

	// Start thread: msg 1 → session recorded under msg 1 and its reply id.
	go b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "start"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	// Two replies to the thread anchor arrive mid-turn.
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "2", ReplyToID: "1", Text: "also this"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "3", ReplyToID: "1", Text: "and this"})
	close(g.Release)

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "3" && strings.Contains(r.text, "batched reply")
	})
	// One batch turn: the model saw both queued messages in one call (call 2).
	// Depending on timing they arrive as the batch turn's user message (queue
	// path) or as an injected steer block (steer path) — either way, ONE extra
	// model call carries both.
	calls := g.inner.CallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2 (first turn + one batch)", len(calls))
	}
	var all strings.Builder
	for _, m := range calls[1].Msgs {
		all.WriteString(m.Content)
		all.WriteString("\n")
	}
	if !strings.Contains(all.String(), "also this") || !strings.Contains(all.String(), "and this") {
		t.Fatalf("batch turn input missing a queued mail:\n%s", all.String())
	}
}

// /inbox renders the queued state.
func TestInboxCommand(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/inbox"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "inbox empty")
	})

	b.mu.Lock()
	b.turnActive = true
	b.mu.Unlock()
	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "9", Text: "later please"})
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.mailQueue) == 1
	})
	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/inbox"})
	waitFor(t, func() bool {
		all := strings.Join(fc.sentTexts(), "\n")
		return strings.Contains(all, "later please") && strings.Contains(all, "from you")
	})
	b.mu.Lock()
	b.turnActive = false
	b.mailQueue = nil
	b.mu.Unlock()
}
