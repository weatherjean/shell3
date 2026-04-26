package chat

import (
	"fmt"

	"github.com/weatherjean/shell3/internal/llm"
)

// session holds the in-progress conversation history.
type session struct {
	messages []llm.Message
	// nextToolCallID drives sequential numeric ids ("1", "2", ...) that
	// replace whatever the provider emits. Models reliably echo a bare
	// integer; provider-native ids like "web_fetch:0" get truncated by
	// models and break tool_call_id-based addressing (e.g. prune_tool_result).
	nextToolCallID int
}

func (s *session) append(m llm.Message) {
	s.messages = append(s.messages, m)
}

// allocToolCallID returns the next sequential numeric tool-call id as
// a decimal string.
func (s *session) allocToolCallID() string {
	s.nextToolCallID++
	return fmt.Sprintf("%d", s.nextToolCallID)
}
