package llm

import "fmt"

// ValidateToolOrder rejects histories that would replay an incomplete or
// unmatched tool exchange to a provider.
func ValidateToolOrder(messages []Message) error {
	pending := map[string]bool{}
	for i, message := range messages {
		if message.Role == RoleTool {
			if !pending[message.ToolCallID] {
				return fmt.Errorf("message %d: unmatched tool result %q", i, message.ToolCallID)
			}
			delete(pending, message.ToolCallID)
			continue
		}
		if len(pending) != 0 {
			return fmt.Errorf("message %d: missing tool results", i)
		}
		for _, call := range message.ToolCalls {
			if message.Role != RoleAssistant || call.ID == "" || pending[call.ID] {
				return fmt.Errorf("message %d: invalid tool call %q", i, call.ID)
			}
			pending[call.ID] = true
		}
	}
	if len(pending) != 0 {
		return fmt.Errorf("history ends with missing tool results")
	}
	return nil
}
