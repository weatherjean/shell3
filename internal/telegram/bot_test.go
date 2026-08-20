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

// storeRuntimeClient builds a Runtime whose sessions share a real file-native
// runs store — so Session.ID() is a stable non-empty store id, which the thread
// index and resume paths need. The given client backs every session.
func storeRuntimeClient(t *testing.T, client chat.LLMClient) *shell3.Runtime {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM: client, ModeLabel: "code", AgentNames: []string{"code"},
			Headless: o.Headless, Store: st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// storeRuntime is storeRuntimeClient with a fakellm that always replies `reply`.
func storeRuntime(t *testing.T, reply string) *shell3.Runtime {
	return storeRuntimeClient(t, fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: reply}}}))
}

// splitRuntime builds a Runtime whose main (non-headless) sessions reply with
// `reply`, while headless children (subagents) run a BlockingClient — so a
// dispatched subagent stays verifiably in flight. Returns the runtime and the
// shared blocking client (its Started channel closes when a child turn begins).
func splitRuntime(t *testing.T, reply string) (*shell3.Runtime, *fakellm.BlockingClient) {
	t.Helper()
	blk := fakellm.NewBlocking()
	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		if o.Headless {
			return chat.Config{LLM: blk, ModeLabel: "code", AgentNames: []string{"code"}, Headless: true}, nil
		}
		scripts := []fakellm.Script{{Events: []llm.StreamEvent{{TextDelta: reply}}}}
		return chat.Config{LLM: fakellm.New(scripts...), ModeLabel: "code", AgentNames: []string{"code"}}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	return rt, blk
}

// Contract 1: a message on an idle bot creates THE conversation, runs the
// turn, posts the reply threaded to the inbound message, and persists the
// session id as the current-conversation marker.
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
	b.mu.Lock()
	main := b.main
	b.mu.Unlock()
	if main == nil {
		t.Fatal("no main session after the first turn")
	}
	if id, ok := b.current.Current(); !ok || id != main.ID() {
		t.Fatalf("current marker = %q,%v want %q", id, ok, main.ID())
	}
}

// Contract 2 (the core of the model): a SECOND bare message — no Telegram
// reply, no buttons — continues the SAME session. Typing without replying
// never forks the conversation.
func TestContract2_BareMessageContinuesTheConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "r")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "100", Text: "first"})
	if !waitForReply(t, fc, "r") {
		t.Fatal("first turn produced no reply")
	}
	b.mu.Lock()
	first := b.main.ID()
	b.mu.Unlock()

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "101", Text: "yes please"})
	waitFor(t, func() bool { return len(fc.sentReplies()) >= 2 })
	b.mu.Lock()
	got := b.main.ID()
	b.mu.Unlock()
	if got != first {
		t.Fatalf("bare message forked the conversation: first=%s second=%s", first, got)
	}
}

// Contract 3: a Telegram reply — even to a message the bot never recorded —
// also continues the conversation, and the quoted text reaches the model as
// context. Replies are context hints, not session switches.
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
	b.mu.Lock()
	first := b.main.ID()
	b.mu.Unlock()

	b.handleMsg(context.Background(), Msg{
		ChatID: 42, SenderID: 42, ID: "2", ReplyToID: "999", ReplyTo: "QUOTED SNIPPET", Text: "pick up from here",
	})
	if !waitForReply(t, fc, "two") {
		t.Fatal("reply turn produced no reply")
	}
	b.mu.Lock()
	got := b.main.ID()
	b.mu.Unlock()
	if got != first {
		t.Fatalf("a reply forked the conversation: first=%s second=%s", first, got)
	}
	calls := rec.CallsSnapshot()
	last := calls[len(calls)-1].Msgs[len(calls[len(calls)-1].Msgs)-1]
	if !strings.Contains(last.Content, "> QUOTED SNIPPET") {
		t.Fatalf("quoted reply context missing from the model prompt: %q", last.Content)
	}
}

