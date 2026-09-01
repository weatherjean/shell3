package chat

import (
	"context"

	"github.com/weatherjean/shell3/internal/llm"
)

// Status is a session's terminal state, written to the session_end event.
type Status string

const (
	StatusOK    Status = "ok"
	StatusError Status = "error"
)

// End emits a session_end event with the given status.
func (s *Session) End(status Status) {
	emitSessionEnd(s, string(status))
}

// Messages returns a snapshot of the in-progress conversation history. The
// returned slice is a copy safe to retain and mutate — internal mutations
// don't affect it, and mutating it (e.g. Prune editing message Content in
// place) doesn't touch session state. Safe to call concurrently with a running
// turn: msgMu guards the read against the turn goroutine's append.
func (s *Session) Messages() []llm.Message {
	s.msgMu.RLock()
	defer s.msgMu.RUnlock()
	if s.messages == nil {
		return nil
	}
	out := make([]llm.Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// Run executes one user→assistant turn: it emits the user_message event, runs
// the turn loop, and (if cfg.Store is non-nil) persists newly appended messages
// to the store. Persistence happens inside the turn, before the terminal
// turn_done/error event fires, so a consumer reacting to that event can't
// mutate history concurrently with the save. Blocks until the turn completes.
func (s *Session) Run(ctx context.Context, cfg TurnConfig, input string) {
	emitUserMessage(s, input)
	persist := func() {
		if cfg.Store != nil && s.id != "" {
			saveHistory(cfg.Store, LogOrNoop(cfg.Log), s, s.id)
		}
	}
	RunTurn(ctx, cfg, s, llm.Message{Role: llm.RoleUser, Content: input}, persist)
}
