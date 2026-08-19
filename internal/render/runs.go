package render

import (
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// firstPrompt is the run's opening user message — what the run was asked to do,
// which identifies it in a listing better than its latest line does.
func firstPrompt(msgs []llm.Message) string {
	for _, m := range msgs {
		if m.Role == llm.RoleUser && strings.TrimSpace(m.Content) != "" {
			return m.Content
		}
	}
	return ""
}

// stripToolIDPrefix drops the "[tool_call_id=…]\n" line the turn loop prepends
// to a stored tool result.
func stripToolIDPrefix(content string) string {
	if strings.HasPrefix(content, "[tool_call_id=") {
		if nl := strings.IndexByte(content, '\n'); nl >= 0 {
			return content[nl+1:]
		}
	}
	return content
}
