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

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "one"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	// Arrives mid-turn: queues silently.
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "two"})
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
	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	select {
	case <-g.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}
	// Two replies to the thread anchor arrive mid-turn.
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", ReplyToID: "1", Text: "also this"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "3", ReplyToID: "1", Text: "and this"})
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

// Inbox() renders the queued state (surfaced on the dash index).
func TestInboxView(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)

	if got := b.Inbox(); !strings.Contains(got, "inbox empty") {
		t.Fatalf("idle inbox = %q, want empty", got)
	}

	b.mu.Lock()
	b.turnActive = true
	b.mu.Unlock()
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "9", Text: "later please"})
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.mailQueue) == 1
	})
	if got := b.Inbox(); !strings.Contains(got, "later please") || !strings.Contains(got, "from you") {
		t.Fatalf("inbox = %q, want the queued mail listed", got)
	}
	b.mu.Lock()
	b.turnActive = false
	b.mailQueue = nil
	b.mu.Unlock()
}

// A model that over-generalizes the wake-turn NO_REPLY sentinel into a USER
// turn must not post the literal string — it degrades to the empty-reply
// placeholder instead. This drives the REAL production path (handleMsg ->
// dispatchMail -> runUserTurn), so it exercises the guard inlined at
// bot.go's runUserTurn, not a copy of it.
func TestRunUserTurn_NoReplySentinelNotPosted(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "NO_REPLY.")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hello"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "(no output)")
	})
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "NO_REPLY") {
			t.Fatalf("the sentinel leaked into the chat: %q", txt)
		}
	}
}

// A corrupt reply (raw tool-call template markup) must never reach the chat
// verbatim in a USER turn — it is replaced by malformedReplyNotice. Drives
// the real production path through runUserTurn, same as above.
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

// Same two guards, but through runPostedQueuedTurn: a Wake that finds queued
// user steering (Contract 12) runs a POSTED turn whose reply passes through
// the SAME inline guards as runUserTurn, at a different call site in bot.go.
// This mirrors TestContract12_WakeWithSteerPostsReply's drive pattern.
func TestRunPostedQueuedTurn_NoReplySentinelNotPosted(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "NO_REPLY.")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "(no output)")
	})
	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()
	before := len(fc.sentTexts())

	sess.Interject("one more thing")
	b.dispatchWake(context.Background(), sess.ID())

	waitFor(t, func() bool { return len(fc.sentTexts()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("posted wake turn must drain the steer")
	}
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "NO_REPLY") {
			t.Fatalf("the sentinel leaked into the chat: %q", txt)
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
	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()
	before := len(fc.sentTexts())

	sess.Interject("one more thing")
	b.dispatchWake(context.Background(), sess.ID())

	waitFor(t, func() bool { return len(fc.sentTexts()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("posted wake turn must drain the steer")
	}
	for _, txt := range fc.sentTexts() {
		if strings.Contains(txt, "<tool_call") {
			t.Fatalf("raw markup leaked into the chat: %q", txt)
		}
	}
}
