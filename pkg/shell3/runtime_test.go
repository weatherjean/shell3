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
)

// newTestRuntime builds a Runtime around fakellm-backed configs, bypassing
// agentsetup the same way newTestSession does for single sessions.
func newTestRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
			cfg := mk()
			cfg.Headless = o.Headless
			if o.WorkDir != "" {
				cfg.WorkDir = o.WorkDir
			}
			return cfg, nil
		},
		cleanup:  func() {},
		sessions: map[string]*Session{},
	}
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

// TestRuntime_SessionsAreIndependent pins the core phase-1 behavior: two named
// sessions on one runtime hold separate histories and separate busy gates.
func TestRuntime_SessionsAreIndependent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("hi"))
	a, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := rt.Session(SessionOpts{Name: "web:1"})
	if err != nil {
		t.Fatal(err)
	}

	for range a.Send(context.Background(), "first") {
	}
	if len(a.History()) == 0 {
		t.Fatal("session a has no history after a turn")
	}
	if len(b.History()) != 0 {
		t.Fatalf("session b inherited a's history: %v", b.History())
	}
}

// TestRuntime_SessionNameReuseAndClose: same name returns the same session;
// closing a session removes it from the runtime without tearing it down.
func TestRuntime_SessionNameReuseAndClose(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	a, _ := rt.Session(SessionOpts{Name: "main"})
	again, _ := rt.Session(SessionOpts{Name: "main"})
	if a != again {
		t.Fatal("same name must return the same live session")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	fresh, _ := rt.Session(SessionOpts{Name: "main"})
	if fresh == a {
		t.Fatal("closed session must not be returned again")
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

// TestRuntime_GeneratedNamesSkipTakenNames: when a manually-named session
// occupies "s1", two auto-named sessions must each get a fresh distinct name.
func TestRuntime_GeneratedNamesSkipTakenNames(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	manual, err := rt.Session(SessionOpts{Name: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	auto1, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	auto2, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if auto1 == manual || auto2 == manual {
		t.Fatal("auto-generated session must not collide with manually-named session")
	}
	if auto1 == auto2 {
		t.Fatal("two auto-generated sessions must be distinct")
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
	a, _ := rt.Session(SessionOpts{Name: "a"})
	b, _ := rt.Session(SessionOpts{Name: "b"})

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
	_, _ = rt.Session(SessionOpts{Name: "a"})
	_, _ = rt.Session(SessionOpts{Name: "b"})
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
	a, _ := rt.Session(SessionOpts{Name: "a", WorkDir: dirA})
	b, _ := rt.Session(SessionOpts{Name: "b", WorkDir: dirB})

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
