package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/llm"
)

// Run executes one user-to-assistant turn. If cfg.Store is non-nil, newly
// appended messages are persisted before the terminal turn-done or error event
// fires, so a consumer reacting to that event cannot race the history save.
func (s *Session) Run(ctx context.Context, cfg TurnConfig, input string) {
	persist := func() {
		if cfg.Store != nil && s.id != "" {
			saveHistory(cfg.Store, LogOrNoop(cfg.Log), s, s.id)
		}
	}
	RunTurn(ctx, cfg, s, llm.Message{Role: llm.RoleUser, Content: input}, persist)
}
