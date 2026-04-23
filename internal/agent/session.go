// Package agent implements the core LLM conversation loop.
package agent

import "github.com/weatherjean/shell3/internal/llm"

// Session holds the in-progress conversation message history.
type Session struct {
	Messages []llm.Message
}

// Append adds m to the session message list.
func (s *Session) Append(m llm.Message) {
	s.Messages = append(s.Messages, m)
}
