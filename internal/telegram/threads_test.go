//go:build unix

package telegram

import (
	"context"
	"strconv"
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

func testStore(t *testing.T) func() *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return func() *runs.Store { return st }
}

func TestThreadIndexRoundtrip(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	if err := idx.SetCurrent("sess-abc"); err != nil {
		t.Fatal(err)
	}
	got, ok := idx.Current()
	if !ok || got != "sess-abc" {
		t.Fatalf("Current() = %q, %v; want sess-abc, true", got, ok)
	}
}

func TestThreadIndexPersistence(t *testing.T) {
	st := testStore(t)
	idx := NewThreadIndex(st, "telegram")
	if err := idx.SetCurrent("s1"); err != nil {
		t.Fatal(err)
	}

	// A second index over the same store (as after a restart) sees the marker.
	idx2 := NewThreadIndex(st, "telegram")
	got, ok := idx2.Current()
	if !ok || got != "s1" {
		t.Fatalf("Current() after restart = %q, %v; want s1, true", got, ok)
	}
}

func TestThreadIndexUnknown(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	if got, ok := idx.Current(); ok {
		t.Fatalf("Current() = %q, true; want _, false", got)
	}
}

// An empty recorded id (the /new that cleared the marker) reads as absent.
func TestThreadIndexClearedMarkerReadsAbsent(t *testing.T) {
	st := testStore(t)
	idx := NewThreadIndex(st, "telegram")
	if err := idx.SetCurrent("s1"); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetCurrent(""); err != nil {
		t.Fatal(err)
	}
	if got, ok := idx.Current(); ok {
		t.Fatalf("Current() after clear = %q, true; want _, false", got)
	}
	// And across a restart, from the store.
	if got, ok := NewThreadIndex(st, "telegram").Current(); ok {
		t.Fatalf("Current() after clear + restart = %q, true; want _, false", got)
	}
}

// Two front-end surfaces over one store never cross-resolve each other's markers.
func TestThreadIndexSurfaceIsolation(t *testing.T) {
	st := testStore(t)
	tg := NewThreadIndex(st, "telegram")
	sv := NewThreadIndex(st, "serve")
	if err := tg.SetCurrent("tg-sess"); err != nil {
		t.Fatal(err)
	}
	if err := sv.SetCurrent("serve-sess"); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewThreadIndex(st, "telegram").Current(); got != "tg-sess" {
		t.Fatalf("telegram Current() = %q", got)
	}
	if got, _ := NewThreadIndex(st, "serve").Current(); got != "serve-sess" {
		t.Fatalf("serve Current() = %q", got)
	}
}

// newResumeTestBot builds a Bot whose session store and current-marker store
// are the SAME runs.Store — unlike newBot/mkThreads, which give the bot's
// ThreadIndex its own throwaway store disconnected from the runtime's. That
// separation is fine for the marker tests above, but a marker/session
// divergence test needs EndSession (on the store sessions actually live in)
// and Current() (on the store the marker actually lives in) to agree on which
// store that is, the way production wiring does (openThreads in
// cmd/shell3/hostwiring.go resolves both from the same rt.Parts().Store()).
func newResumeTestBot(t *testing.T) (*Bot, *runs.Store) {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		scripts := make([]fakellm.Script, 8)
		for i := range scripts {
			scripts[i] = fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}}
		}
		return chat.Config{
			LLM: fakellm.New(scripts...), ModeLabel: "code",
			Store: st, Headless: o.Headless,
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })

	idx := NewThreadIndex(func() *runs.Store { return st }, "telegram")
	b := NewBot(newFakeClient(), rt, 42, idx)
	b.debounce = time.Millisecond
	return b, st
}

