package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

// NewRuntimeForTest builds a Runtime whose model always streams replyText.
// For tests in other packages only.
func NewRuntimeForTest(t *testing.T, replyText string) *Runtime {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	rt := &Runtime{
		sessionConfig: func(o SessionOpts) (chat.Config, error) {
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
				ModeLabel: "code",
			}
			cfg.Headless = o.Headless
			return cfg, nil
		},
		events:   make(chan HostEvent, 64),
		workDir:  t.TempDir(),
		ctx:      ctx,
		cancel:   cancel,
		cleanup:  func() {},
		sessions: map[string]*Session{},
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}
