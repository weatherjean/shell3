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

// Case 1: a non-reply message on an idle bot creates a fresh session, runs the
// turn, and posts the reply as a Telegram reply to the inbound message; both
// the inbound id and the sent reply id are recorded in the thread index.
func TestContract1_FreshSessionThreadsReply(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "fresh reply")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "100", Text: "hi"})

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && strings.Contains(r.text, "fresh reply")
	})
	r, _ := fc.lastReply()
	if r.replyTo != "100" {
		t.Fatalf("reply must thread to inbound message 100, got replyTo=%s", r.replyTo)
	}
	id, ok := b.threads.Lookup("100")
	if !ok || id == "" {
		t.Fatal("inbound message must be recorded in the thread index")
	}
	if _, ok := b.threads.Lookup(r.msgID); !ok {
		t.Fatal("the bot's own reply must be recorded too (advances the thread anchor)")
	}
}

// Case 2: a reply to a recorded message resumes THAT thread's session (same
// store id), not a fresh one.
func TestContract2_ReplyResumesMappedSession(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "r")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "100", Text: "first"})
	if !waitForReply(t, fc, "r") {
		t.Fatal("first turn produced no reply")
	}
	first, ok := b.threads.Lookup("100")
	if !ok {
		t.Fatal("first message was not recorded")
	}
	// Let the idle session retire so the reply exercises the ResumeID path.
	waitFor(t, func() bool { b.mu.Lock(); n := len(b.live); b.mu.Unlock(); return n == 0 })

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "101", ReplyToID: "100", Text: "second"})
	waitFor(t, func() bool { _, ok := b.threads.Lookup("101"); return ok })
	second, _ := b.threads.Lookup("101")
	if second != first {
		t.Fatalf("a reply to a recorded message must resume the same session: first=%s second=%s", first, second)
	}
}

// Case 3: a reply to an UNKNOWN message id (a courtesy notice, a status line,
// a pre-index message) is answered by the INTERFACE with a fixed can't-continue
// notice — no session, no model call, no guessing at lost context.
func TestContract3_UnknownReplyGetsCantContinueNotice(t *testing.T) {
	fc := newFakeClient()
	rec := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	rt := storeRuntimeClient(t, rec)
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{
		ChatID: 42, ID: "102", ReplyToID: "999", ReplyTo: "QUOTED SNIPPET", Text: "can you pick up from here?",
	})
	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "102" && strings.Contains(r.text, "can't continue")
	})
	if _, ok := b.threads.Lookup("102"); ok {
		t.Fatal("an unknown-reply message must not create or record a session")
	}
	if len(rec.CallsSnapshot()) != 0 {
		t.Fatal("an unknown-reply message must not reach the model")
	}
}

// Case 4: a message arriving mid-turn gets a courtesy reply and is dropped — no
// session is created or recorded for it. One main-agent turn at a time.
func TestContract4_MidTurnMessageCourtesyDropped(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("first turn never started")
	}

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "2", Text: "steer"})

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "2" &&
			strings.Contains(r.text, "a turn is running") && strings.Contains(r.text, "/stop")
	})
	if _, ok := b.threads.Lookup("2"); ok {
		t.Fatal("a dropped mid-turn message must not create or record a session")
	}

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/stop"}) // unwind
}

// Case 5: /stop mid-turn cancels the active turn but never kills background
// jobs; the reply says so.
func TestContract5_StopCancelsTurnKeepsJobs(t *testing.T) {
	fc := newFakeClient()
	blk := fakellm.NewBlocking()
	rt := shell3test.NewRuntimeForTestClient(t, blk)
	b := newBot(t, fc, rt)

	go b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "1", Text: "work"})
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("turn never started")
	}

	b.handleCommand(context.Background(), Msg{ChatID: 42, Text: "/stop"})

	waitFor(t, func() bool { b.mu.Lock(); a := b.turnActive; b.mu.Unlock(); return !a })
	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
}

// Fix wave (Finding 2): /stop cancels the main turn but must NOT cancel a
// background job. Dispatch a real blocking subagent (like TestContract6), take
// the turn slot to stand in for an in-flight main turn, /stop it, and assert the
// job is still running afterward.
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

	// Stand in for an in-flight main turn holding the slot, so /stop has a turn to
	// cancel.
	ctx := context.Background()
	b.mu.Lock()
	_, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()
	defer cancel()

	b.handleCommand(ctx, Msg{ChatID: 42, Text: "/stop"})

	all := strings.Join(fc.sentTexts(), "\n")
	if !strings.Contains(all, "stopped the turn") || !strings.Contains(all, "background jobs keep running") {
		t.Fatalf("stop reply must state background jobs keep running, got %v", fc.sentTexts())
	}
	// The job survives /stop: its child session runs on its own ctx, untouched by
	// the main turn's cancellation. Give any errant cancellation a moment to land.
	waitFor(t, func() bool { return true })
	if !b.sessionHasRunningJob(sess) {
		t.Fatal("/stop must not cancel a background job — it should still be running")
	}
}

