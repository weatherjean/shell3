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

// TestCronSubagentFollowUpMailsAgent is the lingering-cron behavior on the
// mail path: a cron dispatch spawns a subagent that starts a bash_bg
// outliving its main turn. The main turn's result arrives as agent mail (a
// fresh quiet turn — cron has no live owner), and the later follow-up turn's
// summary rides the same route — the pinned parent is never woken.
func TestCronSubagentFollowUpMailsAgent(t *testing.T) {
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

	// The main turn's result arrives as agent mail carrying the cron origin.
	waitFor(t, "main-turn mail", func() bool { _, _, fresh := host.snapshot(); return len(fresh) >= 1 })
	_, _, fresh := host.snapshot()
	if !strings.Contains(fresh[0], "followup") || !strings.Contains(fresh[0], "main answer") {
		t.Fatalf("main-turn mail = %q", fresh[0])
	}

	// The lingering job finishes → a follow-up turn runs → its summary rides
	// the same mail route, never a wake of the pinned parent.
	waitFor(t, "one follow-up turn", func() bool {
		_, _, _, fu, _ := jobSnapshot(rt.jobs, id)
		return fu == 1
	})
	waitFor(t, "follow-up mail", func() bool { _, _, fresh := host.snapshot(); return len(fresh) >= 2 })
	_, _, fresh = host.snapshot()
	if !strings.Contains(fresh[1], "followup") || !strings.Contains(fresh[1], "follow-up answer") {
		t.Fatalf("follow-up mail = %q", fresh[1])
	}
	waitFor(t, "child closed after follow-up", func() bool {
		_, closed, driver, _, _ := jobSnapshot(rt.jobs, id)
		return closed && !driver
	})

	// The parent (pinned cron session) was never woken by any event.
	assertNoWakeFor(t, rt, parent, 200*time.Millisecond)
}

// TestCronSubagentOrphanFloors covers the orphan path: when follow-ups are
// unavailable (poisoned), the lingering bash_bg is cascade-cancelled at the
// main turn's end, so its completion arrives FAILED — and a failure is never
// silent: the ⚠️ floor posts with the cron label. The pinned parent is never
// woken, and no fresh agent turn is spent on an ownerless failure.
func TestCronSubagentOrphanFloors(t *testing.T) {
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

	// Main-turn result arrives as agent mail.
	waitFor(t, "main-turn mail", func() bool { _, _, fresh := host.snapshot(); return len(fresh) >= 1 })

	// The poisoned job is cascade-cancelled → its completion is a failure →
	// the ⚠️ floor posts with the cron label (no follow-up turn ever runs,
	// and no fresh turn is spent on it).
	waitFor(t, "orphan floor post", func() bool {
		posts, _, _ := host.snapshot()
		if len(posts) < 1 {
			return false
		}
		return strings.Contains(posts[0], "⚠️") && strings.Contains(posts[0], "cron=degrade")
	})
	if _, _, fresh := host.snapshot(); len(fresh) != 1 {
		t.Fatalf("fresh = %v, want only the main-turn mail", fresh)
	}
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
