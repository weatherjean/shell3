package shell3

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/notify"
)

// gatedLLM is an llm.Streamer whose FIRST call signals Started and blocks
// until Release is closed (or ctx is cancelled), then emits its text. Later
// calls emit immediately. It records every call's messages so tests can
// assert what context a turn saw.
type gatedLLM struct {
	Started chan struct{}
	Release chan struct{}

	mu    sync.Mutex
	calls int
	texts []string
	Msgs  [][]llm.Message
}

func newGatedLLM(texts ...string) *gatedLLM {
	return &gatedLLM{
		Started: make(chan struct{}), Release: make(chan struct{}),
		texts: texts,
	}
}

func (g *gatedLLM) Stream(ctx context.Context, msgs []llm.Message, _ []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	g.mu.Lock()
	idx := g.calls
	g.calls++
	g.Msgs = append(g.Msgs, append([]llm.Message(nil), msgs...))
	text := g.texts[min(idx, len(g.texts)-1)]
	g.mu.Unlock()
	if idx == 0 {
		close(g.Started)
		select {
		case <-g.Release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	onEvent(llm.StreamEvent{TextDelta: text})
	return nil
}

func (g *gatedLLM) callMsgs() [][]llm.Message {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([][]llm.Message(nil), g.Msgs...)
}

// jobSnapshot copies the keep-open fields of a job under the manager lock.
func jobSnapshot(m *jobManager, id string) (lingering, childClosed, driver bool, followUps int, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[id]
	if j == nil {
		return false, false, false, 0, false
	}
	return j.lingering, j.childClosed, j.driver, j.followUps, true
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func TestSubagentLingersAndRunsFollowUp(t *testing.T) {
	g := newGatedLLM("main answer", "follow-up answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "keep-open test", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	<-g.Started

	rt.jobs.mu.Lock()
	child := rt.jobs.jobs[id].child
	rt.jobs.mu.Unlock()
	if child == nil {
		t.Fatal("child session not recorded on the job")
	}
	if _, err := rt.jobs.startCommand(child, "sleep", t.TempDir(), []string{"sleep", "0.3"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	close(g.Release)
	waitForWake(t, rt, parent)

	waitFor(t, "job lingering", func() bool {
		lingering, _, _, _, ok := jobSnapshot(rt.jobs, id)
		return ok && lingering
	})

	waitForWake(t, rt, parent)
	waitFor(t, "one follow-up turn", func() bool {
		_, _, _, fu, _ := jobSnapshot(rt.jobs, id)
		return fu == 1
	})
	waitFor(t, "child closed after follow-up", func() bool {
		_, closed, driver, _, _ := jobSnapshot(rt.jobs, id)
		return closed && !driver
	})

	for range parent.Send(context.Background(), "and?") {
	}
	calls := g.callMsgs()
	final := calls[len(calls)-1]
	var all strings.Builder
	for _, m := range final {
		all.WriteString(m.Content + "\n")
	}
	if !strings.Contains(all.String(), "follow-up") || !strings.Contains(all.String(), "follow-up answer") {
		t.Fatalf("parent turn context missing the agent_update notice:\n%s", all.String())
	}
}

// TestCancelSubagentCascades verifies task_cancel on a subagent kills the
// bash_bg jobs its child started and closes the child session.
func TestCancelSubagentCascades(t *testing.T) {
	g := newGatedLLM("unused")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "cascade test", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	<-g.Started
	rt.jobs.mu.Lock()
	child := rt.jobs.jobs[id].child
	rt.jobs.mu.Unlock()
	jobID, err := rt.jobs.startCommand(child, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitFor(t, "cascaded job finished", func() bool {
		rt.jobs.mu.Lock()
		defer rt.jobs.mu.Unlock()
		return rt.jobs.jobs[jobID].finished
	})
	waitFor(t, "child closed after cascade", func() bool {
		_, closed, _, _, _ := jobSnapshot(rt.jobs, id)
		return closed
	})
	_, _, _, followUps, _ := jobSnapshot(rt.jobs, id)
	if followUps != 0 {
		t.Fatalf("cancelled subagent ran %d follow-up turns, want 0", followUps)
	}
}

func TestOrphanJobDegradesToRoot(t *testing.T) {
	g := newGatedLLM("main answer")
	rt := newTestRuntime(t, func() chat.Config {
		return chat.Config{LLM: g, ModeLabel: "code"}
	})
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("parent: %v", err)
	}
	id, err := rt.jobs.startSubagent(parent, "", "do the thing", "degrade test", subagentOpts{})
	if err != nil {
		t.Fatalf("startSubagent: %v", err)
	}
	<-g.Started
	rt.jobs.mu.Lock()
	child := rt.jobs.jobs[id].child
	rt.jobs.jobs[id].noFollowUps = true
	rt.jobs.mu.Unlock()
	if _, err := rt.jobs.startCommand(child, "sleep", t.TempDir(), []string{"sleep", "0.3"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	close(g.Release)
	waitForWake(t, rt, parent)

	waitFor(t, "child closed", func() bool {
		_, closed, _, _, _ := jobSnapshot(rt.jobs, id)
		return closed
	})
	_, _, _, followUps, _ := jobSnapshot(rt.jobs, id)
	if followUps != 0 {
		t.Fatalf("poisoned subagent ran %d follow-up turns, want 0", followUps)
	}

	for range parent.Send(context.Background(), "and?") {
	}
	calls := g.callMsgs()
	final := calls[len(calls)-1]
	var all strings.Builder
	for _, m := range final {
		all.WriteString(m.Content + "\n")
	}
	if !strings.Contains(all.String(), "started by subagent "+id) {
		t.Fatalf("root turn context missing the orphan bg notice:\n%s", all.String())
	}
}
