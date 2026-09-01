package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/notify"
)

func subagentCfg(client chat.LLMClient) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM:        client,
			Agent:      "code",
			AgentKnobs: chat.AgentKnobs{Subagents: []string{"explorer"}},
		}
	}
}

func TestStartSubagent_ConcurrencyCap(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, subagentCfg(block))
	rt.jobs = newJobManager(rt, 1)

	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startSubagent(parent, "explorer", "task", "desc", subagentOpts{})
	if err != nil {
		t.Fatalf("first startSubagent: %v", err)
	}
	<-block.Started

	if _, err := rt.jobs.startSubagent(parent, "explorer", "task2", "desc2", subagentOpts{}); err == nil ||
		!strings.Contains(err.Error(), "cap 1 reached") {
		t.Fatalf("second spawn at cap: want cap error, got %v", err)
	}
	_ = rt.jobs.cancel(id, false)
}

func TestSubagentCancelMidRun(t *testing.T) {
	block := fakellm.NewBlocking()
	rt := newTestRuntime(t, subagentCfg(block))

	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	id, err := rt.jobs.startSubagent(parent, "explorer", "task", "desc", subagentOpts{})
	if err != nil {
		t.Fatal(err)
	}
	<-block.Started

	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	waitForWake(t, rt, parent)
	var found JobInfo
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, j := range rt.jobs.list() {
			if j.ID == id {
				found = j
			}
		}
		if found.Done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found.Done {
		t.Fatalf("cancelled subagent not marked done: %+v", found)
	}
	if found.Error == "" {
		t.Fatal("cancelled subagent reported a clean done; want an error")
	}
}

func TestRuntimeClose_JoinsLiveCommandJob(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if _, err := rt.jobs.startCommand(nil, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, ""); err != nil {
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
