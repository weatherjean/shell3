package shell3

import (
	"fmt"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
)

func TestDispatchDepth(t *testing.T) {
	g := newGatedLLM("l1 answer", "l2 answer", "l3 answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, Agent: "code"}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}

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

func TestDepth2ResultWakesItsParent(t *testing.T) {
	g := newGatedLLM("l1 answer", "l2 answer", "l1 follow-up")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, Agent: "code"}
	})
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

	if _, err := rt.jobs.startSubagent(c1, "", "level two", "l2", subagentOpts{}); err != nil {
		t.Fatalf("startSubagent l2: %v", err)
	}
	close(g.Release)

	waitFor(t, "l1 runs a follow-up turn", func() bool {
		_, _, _, followUps, ok := jobSnapshot(rt.jobs, l1)
		return ok && followUps >= 1
	})
	_, _, fresh := host.snapshot()
	for _, m := range fresh {
		if strings.Contains(m, "(l2)") {
			t.Fatalf("a depth-2 result leaked to the root conversation instead of its parent:\n%s", m)
		}
	}
}

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

	m.jobs["sub0"].finished = true
	if got := m.runningCountAtDepth(1); got != 2 {
		t.Fatalf("finished depth-1 job still counted: %d", got)
	}
}