// Contract 4 (steering): a TEXT message arriving mid-turn STEERS the running
// turn — injected into the session inbox for the next round boundary, never
// parked behind the turn. Nothing posts; the anchor advances to it.
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

	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()
	if sess == nil {
		t.Fatal("no main session mid-turn")
	}
	waitFor(t, func() bool { return sess.HasQueuedSteer() })
	b.mu.Lock()
	queued := len(b.mailQueue)
	anchor := b.mainAnchor
	b.mu.Unlock()
	if queued != 0 {
		t.Fatalf("steered text must not also queue (mailQueue=%d)", queued)
	}
	if anchor != "2" {
		t.Fatalf("anchor = %q, want the steering message id", anchor)
	}
	if r, ok := fc.lastReply(); ok {
		t.Fatalf("steering must be silent, got reply %q", r.text)
	}
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})
}

// Contract 4b: a mid-turn message CARRYING MEDIA queues (its preflight needs a
// turn goroutine) and drains after the turn.
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
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.mailQueue) == 1
	})
	b.mu.Lock()
	b.mailQueue = nil
	b.mu.Unlock()
	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})
}

// Contract 4c: a steer that lands AFTER the turn's final round boundary is
// caught up by its own POSTED turn — never silently absorbed into a later
// quiet turn.
func TestContract4_SteerCatchupPostsReply(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "caught up")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "caught up") {
		t.Fatal("first turn produced no reply")
	}
	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()

	// Simulate the missed-boundary steer: it sits in the inbox with no turn
	// running.
	sess.Interject("also do this")
	before := len(fc.sentReplies())
	b.startNextWork(context.Background())

	waitFor(t, func() bool { return len(fc.sentReplies()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("catch-up turn must drain the queued steer")
	}
}

// Contract 5: /stop mid-turn cancels the active turn but never kills
// background jobs; the reply says so.
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

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	waitFor(t, func() bool { b.mu.Lock(); a := b.turnActive; b.mu.Unlock(); return !a })
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
}

// Contract 5b: /stop cancels the main turn but must NOT cancel a background
// job.
func TestContract5_StopKeepsBackgroundJobsRunning(t *testing.T) {
	fc := newFakeClient()
	rt, blk := splitRuntime(t, "done")
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(sess)

	// Start a background subagent job that stays running (headless → blocking).
	if _, err := sess.Dispatch("", "bg work", shell3.DispatchOpts{Direct: true}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("background job never started")
	}

	ctx := context.Background()
	b.mu.Lock()
	_, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	defer cancel()

	b.handleCommand(ctx, Msg{ChatID: 42, SenderID: 42, Text: "/stop"})

	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
	waitFor(t, func() bool { return true })
	if !b.sessionHasRunningJob(sess) {
		t.Fatal("/stop must not cancel a background job — it should still be running")
	}
}

// Contract 6: a Wake for the main conversation arriving mid-turn marks
// pending (not a second turn), and drains as a quiet mail turn after the slot
// frees.
func TestContract6_WakeMidTurnPendsThenDrains(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "queued reply")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "queued reply") {
		t.Fatal("first turn produced no reply")
	}
	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()

	b.mu.Lock()
	b.turnActive = true // simulate a turn holding the slot
	b.mu.Unlock()

	sess.NotifyText("bg result") // agent mail (a notice), NOT user steering
	b.dispatchWake(context.Background(), sess.ID())
	b.mu.Lock()
	pending := b.wakePending
	b.mu.Unlock()
	if !pending {
		t.Fatal("a Wake arriving mid-turn must mark wakePending")
	}

	b.mu.Lock()
	b.turnActive = false
	b.mu.Unlock()
	b.startNextWork(context.Background())

	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive && !b.wakePending && !sess.HasQueuedInput()
	})
	// The wake turn's reply reaches the user as ✉️ agent mail — one channel.
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "✉️ queued reply")
	})
}

// Contract 7: a Wake for anything that is NOT the main conversation (the cron
// parent, a /new'd-away session) is dropped — no turn, no post.
func TestContract7_ForeignWakeDropped(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)

	cron, err := rt.Session(shell3.SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(cron)

	b.dispatchWake(context.Background(), cron.ID())
	b.mu.Lock()
	active, pending := b.turnActive, b.wakePending
	b.mu.Unlock()
	if active || pending {
		t.Fatal("a wake for the cron parent must be dropped, not run or pended")
	}
}

