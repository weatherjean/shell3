package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestNewSession_SeedsInitialMessages(t *testing.T) {
	seed := []llm.Message{
		{Role: llm.RoleUser, Content: "earlier"},
		{Role: llm.RoleAssistant, Content: "reply"},
	}
	s := NewSession(SessionOpts{InitialMessages: seed})
	if len(s.messages) != 2 || s.messages[0].Content != "earlier" {
		t.Fatalf("session not seeded: %#v", s.messages)
	}
}
