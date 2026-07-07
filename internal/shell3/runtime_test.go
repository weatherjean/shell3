package shell3

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// newTestRuntime builds a Runtime around fakellm-backed configs, bypassing
// agentsetup the same way newTestSession does for single sessions. It opens a
// real runs.Store in a temp dir so sessions (including in-process subagents)
// can persist messages, and initialises rt.jobs for background-job tests.
func newTestRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	store, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("newTestRuntime: runs.Open: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			cfg := mk()
			cfg.Headless = o.Headless
			if o.WorkDir != "" {
				cfg.WorkDir = o.WorkDir
			}
			// Only inject the runtime's store when the config doesn't already
			// carry an explicit one (tests using fakeCfgWithStore bring their own).
			if cfg.Store == nil {
				cfg.Store = store
			}
			return cfg, nil
		},
		events:    make(chan HostEvent, 64),
		jobEvents: make(chan JobProgress, 256),
		workDir:   t.TempDir(),
		store:     store,
		ctx:       ctx,
		cancel:    cancel,
		cleanup:   func() {},
		sessions:  map[string]*Session{},
	}
	rt.jobs = newJobManager(rt, 0)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func fakeCfg(text string) func() chat.Config {
	return func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: text}}},
			),
			ModeLabel: "code",
		}
	}
}

// TestRuntime_SessionsAreIndependent pins the core phase-1 behavior: two
// sessions on one runtime hold separate histories and separate busy gates.
func TestRuntime_SessionsAreIndependent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	a, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}

	for range a.Send(context.Background(), "first") {
	}
	if a.MessageCount() == 0 {
		t.Fatal("session a has no history after a turn")
	}
	if b.MessageCount() != 0 {
		t.Fatalf("session b inherited a's history: %d messages", b.MessageCount())
	}
}

// TestRuntime_SessionAfterCloseErrors: Session on a closed runtime returns an error.
func TestRuntime_SessionAfterCloseErrors(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Session(SessionOpts{})
	if err == nil {
		t.Fatal("expected error from Session on closed runtime, got nil")
	}
}

// TestRuntime_EachSessionCallIsDistinct: every Runtime.Session call creates a
// fresh, independent session (no dedup/reuse-by-name lookup).
func TestRuntime_EachSessionCallIsDistinct(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	a, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two Session calls must return distinct sessions")
	}
}

// TestRuntime_AgentSwitchIsPerSession: switching agents in one session must
// not affect a sibling — the spec's core multi-session invariant.
func TestRuntime_AgentSwitchIsPerSession(t *testing.T) {
	mk := func() chat.Config {
		cfg := chat.Config{LLM: fakellm.New(), ModeLabel: "code", AgentNames: []string{"code", "plan"}}
		cfg.SwitchAgent = func(name string) (chat.ActiveAgent, error) {
			return chat.ActiveAgent{ModeLabel: name, LLM: fakellm.New()}, nil
		}
		return cfg
	}
	rt := newTestRuntime(t, mk)
	a, _ := rt.Session(SessionOpts{})
	b, _ := rt.Session(SessionOpts{})

	if err := b.SwitchAgent("plan"); err != nil {
		t.Fatal(err)
	}
	if got := a.ActiveAgent(); got != "code" {
		t.Fatalf("session a's agent changed to %q when b switched", got)
	}
	if got := b.ActiveAgent(); got != "plan" {
		t.Fatalf("session b should be plan, got %q", got)
	}
}

// TestRuntime_CloseClosesSessions: Runtime.Close closes remaining sessions
// then runs the shared cleanup exactly once.
func TestRuntime_CloseClosesSessions(t *testing.T) {
	cleanups := 0
	rt := newTestRuntime(t, fakeCfg("x"))
	rt.cleanup = func() { cleanups++ }
	_, _ = rt.Session(SessionOpts{})
	_, _ = rt.Session(SessionOpts{})
	if err := rt.Close(); err != nil {
		t.Fatal(err)
	}
	if cleanups != 1 {
		t.Fatalf("shared cleanup ran %d times, want 1", cleanups)
	}
	if err := rt.Close(); err != nil {
		t.Fatal("second Close must be a no-op, not an error")
	}
	if cleanups != 1 {
		t.Fatalf("second Close re-ran cleanup (%d)", cleanups)
	}
}

// TestRuntime_PerSessionWorkdir: a bash tool call runs in the session's own
// workdir, not the runtime root — the substrate for repo-rooted subagents.
func TestRuntime_PerSessionWorkdir(t *testing.T) {
	dirA, dirB := t.TempDir(), t.TempDir()
	mk := func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{
					{ToolCall: &llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"pwd"}`}},
				}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
			),
			ModeLabel: "code",
			Personality: persona.Persona{Tools: []llm.ToolDefinition{{
				Name: "bash", Parameters: map[string]any{"type": "object"},
			}}},
		}
	}
	rt := newTestRuntime(t, mk)
	a, _ := rt.Session(SessionOpts{WorkDir: dirA})
	b, _ := rt.Session(SessionOpts{WorkDir: dirB})

	got := map[*Session]string{}
	for _, s := range []*Session{a, b} {
		for ev := range s.Send(context.Background(), "where am I?") {
			if ev.Kind == ToolResult && ev.ToolName == "bash" {
				got[s] = strings.TrimSpace(ev.ToolOutput)
			}
		}
	}
	// macOS tempdirs may resolve through /private; compare with EvalSymlinks.
	wantA, _ := filepath.EvalSymlinks(dirA)
	wantB, _ := filepath.EvalSymlinks(dirB)
	gotA, _ := filepath.EvalSymlinks(got[a])
	gotB, _ := filepath.EvalSymlinks(got[b])
	if gotA != wantA || gotB != wantB {
		t.Fatalf("bash cwd: a=%q (want %q) b=%q (want %q)", gotA, wantA, gotB, wantB)
	}
}
