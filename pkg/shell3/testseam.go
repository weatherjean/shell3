package shell3

import (
	"context"
	"sync"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// NewRuntimeForTest builds a Runtime whose model always streams replyText.
// For tests in other packages only.
func NewRuntimeForTest(t *testing.T, replyText string) *Runtime {
	t.Helper()
	return newRuntimeForTest(t, func(o SessionOpts) (chat.Config, error) {
		cfg := chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
				fakellm.Script{Events: []llm.StreamEvent{{TextDelta: replyText}}},
			),
			ModeLabel:  "code",
			AgentNames: []string{"code"},
		}
		cfg.Headless = o.Headless
		return cfg, nil
	})
}

// NewRuntimeForTestClient builds a Runtime backed by the given LLMClient.
// For tests in other packages only.
func NewRuntimeForTestClient(t *testing.T, client chat.LLMClient) *Runtime {
	t.Helper()
	return newRuntimeForTest(t, func(o SessionOpts) (chat.Config, error) {
		cfg := chat.Config{
			LLM:        client,
			ModeLabel:  "code",
			AgentNames: []string{"code"},
		}
		cfg.Headless = o.Headless
		return cfg, nil
	})
}

// newRuntimeForTest is the shared construction for the test runtime builders.
func newRuntimeForTest(t *testing.T, sessionConfig func(SessionOpts) (chat.Config, error)) *Runtime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		sessionConfig: sessionConfig,
		events:        make(chan HostEvent, 64),
		workDir:       t.TempDir(),
		ctx:           ctx,
		cancel:        cancel,
		cleanup:       func() {},
		sessions:      map[string]*Session{},
	}
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
