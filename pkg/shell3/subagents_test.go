package shell3

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// subOptsRecorder captures the SessionOpts each session was built with, keyed
// by Name, so depth-limit and workdir wiring can be asserted.
type subOptsRecorder struct {
	mu   sync.Mutex
	opts map[string]SessionOpts
}

func (r *subOptsRecorder) record(o SessionOpts) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.opts[o.Name] = o
}

func (r *subOptsRecorder) get(name string) (SessionOpts, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.opts[name]
	return o, ok
}

// newSubagentTestRuntime builds a Runtime whose parent session's model calls
// spawn_agent once then ends, and whose sub sessions just return assistant text.
func newSubagentTestRuntime(t *testing.T, rec *subOptsRecorder) *Runtime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		events:   make(chan HostEvent, 64),
		workDir:  t.TempDir(),
		ctx:      ctx,
		cancel:   cancel,
		cleanup:  func() {},
		sessions: map[string]*Session{},
	}
	rt.sessionConfig = func(o SessionOpts) (chat.Config, error) {
		rec.record(o)
		var cfg chat.Config
		if strings.HasPrefix(o.Name, "sub:") {
			cfg = chat.Config{
				LLM:       fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "subagent result here"}}}),
				ModeLabel: "code",
			}
		} else {
			cfg = chat.Config{
				LLM: fakellm.New(
					fakellm.Script{Events: []llm.StreamEvent{
						{ToolCall: &llm.ToolCall{ID: "1", Name: "spawn_agent", RawArgs: `{"task":"do the thing"}`}},
					}},
					fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "parent done"}}},
				),
				ModeLabel: "code",
				Personality: persona.Persona{Name: "code", Tools: []llm.ToolDefinition{{
					Name: "spawn_agent", Parameters: map[string]any{"type": "object"},
				}}},
			}
		}
		cfg.Headless = o.Headless
		if o.WorkDir != "" {
			cfg.WorkDir = o.WorkDir
		}
		return cfg, nil
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// TestSubagent_SpawnWakesIdleParent: spawning runs the sub on a goroutine; when
// it finishes it posts its result to the parent inbox and Wakes the idle parent.
func TestSubagent_SpawnWakesIdleParent(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
	rt := newSubagentTestRuntime(t, rec)
	parent, err := rt.Session(SessionOpts{Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	for range parent.Send(context.Background(), "go") {
	}

	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake {
			t.Fatalf("got event kind %v, want Wake", ev.Kind)
		}
		if ev.Session != "parent" {
			t.Fatalf("wake for %q, want parent", ev.Session)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no Wake event for parent within timeout")
	}

	// The subagent result was Interjected into the parent inbox before the Wake;
	// the next turn drains it as a system-reminder injected into the request.
	found := false
	for ev := range parent.Send(context.Background(), "next") {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "subagent result here") {
			found = true
		}
	}
	if !found {
		t.Fatal("subagent result not delivered to parent inbox")
	}
}

// TestSubagent_DepthLimit: the spawned sub is created with DisableSubagents.
func TestSubagent_DepthLimit(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
	rt := newSubagentTestRuntime(t, rec)
	parent, _ := rt.Session(SessionOpts{Name: "parent"})
	for range parent.Send(context.Background(), "go") {
	}
	// Wait for spawn to have created the sub session.
	deadline := time.After(2 * time.Second)
	for {
		if o, ok := rec.get("sub:a1"); ok {
			if !o.DisableSubagents {
				t.Fatal("sub session must be created with DisableSubagents=true")
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("sub session never created")
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
}

// TestSubagent_ListAgentsSnapshot: snapshot shows running then finished.
func TestSubagent_ListAgentsSnapshot(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
	rt := newSubagentTestRuntime(t, rec)
	parent, _ := rt.Session(SessionOpts{Name: "parent"})
	for range parent.Send(context.Background(), "go") {
	}
	// Wait for the Wake (sub finished).
	select {
	case <-rt.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("no Wake")
	}
	snap := parent.subs.snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 subagent, got %d", len(snap))
	}
	if snap[0].Status != "finished" {
		t.Fatalf("status %q, want finished", snap[0].Status)
	}
	if snap[0].Result == "" {
		t.Fatal("finished subagent has empty result")
	}
}
