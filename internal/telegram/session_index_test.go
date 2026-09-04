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
	"github.com/weatherjean/shell3/internal/shell3/shell3test"
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

func TestSessionIndexRoundtrip(t *testing.T) {
	idx := NewSessionIndex(testStore(t), "telegram")
	if err := idx.SetCurrent("sess-abc"); err != nil {
		t.Fatal(err)
	}
	got, ok := idx.Current()
	if !ok || got != "sess-abc" {
		t.Fatalf("Current() = %q, %v; want sess-abc, true", got, ok)
	}
}

func TestSessionIndexPersistence(t *testing.T) {
	st := testStore(t)
	idx := NewSessionIndex(st, "telegram")
	if err := idx.SetCurrent("s1"); err != nil {
		t.Fatal(err)
	}

	idx2 := NewSessionIndex(st, "telegram")
	got, ok := idx2.Current()
	if !ok || got != "s1" {
		t.Fatalf("Current() after restart = %q, %v; want s1, true", got, ok)
	}
}

func TestSessionIndexUnknown(t *testing.T) {
	idx := NewSessionIndex(testStore(t), "telegram")
	if got, ok := idx.Current(); ok {
		t.Fatalf("Current() = %q, true; want _, false", got)
	}
}

func TestSessionIndexClearedMarkerReadsAbsent(t *testing.T) {
	st := testStore(t)
	idx := NewSessionIndex(st, "telegram")
	if err := idx.SetCurrent("s1"); err != nil {
		t.Fatal(err)
	}
	if err := idx.SetCurrent(""); err != nil {
		t.Fatal(err)
	}
	if got, ok := idx.Current(); ok {
		t.Fatalf("Current() after clear = %q, true; want _, false", got)
	}
	if got, ok := NewSessionIndex(st, "telegram").Current(); ok {
		t.Fatalf("Current() after clear + restart = %q, true; want _, false", got)
	}
}

func TestSessionIndexSurfaceIsolation(t *testing.T) {
	st := testStore(t)
	tg := NewSessionIndex(st, "telegram")
	sv := NewSessionIndex(st, "other")
	if err := tg.SetCurrent("tg-sess"); err != nil {
		t.Fatal(err)
	}
	if err := sv.SetCurrent("other-sess"); err != nil {
		t.Fatal(err)
	}
	if got, _ := NewSessionIndex(st, "telegram").Current(); got != "tg-sess" {
		t.Fatalf("telegram Current() = %q", got)
	}
	if got, _ := NewSessionIndex(st, "other").Current(); got != "other-sess" {
		t.Fatalf("other Current() = %q", got)
	}
}

func newResumeTestBot(t *testing.T) (*Bot, *runs.Store) {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	rt := shell3test.NewRuntimeForTestConfig(t, func(o shell3.SessionOpts) (chat.Config, error) {
		scripts := make([]fakellm.Script, 8)
		for i := range scripts {
			scripts[i] = fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}}
		}
		return chat.Config{
			LLM:   fakellm.New(scripts...),
			Store: st, Headless: o.Headless,
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })

	idx := NewSessionIndex(func() *runs.Store { return st }, "telegram")
	b := NewBot(newFakeClient(), rt, 42, idx)
	b.debounce = time.Millisecond
	return b, st
}

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

	if err := st.AppendMessage(firstID, llm.Message{Role: llm.RoleUser, Content: "hi"}); err != nil {
		t.Fatal(err)
	}

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
	reread := NewSessionIndex(func() *runs.Store { return st }, roomSurface("telegram", b.homeChat))
	got, ok := reread.Current()
	if !ok || got != second.ID() {
		t.Fatalf("persisted marker = %q, want the resumed session %q (stale marker forks the conversation)", got, second.ID())
	}
}

func TestMainSession_MarkerConsistentAfterMainHandleCleared(t *testing.T) {
	b, st := newResumeTestBot(t)
	first, err := tconv(b).mainSession()
	if err != nil {
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
	reread := NewSessionIndex(func() *runs.Store { return st }, roomSurface("telegram", b.homeChat))
	got, ok := reread.Current()
	if !ok || got != second.ID() {
		t.Fatalf("marker %q != live session %q after reload", got, second.ID())
	}
	_ = first
}

func TestMainSession_MarkerSurvivesCompaction(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })

	const builderTurns = 12
	scripts := make([]fakellm.Script, 0, builderTurns+2)
	for i := 0; i < builderTurns; i++ {
		usage := &llm.Usage{PromptTokens: 10, TotalTokens: 10}
		if i == builderTurns-1 {
			usage = &llm.Usage{PromptTokens: 5000, TotalTokens: 5000}
		}
		scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "ok"},
			{Usage: usage},
		}})
	}
	scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}})
	scripts = append(scripts, fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "final answer"},
		{Usage: &llm.Usage{PromptTokens: 50, TotalTokens: 50}},
	}})

	rt := shell3test.NewRuntimeForTestConfig(t, func(o shell3.SessionOpts) (chat.Config, error) {
		return chat.Config{
			LLM:   fakellm.New(scripts...),
			Store: st, Headless: o.Headless,
			AgentKnobs: chat.AgentKnobs{CompactAt: 100, KeepRecent: 50},
		}, nil
	})
	t.Cleanup(func() { _ = rt.Close() })

	idx := NewSessionIndex(func() *runs.Store { return st }, "telegram")
	fc := newFakeClient()
	b := NewBot(fc, rt, 42, idx)
	b.debounce = time.Millisecond

	big := strings.Repeat("x", 2000)
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

	reread := NewSessionIndex(func() *runs.Store { return st }, roomSurface("telegram", b.homeChat))
	got, ok := reread.Current()
	if !ok || got != after {
		t.Fatalf("marker = %q, want the post-compaction session %q — a restart would resume the stale one and orphan the conversation", got, after)
	}
}

func TestSessionIndexConcurrentSetCurrent(t *testing.T) {
	idx := NewSessionIndex(testStore(t), "telegram")
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
