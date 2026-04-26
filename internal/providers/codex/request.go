package codex

import (
	"github.com/weatherjean/shell3/internal/llm"
)

// responsesRequest is the JSON body shape posted to the Codex Responses API.
type responsesRequest struct {
	Model        string          `json:"model"`
	Instructions string          `json:"instructions,omitempty"`
	Input        []any           `json:"input"`
	Tools        []responsesTool `json:"tools,omitempty"`
	Stream       bool            `json:"stream"`
	Store        bool            `json:"store"`
}

// responsesTool is the function-tool descriptor accepted by the Responses API.
type responsesTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// inputMessage is a regular conversation turn in the Responses API input array.
type inputMessage struct {
	Type    string        `json:"type"`
	Role    string        `json:"role"`
	Content []messagePart `json:"content"`
}

// messagePart is a single content fragment inside an input/output message.
// Text-only is enough for v1; image / file parts can be added later.
type messagePart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// inputFunctionCall represents an assistant-issued tool invocation that the
// caller has already executed (and is supplying back as part of history).
type inputFunctionCall struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// inputFunctionCallOutput pairs a tool result with its originating call_id.
type inputFunctionCallOutput struct {
	Type   string `json:"type"`
	CallID string `json:"call_id"`
	Output string `json:"output"`
}

// buildRequest converts shell3's internal message + tool format into the
// Responses API request body.
//
// Mapping rules:
//   - The first system message becomes `instructions` (Responses API has a
//     dedicated field for it; sending system as a regular item works but is
//     non-canonical).
//   - User messages → input_text parts.
//   - Assistant messages with tool_calls → function_call items (one per call).
//     Plain assistant text becomes an output_text message.
//   - Tool results → function_call_output items, keyed by tool_call_id.
func buildRequest(model string, msgs []llm.Message, tools []llm.ToolDefinition) (*responsesRequest, error) {
	req := &responsesRequest{
		Model:  model,
		Stream: true,
		Store:  false,
	}

	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			// First system message becomes top-level instructions; subsequent
			// system messages are concatenated. Most chats only have one.
			if req.Instructions == "" {
				req.Instructions = m.Content
			} else {
				req.Instructions += "\n\n" + m.Content
			}
		case llm.RoleUser:
			req.Input = append(req.Input, inputMessage{
				Type:    "message",
				Role:    "user",
				Content: []messagePart{{Type: "input_text", Text: m.Content}},
			})
		case llm.RoleAssistant:
			if m.Content != "" {
				req.Input = append(req.Input, inputMessage{
					Type:    "message",
					Role:    "assistant",
					Content: []messagePart{{Type: "output_text", Text: m.Content}},
				})
			}
			for _, tc := range m.ToolCalls {
				req.Input = append(req.Input, inputFunctionCall{
					Type:      "function_call",
					CallID:    tc.ID,
					Name:      tc.Name,
					Arguments: tc.RawArgs,
				})
			}
		case llm.RoleTool:
			req.Input = append(req.Input, inputFunctionCallOutput{
				Type:   "function_call_output",
				CallID: m.ToolCallID,
				Output: m.Content,
			})
		}
	}

	for _, t := range tools {
		req.Tools = append(req.Tools, responsesTool{
			Type:        "function",
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	return req, nil
}
