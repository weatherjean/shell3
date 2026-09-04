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
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
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
	rt := shell3test.NewRuntimeForTestConfig(t, func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM:      g,
			Headless: o.Headless, Store: st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestMessageQueueDrainsAfterTurn(t *testing.T) {
	fc := newFakeClient()
	g := newGatedFirst("first reply", "second reply")
	rt := gatedRuntime(t, g)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "one"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "two"})
	close(g.Release)

	waitFor(t, func() bool {
		all := strings.Join(fc.sentTexts(), "\n")
		return strings.Contains(all, "first reply") && strings.Contains(all, "second reply")
	})
	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "2" && strings.Contains(r.text, "second reply")
	})
}

func TestMessageQueueBatchesRepliesIntoOneTurn(t *testing.T) {
	fc := newFakeClient()
	g := newGatedFirst("first reply", "batched reply")
	rt := gatedRuntime(t, g)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", ReplyToID: "1", Text: "also this"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "3", ReplyToID: "1", Text: "and this"})
	close(g.Release)

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "3" && strings.Contains(r.text, "batched reply")
	})
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
		t.Fatalf("batch turn input missing a queued message:\n%s", all.String())
	}
}

func TestSuperstopNoticeWaitsForNextUserTurn(t *testing.T) {
	fc := newFakeClient()
	g := newGatedFirst("next reply")
	rt := gatedRuntime(t, g)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	c := tconv(b)
	c.mu.Lock()
	sess := c.main
	c.mu.Unlock()
	if sess == nil {
		t.Fatal("no live session")
	}

	c.handleSuperstop(context.Background())
	waitIdle(t, b)
	g.mu.Lock()
	calls := g.calls
	g.mu.Unlock()
	if calls != 1 {
		t.Fatalf("superstop started %d model calls, want only the cancelled user turn", calls)
	}
	if !sess.HasQueuedInput() || sess.HasQueuedSteer() {
		t.Fatal("superstop must leave one host notice queued without user steering")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "continue"})
	if !waitForReply(t, fc, "next reply") {
		t.Fatalf("next user turn produced no reply: %v", fc.sentTexts())
	}
	wire := g.inner.CallsSnapshot()
	if len(wire) != 1 {
		t.Fatalf("provider calls after next user turn = %d, want 1", len(wire))
	}
	var prompt strings.Builder
	for _, msg := range wire[0].Msgs {
		prompt.WriteString(msg.Content)
		prompt.WriteByte('\n')
	}
	if !strings.Contains(prompt.String(), "superstop") || !strings.Contains(prompt.String(), "continue") {
		t.Fatalf("next turn did not receive host notice and user input:\n%s", prompt.String())
	}
}

func TestRunUserTurn_ToolMarkupReplacedWithNotice(t *testing.T) {
	corrupt := "]<]minimax[>[<tool_call>\nbash: git show 9cc4ffc\n</tool_call>"
	fc := newFakeClient()
	rt := storeRuntime(t, corrupt)
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hello"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), malformedReplyNotice)
	})
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "<tool_call") {
			t.Fatalf("raw markup leaked into the chat: %q", txt)
		}
	}
}

func TestRunPostedQueuedTurn_ToolMarkupReplacedWithNotice(t *testing.T) {
	corrupt := "]<]minimax[>[<tool_call>\nbash: git show 9cc4ffc\n</tool_call>"
	fc := newFakeClient()
	rt := storeRuntime(t, corrupt)
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), malformedReplyNotice)
	})
	c := tconv(b)
	c.mu.Lock()
	sess := c.main
	c.mu.Unlock()
	before := len(fc.sentTexts())

	sess.Interject("one more thing")
	c.startNextWork(context.Background())

	waitFor(t, func() bool { return len(fc.sentTexts()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("posted catch-up turn must drain the steer")
	}
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "<tool_call") {
			t.Fatalf("raw markup leaked into the chat: %q", txt)
		}
	}
}
