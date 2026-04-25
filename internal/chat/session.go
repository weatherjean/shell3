package chat

import "github.com/weatherjean/shell3/internal/llm"

// session holds the in-progress conversation history.
type session struct {
	messages []llm.Message
}

func (s *session) append(m llm.Message) {
	s.messages = append(s.messages, m)
}
