//go:build unix

package telegram

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
)

func storeRuntimeClient(t *testing.T, client chat.LLMClient) *shell3.Runtime {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rt := shell3test.NewRuntimeForTestConfig(t, func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM:      client,
			Headless: o.Headless, Store: st,
			Profile: chat.AgentProfile{Tools: []llm.ToolDefinition{{
				Name: "bash_bg",
				Parameters: map[string]any{
					"type": "object", "properties": map[string]any{"command": map[string]any{"type": "string"}}, "required": []string{"command"},
				},
			}}},
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func storeRuntime(t *testing.T, reply string) *shell3.Runtime {
	return storeRuntimeClient(t, fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: reply}}}))
}

func TestContract1_FirstMessageStartsTheConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "fresh reply")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "100", Text: "hi"})

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && strings.Contains(r.text, "fresh reply")
	})
	r, _ := fc.lastReply()
	if r.replyTo != "100" {
		t.Fatalf("reply must thread to inbound message 100, got replyTo=%s", r.replyTo)
	}
	c := tconv(b)
	c.mu.Lock()
	main := c.main
	c.mu.Unlock()
	if main == nil {
		t.Fatal("no main session after the first turn")
	}
	if id, ok := tconv(b).index.Current(); !ok || id != main.ID() {
		t.Fatalf("current marker = %q,%v want %q", id, ok, main.ID())
	}
}

func TestContract2_BareMessageContinuesTheConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "r")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "100", Text: "first"})
	if !waitForReply(t, fc, "r") {
		t.Fatal("first turn produced no reply")
	}
	c := tconv(b)
	c.mu.Lock()
	first := c.main.ID()
	c.mu.Unlock()

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "101", Text: "yes please"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 2 })
	c.mu.Lock()
	got := c.main.ID()
	c.mu.Unlock()
	if got != first {
		t.Fatalf("bare message forked the conversation: first=%s second=%s", first, got)
	}
}

func TestContract3_ReplyAddsQuotedContext(t *testing.T) {
	fc := newFakeClient()
	rec := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "one"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "two"}}},
	)
	rt := storeRuntimeClient(t, rec)
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "one") {
		t.Fatal("first turn produced no reply")
	}
	c := tconv(b)
	c.mu.Lock()
	first := c.main.ID()
	c.mu.Unlock()

	b.handleMsg(context.Background(), Msg{
		ChatID: 42, SenderID: 42, ID: "2", ReplyToID: "999", ReplyTo: "QUOTED SNIPPET", Text: "pick up from here",
	})
	if !waitForReply(t, fc, "two") {
		t.Fatal("reply turn produced no reply")
	}
	c.mu.Lock()
	got := c.main.ID()
	c.mu.Unlock()
	if got != first {
		t.Fatalf("a reply forked the conversation: first=%s second=%s", first, got)
	}
	calls := rec.CallsSnapshot()
	last := calls[len(calls)-1].Msgs[len(calls[len(calls)-1].Msgs)-1]
	if !strings.Contains(last.Content, "> QUOTED SNIPPET") {
		t.Fatalf("quoted reply context missing from the model prompt: %q", last.Content)
	}
}

func TestContract4_MidTurnTextSteers(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "stop — wrong file"})

	c := tconv(b)
	c.mu.Lock()
	sess := c.main
	c.mu.Unlock()
	if sess == nil {
		t.Fatal("no main session mid-turn")
	}
	waitFor(t, func() bool { return sess.HasQueuedSteer() })
	tconv(b).mu.Lock()
	queued := len(tconv(b).pendingMessages)
	anchor := tconv(b).mainAnchor
	tconv(b).mu.Unlock()
	if queued != 0 {
		t.Fatalf("steered text must not also queue (pendingMessages=%d)", queued)
	}
	if anchor != "2" {
		t.Fatalf("anchor = %q, want the steering message id", anchor)
	}
	if r, ok := fc.lastReply(); ok {
		t.Fatalf("steering must be silent, got reply %q", r.text)
	}
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})
}

func TestContract4_MidTurnMediaQueues(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "look at this",
		Media: []Media{{Bytes: []byte("img"), MIME: "image/jpeg", Filename: "x.jpg"}}})

	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return len(tconv(b).pendingMessages) == 1
	})
	c := tconv(b)
	c.mu.Lock()
	c.pendingMessages = nil
	c.mu.Unlock()
	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})
}

