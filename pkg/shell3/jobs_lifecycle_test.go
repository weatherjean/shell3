package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// subagentCfg returns a config whose active agent may spawn the "explorer"
// subagent, backed by the given LLM client.
func subagentCfg(client chat.LLMClient) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        client,
			ModeLabel:  "code",
			AgentKnobs: chat.AgentKnobs{Subagents: []string{"explorer"}},
		}
	}
}

// TestStartSubagent_DepthLimit pins the containment guard: a session already
// at the configured max depth may not spawn deeper.
func TestStartSubagent_DepthLimit(t *testing.T) {
	rt := newTestRuntime(t, subagentCfg(fakellm.New()))
	rt.subagentMaxDepthVal = 2

	parent, err := rt.Session(SessionOpts{Name: "deep", Depth: 2})
	if err != nil {
		t.Fatal(err)
	}
	tc := parent.turnConfig()
	if _, err := tc.StartSubagent("explorer", "p", "d"); err == nil ||
		!strings.Contains(err.Error(), "max subagent depth 2") {
		t.Fatalf("spawn at max depth: want depth-limit error, got %v", err)
	}
	// No job may have been created by the refused spawn.
	if jobs := rt.jobs.list(); len(jobs) != 0 {
		t.Fatalf("refused spawn still created a job: %+v", jobs)
	}
}

// TestStartSubagent_ConcurrencyCap pins the subagent slot reservation: with
// max_concurrent=1 and one subagent running, a second spawn is refused.
func TestStartSubagent_ConcurrencyCap(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, subagentCfg(block))
	rt.jobs = newJobManager(rt, 1)

	parent, err := rt.Session(SessionOpts{Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startSubagent(parent, "explorer", "task", "desc", 1)
	if err != nil {
		t.Fatalf("first startSubagent: %v", err)
	}
	<-block.Started // the child turn is verifiably in flight

	if _, err := rt.jobs.startSubagent(parent, "explorer", "task2", "desc2", 1); err == nil ||
		!strings.Contains(err.Error(), "cap 1 reached") {
		t.Fatalf("second spawn at cap: want cap error, got %v", err)
	}
	_ = rt.jobs.cancel(id)
}

// TestSubagentCancelMidRun pins that cancelling a running subagent unwinds
// the child turn and reports the job as failed (not a clean done).
func TestSubagentCancelMidRun(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, subagentCfg(block))

	parent, err := rt.Session(SessionOpts{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startSubagent(parent, "explorer", "task", "desc", 1)
	if err != nil {
		t.Fatal(err)
	}
	<-block.Started

	if err := rt.jobs.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	// finishSubagent wakes the parent when the job goroutine unwinds.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-rt.Events():
			if ev.Kind != Wake || ev.Session != "p" {
				continue
			}
			var found JobInfo
			for _, j := range rt.jobs.list() {
				if j.ID == id {
					found = j
				}
			}
			if !found.Done {
				t.Fatalf("cancelled subagent not marked done: %+v", found)
			}
			if found.Error == "" {
				t.Fatal("cancelled subagent reported a clean done; want an error")
			}
			return
		case <-deadline:
			t.Fatal("timeout waiting for the cancelled subagent to finish")
		}
	}
}

// TestRuntimeClose_JoinsLiveCommandJob pins Close ordering: with a command
// job still running, Runtime.Close must cancel it, join its goroutine, and
// return — no hang, no write-after-close on the store. Run under -race.
func TestRuntimeClose_JoinsLiveCommandJob(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if _, err := rt.jobs.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "30"}, nil); err != nil {
		t.Fatalf("startCommand: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- rt.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Runtime.Close hung with a live command job")
	}
}
