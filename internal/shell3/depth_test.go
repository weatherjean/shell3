package shell3

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
)

// Delegation is TWO levels. A root session's dispatch is depth 1; that
// subagent may dispatch once more (depth 2); a depth-2 agent may not. The
// refusal is an ERROR THE MODEL READS, not a hidden tool — it names the
// bound and tells the agent to report up, because an agent that finds
// delegation silently unavailable improvises instead (the failure this
// exists to stop was a hand-rolled HTTP client against a model API).
func TestDispatchDepth(t *testing.T) {
	g := newGatedLLM("l1 answer", "l2 answer", "l3 answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}

	// Root → depth 1.
	l1, err := rt.jobs.startSubagent(parent, "", "level one", "l1", subagentOpts{})
	if err != nil {
		t.Fatalf("depth-1 dispatch must be allowed: %v", err)
	}
	<-g.Started
	rt.jobs.mu.Lock()
	c1 := rt.jobs.jobs[l1].child
	d1 := rt.jobs.jobs[l1].depth
	rt.jobs.mu.Unlock()
	if d1 != 1 {
		t.Fatalf("root dispatch depth = %d, want 1", d1)
	}

	// Depth 1 → depth 2, still allowed.
	l2, err := rt.jobs.startSubagent(c1, "", "level two", "l2", subagentOpts{})
	if err != nil {
		t.Fatalf("depth-2 dispatch must be allowed: %v", err)
	}
	rt.jobs.mu.Lock()
	c2 := rt.jobs.jobs[l2].child
	d2 := rt.jobs.jobs[l2].depth
	rt.jobs.mu.Unlock()
	if d2 != 2 {
		t.Fatalf("second-level dispatch depth = %d, want 2", d2)
	}

	// Depth 2 → refused, with a message that teaches.
	_, err = rt.jobs.startSubagent(c2, "", "level three", "l3", subagentOpts{})
	if err == nil {
		t.Fatal("a depth-2 agent must not dispatch — delegation is two levels")
	}
	for _, want := range []string{"two levels", "report"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal must name the bound and the way out; missing %q:\n%s", want, err)
		}
	}
}

// A depth-2 subagent's result must climb to its depth-1 PARENT — injected
// into the parent's still-open child session and answered in a follow-up
// turn — not routed to the root as if the parent were not there. Without
// this the cascade only goes down: a level-1 agent would dispatch work and
// never learn what came back, and the report would surface in the user's
// conversation instead.
func TestDepth2ResultWakesItsParent(t *testing.T) {
	g := newGatedLLM("l1 answer", "l2 answer", "l1 follow-up")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	// wakeOK:false is the production bot: WakeOwner accepts only the main
	// conversation, so a completion whose owner is a SUBAGENT's session falls
	// through to StartFreshTurn — landing in the user's chat. That fall-through
	// is the bug this pins.
	host := &fakeHost{wakeOK: false}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	l1, err := rt.jobs.startSubagent(parent, "", "level one", "l1", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent l1: %v", err)
	}
	<-g.Started
	rt.jobs.mu.Lock()
	c1 := rt.jobs.jobs[l1].child
	rt.jobs.mu.Unlock()

	// l1 dispatches l2, then its own main turn ends — so l1 lingers.
	if _, err := rt.jobs.startSubagent(c1, "", "level two", "l2", subagentOpts{}); err != nil {
		t.Fatalf("startSubagent l2: %v", err)
	}
	close(g.Release)

	// l1 must run a follow-up turn: that only happens if l2's completion was
	// injected into l1's session rather than dispatched at the root.
	waitFor(t, "l1 runs a follow-up turn", func() bool {
		_, _, _, followUps, ok := jobSnapshot(rt.jobs, l1)
		return ok && followUps >= 1
	})
	// l1's OWN report reaching the root is correct — its parent is the root.
	// l2's must not: it belongs to l1.
	_, _, fresh := host.snapshot()
	for _, m := range fresh {
		if strings.Contains(m, "(l2)") {
			t.Fatalf("a depth-2 result leaked to the root conversation instead of its parent:\n%s", m)
		}
	}
}

// The cap counts unfinished jobs, and a LINGERING parent is unfinished. So a
// full rank of depth-1 agents would occupy every slot and refuse every child
// they tried to spawn — and a "cap reached" error is exactly the dead end that
// sends an agent back to improvising. The cap is therefore PER DEPTH: a parent
// never competes with its own children.
func TestCapIsPerDepth(t *testing.T) {
	m := &jobManager{jobs: map[string]*bgJob{}, max: 3}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("sub%d", i)
		m.jobs[id] = &bgJob{id: id, kind: JobSubagent, depth: 1}
	}
	if got := m.runningCountAtDepth(1); got != 3 {
		t.Fatalf("depth-1 running = %d, want 3", got)
	}
	if got := m.runningCountAtDepth(2); got != 0 {
		t.Fatalf("a full depth-1 rank must not count against depth 2; got %d", got)
	}

	// A depth-1 job that FINISHED frees its own rank's slot, as before.
	m.jobs["sub0"].finished = true
	if got := m.runningCountAtDepth(1); got != 2 {
		t.Fatalf("finished depth-1 job still counted: %d", got)
	}
}
