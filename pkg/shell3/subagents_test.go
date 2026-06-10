package shell3

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

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

// TestSubagent_TwoParentsDistinctSubNames: two parent sessions each spawn a
// subagent; the sub session names must be globally unique (sub:a1, sub:a2),
// never colliding on a per-parent "a1" that would cross-wire the two parents
// onto one child session.
func TestSubagent_TwoParentsDistinctSubNames(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
	rt := newSubagentTestRuntime(t, rec)

	p1, err := rt.Session(SessionOpts{Name: "parent1"})
	if err != nil {
		t.Fatal(err)
	}
	p2, err := rt.Session(SessionOpts{Name: "parent2"})
	if err != nil {
		t.Fatal(err)
	}
	for range p1.Send(context.Background(), "go") {
	}
	for range p2.Send(context.Background(), "go") {
	}
	// Both parents waked after their sub finished; drain whatever events arrive.
	for i := 0; i < 2; i++ {
		select {
		case <-rt.Events():
		case <-time.After(2 * time.Second):
			t.Fatal("missing Wake event")
		}
	}

	// Collect the sub session names that were created. With a global id counter
	// they must be distinct.
	rec.mu.Lock()
	var subNames []string
	for name := range rec.opts {
		if strings.HasPrefix(name, "sub:") {
			subNames = append(subNames, name)
		}
	}
	rec.mu.Unlock()
	if len(subNames) != 2 {
		t.Fatalf("want 2 distinct sub sessions, got %d: %v", len(subNames), subNames)
	}
	if subNames[0] == subNames[1] {
		t.Fatalf("sub session names collided: both %q", subNames[0])
	}
}

// TestSubagent_FailedSpawnLeavesNoDanglingEntry: a spawn whose child creation
// fails (runtime closed before spawn) must add nothing to the registry — no
// phantom forever-"running" entry.
func TestSubagent_FailedSpawnLeavesNoDanglingEntry(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
	rt := newSubagentTestRuntime(t, rec)
	parent, err := rt.Session(SessionOpts{Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	// Close the runtime so rt.Session fails inside spawn.
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	id, err := parent.spawn(context.Background(), chat.SpawnRequest{Task: "do it"})
	if err == nil {
		t.Fatalf("spawn on closed runtime should fail; got id %q", id)
	}
	if snap := parent.subs.snapshot(); len(snap) != 0 {
		t.Fatalf("failed spawn left %d registry entries, want 0: %+v", len(snap), snap)
	}
}

// delayedFinishClient emits one assistant token so the turn produces a real
// streamed result, then blocks until its turn ctx is cancelled (by
// Runtime.Close → rt.cancel), and finally waits a bounded delay BEFORE
// returning. The subagent goroutine only runs finish() (which flips registry
// status to "finished") after Stream returns, so this post-cancel delay forces
// finish() to land strictly after the moment Close returns if Close did NOT
// join the goroutine. With rt.wg.Wait() present Close blocks for the delay and
// the status is "finished"; without it Close returns in microseconds and the
// status is still "running" — making the guard deterministic at -count=1. The
// timer (select-free, but bounded) keeps the test fast and hang-free: the delay
// only begins after ctx is already cancelled.
type delayedFinishClient struct {
	delay time.Duration
}

func (c *delayedFinishClient) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamEvent)) error {
	// A non-empty streamed token makes the turn behave like a normal completion
	// (an empty turn short-circuits finish() too early to be a useful guard).
	emit(llm.StreamEvent{TextDelta: "subagent result here"})
	<-ctx.Done()
	// Bounded post-cancel delay: pushes finish() past Close's return point.
	timer := time.NewTimer(c.delay)
	defer timer.Stop()
	<-timer.C
	return ctx.Err()
}

// TestSubagent_CloseJoinsGoroutine: Close must join in-flight subagent
// goroutines. The sub model blocks until its turn ctx is cancelled, then delays
// before returning; the registry only flips to "finished" once the goroutine's
// finish() runs after Stream returns. After rt.Close() returns the status must
// already be "finished" (no sleep) — which can only hold if Close waited on the
// goroutine via rt.wg.Wait().
func TestSubagent_CloseJoinsGoroutine(t *testing.T) {
	rec := &subOptsRecorder{opts: map[string]SessionOpts{}}
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
			// Blocks until its turn ctx is cancelled (by Runtime.Close →
			// child.Close), then delays ~50ms before returning so the goroutine's
			// finish() lands after Close's return point unless Close joins it.
			cfg = chat.Config{
				LLM:       &delayedFinishClient{delay: 50 * time.Millisecond},
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

	parent, err := rt.Session(SessionOpts{Name: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	// Wrap the subagent goroutine's tail via a sentinel: we observe completion
	// by patching deliverSubagentResult through a hook is intrusive, so instead
	// we detect the goroutine finished by the registry flipping to "finished"
	// AND the join. Use a dedicated completion channel set by a wrapper spawn.
	for range parent.Send(context.Background(), "go") {
	}
	// Spawn launched a blocked subagent goroutine. Wait until the sub session
	// exists so the goroutine is actually in-flight before Close.
	deadline := time.After(2 * time.Second)
	for {
		if _, ok := rec.get("sub:a1"); ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("sub session never created")
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
	// We can't inject into the production goroutine, so assert via registry
	// status, which finish() sets right before deliver. After Close returns,
	// the goroutine has been joined, so the sub must already be "finished".
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	// No sleep: if Close joined the goroutine, the subagent has run finish().
	snap := parent.subs.snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 subagent, got %d", len(snap))
	}
	if snap[0].Status != "finished" {
		t.Fatalf("after Close, sub status %q, want finished (Close did not join the goroutine)", snap[0].Status)
	}
}

// TestSubagent_PreviewRuneSafe: a result longer than 200 bytes of multibyte
// runes truncates on a rune boundary, so the snapshot Result stays valid UTF-8.
func TestSubagent_PreviewRuneSafe(t *testing.T) {
	var r subRegistry
	sa := r.add("a1", "code", "task")
	// 100 copies of "日" (3 bytes each) = 300 bytes, well over 200.
	r.finish(sa, strings.Repeat("日", 100))
	snap := r.snapshot()
	if len(snap) != 1 {
		t.Fatalf("want 1 entry, got %d", len(snap))
	}
	if !utf8.ValidString(snap[0].Result) {
		t.Fatalf("truncated preview is not valid UTF-8: %q", snap[0].Result)
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
