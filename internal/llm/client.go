package llm

import (
	"context"
	"fmt"
	"io"

	openai "github.com/sashabaranov/go-openai"
)

// Client is an OpenAI-compatible streaming LLM client.
type Client struct {
	oc    *openai.Client
	model string
}

// NewClient creates a Client targeting baseURL with the given apiKey and model.
func NewClient(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	return &Client{
		oc:    openai.NewClientWithConfig(cfg),
		model: model,
	}
}

// Stream sends msgs to the LLM and calls onEvent for each token, tool call, and completion.
func (c *Client) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error {
	req := openai.ChatCompletionRequest{
		Model:    c.model,
		Messages: toOpenAI(msgs),
		Stream:   true,
	}
	if len(tools) > 0 {
		req.Tools = toOpenAITools(tools)
	}

	stream, err := c.oc.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return fmt.Errorf("llm: stream: %w", err)
	}
	defer stream.Close()

	toolCalls := map[int]*ToolCall{}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("llm: recv: %w", err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			onEvent(StreamEvent{TextDelta: delta.Content})
		}

		for _, tc := range delta.ToolCalls {
			if tc.Index == nil {
				continue
			}
			idx := *tc.Index
			if toolCalls[idx] == nil {
				toolCalls[idx] = &ToolCall{ID: tc.ID, Name: tc.Function.Name}
			}
			toolCalls[idx].RawArgs += tc.Function.Arguments
		}

		if chunk.Choices[0].FinishReason == "tool_calls" {
			for i := 0; i < len(toolCalls); i++ {
				onEvent(StreamEvent{ToolCall: toolCalls[i]})
			}
		}
	}

	onEvent(StreamEvent{Done: true})
	return nil
}

func toOpenAI(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		msg := openai.ChatCompletionMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
		}
		for _, tc := range m.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, openai.ToolCall{
				ID:   tc.ID,
				Type: openai.ToolTypeFunction,
				Function: openai.FunctionCall{
					Name:      tc.Name,
					Arguments: tc.RawArgs,
				},
			})
		}
		out[i] = msg
	}
	return out
}

func toOpenAITools(tools []ToolDefinition) []openai.Tool {
	out := make([]openai.Tool, len(tools))
	for i, t := range tools {
		out[i] = openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		}
	}
	return out
}
