package llm

import "testing"

func TestValidateToolOrder(t *testing.T) {
	call := Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "a"}, {ID: "b"}}}
	a, b := Message{Role: RoleTool, ToolCallID: "a"}, Message{Role: RoleTool, ToolCallID: "b"}
	for _, tc := range []struct {
		name     string
		messages []Message
		valid    bool
	}{
		{"complete", []Message{call, a, b, {Role: RoleAssistant, Content: "done"}}, true},
		{"reordered results", []Message{call, b, a}, true},
		{"missing result", []Message{call, a}, false},
		{"duplicate result", []Message{call, a, a, b}, false},
		{"orphan result", []Message{a}, false},
		{"interrupted exchange", []Message{call, {Role: RoleUser, Content: "next"}, a, b}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateToolOrder(tc.messages); (err == nil) != tc.valid {
				t.Fatalf("validation=%v, valid=%t", err, tc.valid)
			}
		})
	}
}
