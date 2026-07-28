package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
)

// assertNoWakeFor drains the runtime bus for d and fails if any Wake for
// session s arrives. A cron job's pinned parent must never be woken.
func assertNoWakeFor(t *testing.T, rt *Runtime, s *Session, d time.Duration) {
	t.Helper()
	id := s.ID()
	deadline := time.After(d)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind == Wake && ev.Session == id {
				t.Fatalf("unexpected Wake for cron parent session %s", id)
			}
		case <-deadline:
			return
		}
	}
}

// TestCronSubagentFollowUpPostsViaHost is the lingering-cron behavior on the
// notifier path: a cron dispatch spawns a subagent that starts a bash_bg
// outliving its main turn. In degraded mode (host set, no notifier.md) the
// main turn's result posts raw through the CompletionHost with its cron
// origin, and the later follow-up turn's summary posts the same way — the
// pinned parent is never woken.
func TestCronSubagentFollowUpPostsViaHost(t *testing.T) {
	g := newGatedLLM("main answer", "follow-up answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	host := &fakeHost{}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "cron:followup",
		subagentOpts{cronJob: "followup"})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	<-g.Started // child main turn verifiably in flight

	rt.jobs.mu.Lock()
	child := rt.jobs.jobs[id].child
	rt.jobs.mu.Unlock()
	if child == nil {
		t.Fatal("child session not recorded on the job")
	}
	// A bash_bg job owned by the child, still running when the main turn ends.
	if _, err := rt.jobs.startCommand(child, "sleep", t.TempDir(), []string{"sleep", "0.3"}, nil, false, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	close(g.Release) // main turn completes now

	// The main turn's result posts through the host with cron routing.
	waitFor(t, "main-turn post", func() bool { posts, _, _ := host.snapshot(); return len(posts) >= 1 })
	posts, _, _ := host.snapshot()
	if !strings.Contains(posts[0], "cron=followup") || !strings.Contains(posts[0], "main answer") {
		t.Fatalf("main-turn post = %q", posts[0])
	}

	// The lingering job finishes → a follow-up turn runs → its summary posts
	// the same way, never a wake.
	waitFor(t, "one follow-up turn", func() bool {
		_, _, _, fu, _ := jobSnapshot(rt.jobs, id)
		return fu == 1
	})
	waitFor(t, "follow-up post", func() bool { posts, _, _ := host.snapshot(); return len(posts) >= 2 })
	posts, _, _ = host.snapshot()
	if !strings.Contains(posts[1], "cron=followup") || !strings.Contains(posts[1], "follow-up answer") {
		t.Fatalf("follow-up post = %q", posts[1])
	}
	waitFor(t, "child closed after follow-up", func() bool {
		_, closed, driver, _, _ := jobSnapshot(rt.jobs, id)
		return closed && !driver
	})

	// The parent (pinned cron session) was never woken by any event.
	assertNoWakeFor(t, rt, parent, 200*time.Millisecond)
}

// TestCronSubagentOrphanPostsViaHost covers the orphan path: when follow-ups
// are unavailable (poisoned), a lingering bash_bg completion posts through the
// CompletionHost as a cron-labeled event carrying its subagent origin — the
// parent is never woken.
func TestCronSubagentOrphanPostsViaHost(t *testing.T) {
	g := newGatedLLM("main answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	host := &fakeHost{}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{Name: "cron", Headless: true})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "cron:degrade",
		subagentOpts{cronJob: "degrade"})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	<-g.Started

	rt.jobs.mu.Lock()
	child := rt.jobs.jobs[id].child
	rt.jobs.jobs[id].noFollowUps = true // poison: no follow-up turns
	rt.jobs.mu.Unlock()
	if _, err := rt.jobs.startCommand(child, "sleep", t.TempDir(), []string{"sleep", "0.3"}, nil, false, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	close(g.Release)

	// Main-turn result posts via the host.
	waitFor(t, "main-turn post", func() bool { posts, _, _ := host.snapshot(); return len(posts) >= 1 })

	// The poisoned job's completion takes the orphan path → a second post
	// labeled with its subagent origin (no follow-up turn ever runs).
	waitFor(t, "orphan post", func() bool {
		posts, _, _ := host.snapshot()
		if len(posts) < 2 {
			return false
		}
		return strings.Contains(posts[1], "started by subagent "+id)
	})
	_, _, _, followUps, _ := jobSnapshot(rt.jobs, id)
	if followUps != 0 {
		t.Fatalf("poisoned cron subagent ran %d follow-up turns, want 0", followUps)
	}
	waitFor(t, "child closed", func() bool {
		_, closed, _, _, _ := jobSnapshot(rt.jobs, id)
		return closed
	})

	assertNoWakeFor(t, rt, parent, 200*time.Millisecond)
}
