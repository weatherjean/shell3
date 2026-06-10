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

// RunParts executes one user→assistant turn whose user message carries media
// parts alongside the prompt text. With parts the message gets
// ContentParts = [text(input), parts...] — the text part is omitted when
// input is empty, since some providers reject empty text content parts.
// Content stays set to input for
// history rows and the user_message audit event (the openai adapter sends
// ContentParts and ignores Content when both are present). Emits the
// user_message event, runs the turn loop, and (if cfg.Store is non-nil)
// persists newly appended messages to the store. Persistence happens inside
// the turn, before the terminal turn_done/error event fires, so a consumer
// reacting to that event (e.g. /clear, /rollback) can't mutate history
// concurrently with the save. Blocks until the turn completes.
func (s *Session) RunParts(ctx context.Context, cfg TurnConfig, input string, parts []llm.ContentPart) {
	emitUserMessage(s, input)
	from := len(s.messages)
	persist := func() {
		if cfg.Store != nil {
			saveHistory(cfg.Store, LogOrNoop(cfg.Log), s, s.id, from)
		}
	}
	userMsg := llm.Message{Role: llm.RoleUser, Content: input}
	if len(parts) > 0 {
		cps := make([]llm.ContentPart, 0, len(parts)+1)
		if input != "" { // some providers reject empty text parts
			cps = append(cps, llm.ContentPart{Type: llm.ContentPartTypeText, Text: input})
		}
		cps = append(cps, parts...)
		userMsg.ContentParts = cps
	}
	RunTurn(ctx, cfg, s, userMsg, persist)
}

// Run executes one text-only user→assistant turn; see RunParts.
func (s *Session) Run(ctx context.Context, cfg TurnConfig, input string) {
	s.RunParts(ctx, cfg, input, nil)
}