// Contract 8: /new detaches the conversation — the next message runs in a
// fresh session, and the marker moves with it.
func TestContract8_NewStartsFreshConversation(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "r")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "first"})
	if !waitForReply(t, fc, "r") {
		t.Fatal("first turn produced no reply")
	}
	b.mu.Lock()
	first := b.main.ID()
	b.mu.Unlock()

	// The reply posts BEFORE the turn slot frees, and /new refuses mid-turn —
	// on a slow runner the raw sequence races into that refusal. Wait for the
	// slot the way a real user's next tap naturally would.
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return !b.turnActive
	})

	b.handleCommand(context.Background(), Msg{ChatID: 42, SenderID: 42, Text: "/new"})
	waitFor(t, func() bool {
		return strings.Contains(strings.Join(fc.sentTexts(), "\n"), "fresh conversation")
	})
	b.mu.Lock()
	live := b.main
	b.mu.Unlock()
	if live != nil {
		t.Fatal("/new must detach the main session")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "second"})
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.main != nil
	})
	b.mu.Lock()
	second := b.main.ID()
	b.mu.Unlock()
	if second == first {
		t.Fatal("/new must start a fresh session")
	}
	if id, _ := b.current.Current(); id != second {
		t.Fatalf("current marker = %q, want the new session %q", id, second)
	}
}

// Contract 9: a restart resumes the SAME conversation — a second Bot over the
// same store picks up the persisted current marker.
func TestContract9_RestartResumesTheConversation(t *testing.T) {
	fc := newFakeClient()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	client := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "one"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "two"}}},
	)
	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM: client, ModeLabel: "code", AgentNames: []string{"code"},
			Headless: o.Headless, Store: st,
			AgentKnobs: chat.AgentKnobs{ContextWindow: 4096},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	threads := NewThreadIndex(func() *runs.Store { return st }, "telegram")

	b1 := NewBot(fc, rt, 42, threads)
	b1.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "hello"})
	if !waitForReply(t, fc, "one") {
		t.Fatal("first bot produced no reply")
	}
	b1.mu.Lock()
	first := b1.main.ID()
	b1.mu.Unlock()

	// "Restart": a fresh Bot over the same store and marker.
	b2 := NewBot(fc, rt, 42, threads)
	b2.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "still there?"})
	if !waitForReply(t, fc, "two") {
		t.Fatal("second bot produced no reply")
	}
	b2.mu.Lock()
	got := b2.main.ID()
	b2.mu.Unlock()
	if got != first {
		t.Fatalf("restart must resume the conversation: first=%s resumed=%s", first, got)
	}
}

// Contract 10 (debounce): text fragments arriving back to back — Telegram
// splits long messages into several updates — merge into ONE turn.
func TestContract10_BurstMergesIntoOneTurn(t *testing.T) {
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

// Contract 11: a text arriving during a QUIET (wake) turn must NOT steer into
// it — the quiet turn's reply posts nowhere, so the user's message would be
// silently absorbed. It queues instead and runs as its own posted turn.
func TestContract11_TextDuringQuietTurnQueues(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "answered")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "answered") {
		t.Fatal("first turn produced no reply")
	}

	b.mu.Lock()
	b.turnActive = true
	b.turnQuiet = true // simulate a wake turn holding the slot
	b.mu.Unlock()

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "2", Text: "are you there?"})
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.mailQueue) == 1
	})
	b.mu.Lock()
	live := b.main
	b.mu.Unlock()
	if live.HasQueuedSteer() {
		t.Fatal("text must not steer a quiet turn")
	}
	b.mu.Lock()
	b.turnActive, b.turnQuiet, b.mailQueue = false, false, nil
	b.mu.Unlock()
}

// Contract 12: a Wake that finds queued USER steering runs a POSTED turn —
// the steer that raced a turn's end still gets its answer delivered.
func TestContract12_WakeWithSteerPostsReply(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "steered answer")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, SenderID: 42, ID: "1", Text: "start"})
	if !waitForReply(t, fc, "steered answer") {
		t.Fatal("first turn produced no reply")
	}
	b.mu.Lock()
	sess := b.main
	b.mu.Unlock()
	before := len(fc.sentReplies())

	sess.Interject("and one more thing") // user steering, landed post-turn
	b.dispatchWake(context.Background(), sess.ID())

	waitFor(t, func() bool { return len(fc.sentReplies()) > before })
	if sess.HasQueuedSteer() {
		t.Fatal("posted wake turn must drain the steer")
	}
}