// TestMainSession_MarkerConsistentAfterRestartResume reproduces the observed
// live state: threads.current-session pointed at a session that had ended
// days earlier while a different, newer session was the actually-live
// conversation. mainSession resumes the marker's id and then SetCurrents the
// result, so the two should never be able to diverge from this path alone —
// this test checks whether they can.
//
// The task brief that seeded this test (and its original name,
// …AdvancesCurrentMarker) assumed ResumeID always mints a fresh session id on
// resume (so the test asserted second.ID() != firstID after an EndSession).
// That assumption does not hold: shell3.Session.ID() returns the StoreID it
// was resumed with unchanged (internal/shell3/session.go's newSession sets
// storeID = resumeID on the resume path; chat.NewSession takes that id
// as-is) — "a restart resumes the same conversation" per CLAUDE.md is the
// intended behaviour, not a bug. The marker therefore never "advances" here;
// this test checks what actually matters: after mainSession recreates the
// resumed session, the PERSISTED marker (re-read via a fresh ThreadIndex over
// the same store, the way a real restart would) still names that session.
// This test PASSES against the current code — the divergence is not
// reproducible from mainSession's own resume path. See
// TestMainSession_MarkerConsistentAfterMainHandleCleared below and
// TestMainSession_MarkerSurvivesCompaction for the actual repro (host-managed
// compaction rolling the session id without re-recording the marker).
func TestMainSession_MarkerConsistentAfterRestartResume(t *testing.T) {
	b, st := newResumeTestBot(t)

	first, err := tconv(b).mainSession()
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.ID()
	if got, _ := tconv(b).index.Current(); got != firstID {
		t.Fatalf("marker after first session = %q, want %q", got, firstID)
	}

	// A real conversation has messages by the time it ends; an empty session
	// leaves no trace (EndSession deletes rather than marks ended). Append one
	// so EndSession exercises the same path a real restart would find.
	if err := st.AppendMessage(firstID, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: drop the in-memory handle, end the stored session.
	if err := st.EndSession(firstID); err != nil {
		t.Fatal(err)
	}
	c := tconv(b)
	c.mu.Lock()
	c.main = nil
	c.mu.Unlock()

	second, err := tconv(b).mainSession()
	if err != nil {
		t.Fatal(err)
	}
	// mainSession resumes the SAME store id (shell3.Session.ID() returns
	// StoreID unchanged — resuming does not mint a new row); the marker must
	// therefore still agree with it. tconv(b).index's in-memory map already agrees
	// by construction (mainSession just wrote it in this process) — that alone
	// wouldn't catch a lost STORE write, which is the failure this task's fix
	// targets. Re-read through a fresh ThreadIndex over the same store, the
	// way a restarting process actually would.
	reread := NewThreadIndex(func() *runs.Store { return st }, TelegramSurface(b.homeChat))
	got, ok := reread.Current()
	if !ok || got != second.ID() {
		t.Fatalf("persisted marker = %q, want the resumed session %q (stale marker forks the conversation)", got, second.ID())
	}
}

// TestMainSession_MarkerConsistentAfterMainHandleCleared covers the brief's
// Step 3b path: a /reload swapping the runtime's Parts generation. It is
// included per the task's Step 1/3b instructions even though Step 2 above
// already passed (this repro attempt is not the divergence's actual
// mechanism — see the task-1 report). shell3.RuntimeForTest builds a Runtime
// with no Parts (no config dir, no store-generation machinery), so Reload
// here is expected to be a no-op/error, and this fixture's ThreadIndex closes
// over a fixed store regardless — it CANNOT exercise the production
// store-handle swap (cmd/shell3/hostwiring.go's openThreads re-resolves via
// rt.Parts().Store() on every call), despite the name this test used to
// carry. What it actually verifies is narrower: clearing the in-memory
// tconv(b).main handle and calling mainSession() again still leaves the persisted
// marker naming the (re-resumed) live session under this fixture.
func TestMainSession_MarkerConsistentAfterMainHandleCleared(t *testing.T) {
	b, st := newResumeTestBot(t)
	first, err := tconv(b).mainSession()
	if err != nil {
		t.Fatal(err)
	}
	_, _ = b.rt.Reload() // swap Parts generation (a no-op on this Parts-less test runtime)
	c := tconv(b)
	c.mu.Lock()
	c.main = nil
	c.mu.Unlock()
	second, err := tconv(b).mainSession()
	if err != nil {
		t.Fatal(err)
	}
	reread := NewThreadIndex(func() *runs.Store { return st }, TelegramSurface(b.homeChat))
	got, ok := reread.Current()
	if !ok || got != second.ID() {
		t.Fatalf("marker %q != live session %q after reload", got, second.ID())
	}
	_ = first
}

// TestMainSession_MarkerSurvivesCompaction is the task-7 repro: the live
// install's marker pointed at a session that had ended three days before the
// conversation actually in progress. internal/chat's compactInto (called
// from inside a normal turn, once auto-compaction's prompt-token threshold
// trips) rolls the session onto a NEW runs-store row mid-conversation — and
// nothing on that path knows about telegram's current-session marker
// (internal/chat and internal/shell3 must never import internal/telegram),
// so the marker is left naming the pre-compaction id. This test drives real
// turns through the bot (the only place compaction actually fires in
// production — there is no manual /compact command) until auto-compaction
// trips inside one of them, then rereads the PERSISTED marker through a
// fresh ThreadIndex over the same store, the way a restarting process would.
func TestMainSession_MarkerSurvivesCompaction(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// 12 "builder" turns pile up real history (big user messages so the
	// eventual compaction has a substantial head to summarize, clearing the
	// compactionFloor without needing to fake the estimator). Only the last
	// builder turn reports a prompt-token usage above CompactAt, so
	// maybeCompact fires at the START of turn 13 — exactly the auto path a
	// live conversation takes, never a forced/manual compact.
	const builderTurns = 12
	scripts := make([]fakellm.Script, 0, builderTurns+2)
	for i := 0; i < builderTurns; i++ {
		usage := &llm.Usage{PromptTokens: 10, TotalTokens: 10} // below CompactAt=100: no trigger yet
		if i == builderTurns-1 {
			usage = &llm.Usage{PromptTokens: 5000, TotalTokens: 5000} // primes the threshold for turn 13
		}
		scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "ok"},
			{Usage: usage},
		}})
	}
	// Turn 13, call 1: the quiet compaction summary (maybeCompact fires before
	// the turn's own answer).
	scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}})
	// Turn 13, call 2: the turn's own answer, against the now-compacted history.
	scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "final answer"},
		{Usage: &llm.Usage{PromptTokens: 50, TotalTokens: 50}},
	}})

	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM: fakellm.New(scripts...), ModeLabel: "code",
			Store: st, Headless: o.Headless,
			AgentKnobs: chat.AgentKnobs{CompactAt: 100, KeepRecent: 50},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })

	idx := NewThreadIndex(func() *runs.Store { return st }, "telegram")
	fc := newFakeClient()
	b := NewBot(fc, rt, 42, idx)
	b.debounce = time.Millisecond

	big := strings.Repeat("x", 2000) // ~500 estimated tokens per message
	ctx := context.Background()
	var before string
	for i := 0; i < builderTurns+1; i++ {
		id := strconv.Itoa(i + 1)
		want := i + 1
		b.handleMsg(ctx, Msg{ChatID: 42, SenderID: 42, ID: id, Text: big})
		waitFor(t, func() bool { return len(fc.sentReplies()) >= want })
		if i == 0 {
			c := tconv(b)
			c.mu.Lock()
			before = c.main.ID()
			c.mu.Unlock()
		}
	}

	c := tconv(b)
	c.mu.Lock()
	after := c.main.ID()
	c.mu.Unlock()
	if after == before {
		t.Fatal("compaction did not roll the store id; this test proves nothing — fix the setup")
	}

	reread := NewThreadIndex(func() *runs.Store { return st }, TelegramSurface(b.homeChat))
	got, ok := reread.Current()
	if !ok || got != after {
		t.Fatalf("marker = %q, want the post-compaction session %q — a restart would resume the stale one and orphan the conversation", got, after)
	}
}

// Concurrent SetCurrent calls are race-free and leave one of the written ids
// as the marker (last write wins).
func TestThreadIndexConcurrentSetCurrent(t *testing.T) {
	idx := NewThreadIndex(testStore(t), "telegram")
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_ = idx.SetCurrent("s" + strconv.Itoa(n))
		}(i)
	}
	wg.Wait()
	got, ok := idx.Current()
	if !ok || !strings.HasPrefix(got, "s") {
		t.Fatalf("Current() = %q, %v; want one of the written ids", got, ok)
	}
}