func TestContract4_SteerCatchupPostsReply(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "caught up")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "caught up") {
		t.Fatal("first turn produced no reply")
	}
	c := tconv(b)
	c.mu.Lock()
	sess := c.main
	c.mu.Unlock()

	sess.Interject("also do this")
	before := len(fc.sentReplies())
	tconv(b).startNextWork(context.Background())

	waitFor(t, func() bool { return len(fc.sentReplies()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("catch-up turn must drain the queued steer")
	}
}

func TestContract5_StopCancelsTurnKeepsJobs(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started")
	}

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	waitFor(t, func() bool { tconv(b).mu.Lock(); a := tconv(b).turnActive; tconv(b).mu.Unlock(); return !a })
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
}

func TestContract5_StopKeepsBackgroundJobsRunning(t *testing.T) {
	fc := newFakeClient()
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "bg-call", Name: "bash_bg", RawArgs: `{"command":"sleep 30"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "background job started"}}},
	)
	rt := storeRuntimeClient(t, client)
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	for range sess.Send(context.Background(), "start background work") {
	}
	waitFor(t, func() bool { return sess.RunningJobs() > 0 })

	ctx := context.Background()
	c := tconv(b)
	c.mu.Lock()
	_, cancel, _ := c.takeSlotLocked(ctx)
	c.mu.Unlock()
	defer cancel()

	tconv(b).handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
	waitFor(t, func() bool { return true })
	if sess.RunningJobs() == 0 {
		t.Fatal("/stop must not cancel a background job — it should still be running")
	}
}

func TestContract6_NewStartsFreshConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "r")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "first"})
	if !waitForReply(t, fc, "r") {
		t.Fatal("first turn produced no reply")
	}
	c := tconv(b)
	c.mu.Lock()
	first := c.main.ID()
	c.mu.Unlock()

	waitIdle(t, b)

	tconv(b).handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/new"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "fresh conversation")
	})
	c.mu.Lock()
	live := c.main
	c.mu.Unlock()
	if live != nil {
		t.Fatal("/new must detach the main session")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "second"})
	waitFor(t, func() bool {
		tconv(b).mu.Lock()
		defer tconv(b).mu.Unlock()
		return tconv(b).main != nil
	})
	c.mu.Lock()
	second := c.main.ID()
	c.mu.Unlock()
	if second == first {
		t.Fatal("/new must start a fresh session")
	}
	if id, _ := tconv(b).index.Current(); id != second {
		t.Fatalf("current marker = %q, want the new session %q", id, second)
	}
}

func TestContract7_RestartResumesTheConversation(t *testing.T) {
	fc := newFakeClient()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "one"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "two"}}},
	)
	rt := shell3test.NewRuntimeForTestConfig(t, func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM:      client,
			Headless: o.Headless, Store: st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	sessions := NewSessionIndex(func() *runs.Store { return st }, "telegram")

	b1 := NewBot(fc, rt, 42, sessions)
	b1.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hello"})
	if !waitForReply(t, fc, "one") {
		t.Fatal("first bot produced no reply")
	}
	c := tconv(b1)
	c.mu.Lock()
	first := c.main.ID()
	c.mu.Unlock()

	b2 := NewBot(fc, rt, 42, sessions)
	b2.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "still there?"})
	if !waitForReply(t, fc, "two") {
		t.Fatal("second bot produced no reply")
	}
	c.mu.Lock()
	got := c.main.ID()
	c.mu.Unlock()
	if got != first {
		t.Fatalf("restart must resume the conversation: first=%s resumed=%s", first, got)
	}
}

func TestContract8_BurstMergesIntoOneTurn(t *testing.T) {
	fc := newFakeClient()
	rec := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "merged"}}})
	rt := storeRuntimeClient(t, rec)
	b := newBot(t, fc, rt)
	b.debounce = 60 * time.Millisecond

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "part one"})
	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "part two"})

	if !waitForReply(t, fc, "merged") {
		t.Fatal("burst never ran")
	}
	calls := rec.CallsSnapshot()
	if len(calls) != 1 {
		t.Fatalf("model calls = %d, want 1 (fragments merged)", len(calls))
	}
	last := calls[0].Msgs[len(calls[0].Msgs)-1]
	if !strings.Contains(last.Content, "part one") || !strings.Contains(last.Content, "part two") {
		t.Fatalf("merged turn missing a fragment: %q", last.Content)
	}
}
