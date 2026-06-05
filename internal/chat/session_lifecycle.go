package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/llm"
)

// Start emits a session_start event with the given metadata. Call this once
// per session, after Store.StartSession() if a store is used.
func (s *Session) Start(meta map[string]string) {
	emitSessionStart(s, meta)
}

// End emits a session_end event with the given status string ("ok" or "error").
func (s *Session) End(status string) {
	emitSessionEnd(s, status)
}

// Messages returns a snapshot of the in-progress conversation history. The
// returned slice is safe to retain — internal mutations don't affect it.
func (s *Session) Messages() []llm.Message {
	if s.messages == nil {
		return nil
	}
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// SetMessages replaces the conversation history. Used by slash commands like
// /clear and /rollback that mutate session state from outside the package.
func (s *Session) SetMessages(msgs []llm.Message) {
	s.messages = msgs
}

// Run executes one user→assistant turn. Emits the user_message event, runs
// the turn loop, and (if cfg.Store is non-nil) persists newly appended
// messages to the store. Persistence happens inside the turn, before the
// terminal turn_done/error event fires, so a consumer reacting to that event
// (e.g. /clear, /rollback) can't mutate history concurrently with the save.
// Blocks until the turn completes.
func (s *Session) Run(ctx context.Context, cfg TurnConfig, input string) {
	emitUserMessage(s, input)
	from := len(s.messages)
	persist := func() {
		if cfg.Store != nil {
			saveHistory(cfg.Store, LogOrNoop(cfg.Log), s, s.id, from)
		}
	}
	RunTurn(ctx, cfg, s, llm.Message{Role: llm.RoleUser, Content: input}, persist)
}
