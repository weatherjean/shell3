// Package shell3test provides test-only helpers for exercising pkg/shell3 from
// other packages. It keeps the `testing` and fakellm dependencies out of the
// production shell3 library: the helpers here import them, shell3 itself does not.
package shell3test

import (
	"context"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/shell3"
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

// newRuntime builds the runtime via shell3.RuntimeForTest and registers cleanup.
func newRuntime(t *testing.T, sessionConfig func(shell3.SessionOpts) (chat.Config, error)) *shell3.Runtime {
	t.Helper()
	rt := shell3.RuntimeForTest(t.TempDir(), sessionConfig)
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

// BlockingLLM is a chat.LLMClient whose Stream blocks until ctx is cancelled,
// closing Started on its first call. For tests that need an in-flight turn.
type BlockingLLM struct {
	Started chan struct{}
	once    sync.Once
}

// NewBlockingLLM returns a BlockingLLM with an open Started channel.
func NewBlockingLLM() *BlockingLLM {
	return &BlockingLLM{Started: make(chan struct{})}
}

// Stream signals Started (once) then blocks until ctx is done.
func (b *BlockingLLM) Stream(ctx context.Context, _ []llm.Message, _ []llm.ToolDefinition, _ func(llm.StreamEvent)) error {
	b.once.Do(func() { close(b.Started) })
	<-ctx.Done()
	return ctx.Err()
}
