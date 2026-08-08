// Package shell3test provides test-only helpers for exercising internal/shell3 from
// other packages. It keeps the `testing` and fakellm dependencies out of the
// production shell3 package: the helpers here import them, shell3 itself does not.
package shell3test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// NewRuntimeForTest builds a Runtime whose model always streams replyText.
func NewRuntimeForTest(t *testing.T, replyText string) *shell3.Runtime {
	t.Helper()
	return newRuntime(t, func(o shell3.SessionOpts) (chat.Config, error) {
		scripts := make([]fakellm.Script, 8)
		for i := range scripts {
			scripts[i] = fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}}
		}
		cfg := chat.Config{LLM: fakellm.New(scripts...), ModeLabel: "code", AgentNames: []string{"code"}}
		cfg.Headless = o.Headless
		return cfg, nil
	})
}

// NewRuntimeForTestClient builds a Runtime backed by the given LLMClient.
func NewRuntimeForTestClient(t *testing.T, client chat.LLMClient) *shell3.Runtime {
	t.Helper()
	return newRuntime(t, func(o shell3.SessionOpts) (chat.Config, error) {
		cfg := chat.Config{LLM: client, ModeLabel: "code", AgentNames: []string{"code"}}
		cfg.Headless = o.Headless
		return cfg, nil
	})
}

// newRuntime builds the runtime via shell3.RuntimeForTest and registers
// cleanup. Sessions persist into a real runs store in the temp dir, so tests
// exercise the same history/runs paths the production runtime does.
func newRuntime(t *testing.T, sessionConfig func(shell3.SessionOpts) (chat.Config, error)) *shell3.Runtime {
	t.Helper()
	dir := t.TempDir()
	store, err := runs.Open(dir)
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	rt := shell3.RuntimeForTest(dir, func(o shell3.SessionOpts) (chat.Config, error) {
		cfg, err := sessionConfig(o)
		if err == nil && cfg.Store == nil {
			cfg.Store = store
		}
		return cfg, err
	})
	rt.SetStoreForTest(store)
	t.Cleanup(func() {
		_ = rt.Close()
		_ = store.Close()
	})
	return rt
}