// Fix wave (Finding 1): the turn slot is held through delivery AND retirement,
// so a Wake arriving while a turn finishes queues (it cannot start a second turn
// racing the retire), retireOrKeep KEEPS the session (it has the queued input
// the wake will drain), and the queued wake turn runs only after the slot is
// released — on a session that was never Closed out from under it. Under the old
// early-release ordering the wake turn could claim the session and then be
// aborted by the retiring turn's Close; this pins that closed.
func TestFixWave_RetireHoldsSlotSoConcurrentWakeIsNotAborted(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "wake reply")
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(sess)
	b.mu.Lock()
	b.lastMsg[sess.ID()] = "500"
	b.mu.Unlock()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	// Turn A takes the slot and has finished its model call — the point at which
	// the OLD code released the slot early, before retiring.
	b.mu.Lock()
	_, cancel := b.takeSlotLocked(ctx)
	b.mu.Unlock()

	// A Wake for the SAME session arrives while turn A still holds the slot: it
	// must land in the wake queue, not start a second turn.
	go b.consumeWakes(ctx)
	sess.Interject("bg result landed") // queues input + emits a Wake
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return contains(b.wakeQueue, sess.ID())
	})
	// While the slot is held no wake turn may have run: no reply is posted yet.
	if _, ok := fc.lastReply(); ok {
		t.Fatal("a wake turn started while turn A held the slot — the slot must serialize turns")
	}

	// Turn A runs its end-of-turn tail: retire (must KEEP — the session has the
	// queued input) then release the slot then drain the queued wake.
	b.afterTurn(ctx, sess, cancel)

	// The queued wake turn ran and posted its reply to the thread anchor — proof
	// the session survived retirement and the wake was not aborted mid-flight.
	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "500" && strings.Contains(r.text, "wake reply")
	})
}

// Case 6: a session with a running background job stays live after its turn (it
// is not Closed), and a Wake for it while idle runs a follow-up turn that posts
// its reply into that thread's latest message.
func TestContract6_RunningJobKeepsSessionLiveAndWakeReplies(t *testing.T) {
	fc := newFakeClient()
	rt, blk := splitRuntime(t, "job narrated")
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(sess)
	b.mu.Lock()
	b.lastMsg[sess.ID()] = "200"
	b.mu.Unlock()

	// Start a background subagent job that stays running (headless → blocking).
	if _, err := sess.Dispatch("", "bg work", shell3.DispatchOpts{Direct: true}); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	select {
	case <-blk.Started:
	case <-time.After(2 * time.Second):
		t.Fatal("background job never started")
	}

	// retireOrKeep must KEEP a session that has a running job.
	b.retireOrKeep(sess)
	b.mu.Lock()
	_, stillLive := b.live[sess.ID()]
	b.mu.Unlock()
	if !stillLive {
		t.Fatal("a session with a running job must stay live, not be closed")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.consumeWakes(ctx)
	sess.Interject("bg result landed") // queues input + emits a Wake

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "200" && strings.Contains(r.text, "job narrated")
	})
}

// Case 7: a Wake arriving mid-turn is queued (not run), and the queue drains
// after the turn finishes.
func TestContract7_WakeMidTurnQueuesThenDrains(t *testing.T) {
	fc := newFakeClient()
	rt, _ := newFakeRuntime(t, "queued reply")
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{Agent: "code"})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(sess)
	b.mu.Lock()
	b.lastMsg[sess.ID()] = "300"
	b.turnActive = true // simulate a turn holding the slot
	b.mu.Unlock()

	sess.Interject("bg result") // queue input for the wake turn to drain

	b.dispatchWake(context.Background(), sess.ID())
	b.mu.Lock()
	queued := contains(b.wakeQueue, sess.ID())
	b.mu.Unlock()
	if !queued {
		t.Fatal("a Wake arriving mid-turn must land in the wake queue")
	}

	// The turn finishes: the queue drains and the wake turn runs.
	b.mu.Lock()
	b.turnActive = false
	b.mu.Unlock()
	b.startNextWake(context.Background())

	waitFor(t, func() bool {
		r, ok := fc.lastReply()
		return ok && r.replyTo == "300" && strings.Contains(r.text, "queued reply")
	})
}

// Case 8: a turn ending with no running jobs and an empty inbox closes and
// forgets the session (dropped from live + lastMsg).
func TestContract8_IdleSessionRetired(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "done")
	b := newBot(t, fc, rt)

	b.handleMsg(context.Background(), Msg{ChatID: 42, ID: "400", Text: "hi"})
	if !waitForReply(t, fc, "done") {
		t.Fatal("turn produced no reply")
	}
	waitFor(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return len(b.live) == 0 && len(b.lastMsg) == 0
	})
}

// Case 8b: an adopted (pinned) session — the persistent cron dispatch parent —
// survives retirement, so it stays live for the /jobs and /runs views
// (and never has its store record ended out from under future cron dispatches).
// Its cron completions are delivered by the notifier/CompletionHost, so it is never woken and
// runs no turn of its own; the invariant that survives is only that retirement
// keeps it.
func TestPinnedAdoptedSessionSurvivesRetirement(t *testing.T) {
	fc := newFakeClient()
	rt := storeRuntime(t, "unused")
	b := newBot(t, fc, rt)

	sess, err := rt.Session(shell3.SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatalf("Session: %v", err)
	}
	b.AdoptSession(sess) // pins the session

	// retireOrKeep on a fully-idle pinned session must KEEP it (not Close it).
	b.retireOrKeep(sess)

	b.mu.Lock()
	_, stillLive := b.live[sess.ID()]
	b.mu.Unlock()
	if !stillLive {
		t.Fatal("an adopted (pinned) session must survive retirement, not be closed")
	}
}
