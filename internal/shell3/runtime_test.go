package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
)

// newTestRuntime uses production lifecycle wiring and owns its store.
func newTestRuntime(t *testing.T, mk func() chat.Config) *Runtime {
	t.Helper()
	store, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("newTestRuntime: runs.Open: %v", err)
	}
	rt, err := NewConfiguredRuntime(context.Background(), t.TempDir(), store, 0, func() { _ = store.Close() }, func(o SessionOpts) (chat.Config, error) {
		cfg := mk()
		cfg.Headless = o.Headless
		if cfg.Store == nil {
			cfg.Store = store
		}
		return cfg, nil
	})
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
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
		}
	}
}

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
	msgsA, err := rt.store.LoadMessages(a.ID())
	if err != nil || len(msgsA) == 0 {
		t.Fatal("session a has no history after a turn")
	}
	msgsB, err := rt.store.LoadMessages(b.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got := len(msgsB); got != 0 {
		t.Fatalf("session b inherited a's history: %d messages", got)
	}
}

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
