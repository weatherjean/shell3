package anthropic

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/weatherjean/shell3/internal/llm"
)

// Client is an Anthropic streaming LLM client using the official SDK.
type Client struct {
	ac     anthropic.Client
	model  string
	params llm.RequestParams
}

// NewClient creates a Client. baseURL is optional (empty = default api.anthropic.com).
func NewClient(apiKey, baseURL, model string) *Client {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{
		ac:     anthropic.NewClient(opts...),
		model:  model,
		params: llm.RequestParams{MaxTokens: 16000},
	}
}

func (c *Client) SetModel(model string)         { c.model = model }
func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, Default: "medium"},
		{Name: "max_tokens", Default: "16000"},
		{Name: "thinking_budget", Default: "0"},
		{Name: "temperature", Default: ""},
	}
}

// effortToBudget maps the vendor-neutral reasoning_effort enum onto
// Anthropic's thinking.budget_tokens. Values picked to match common
// Claude Code tiers; budget must be < max_tokens.
func effortToBudget(effort string) int64 {
	switch effort {
	case "low":
		return 2000
	case "medium":
		return 6000
	case "high":
		return 12000
	case "xhigh":
		return 24000
	default: // "", "none", "minimal" → no thinking
		return 0
	}
}

// Stream sends msgs to Anthropic and calls onEvent for each delta and completion.
func (c *Client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	history, system := toAnthropicMessages(msgs)

	maxTok := int64(c.params.MaxTokens)
	if maxTok <= 0 {
		maxTok = 16000
	}

	// Resolve thinking budget: explicit thinking_budget wins; else map
	// reasoning_effort onto a budget. Auto-bump max_tokens to cover
	// budget + 4k output headroom (Anthropic requires budget < max_tokens).
	budget := int64(c.params.ThinkingBudget)
	if budget == 0 && c.params.ReasoningEffort != "" {
		budget = effortToBudget(c.params.ReasoningEffort)
	}
	if budget > 0 && budget+4000 > maxTok {
		maxTok = budget + 4000
	}

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		Messages:  history,
		MaxTokens: maxTok,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Text: system}}
	}
	if len(tools) > 0 {
		params.Tools = toAnthropicTools(tools)
	}
	if budget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(budget)
	}
	if c.params.Temperature != nil {
		params.Temperature = anthropic.Float(*c.params.Temperature)
	}

	stream := c.ac.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	type toolUseBlock struct {
		id       string
		name     string
		inputBuf []byte
	}
	toolBlocks := map[int64]*toolUseBlock{}
	var toolBlockOrder []int64
	var inputTokens, outputTokens int64

	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "content_block_start":
			e := event.AsContentBlockStart()
			if e.ContentBlock.Type == "tool_use" {
				toolBlocks[e.Index] = &toolUseBlock{
					id:   e.ContentBlock.ID,
					name: e.ContentBlock.Name,
				}
				toolBlockOrder = append(toolBlockOrder, e.Index)
			}
		case "content_block_delta":
			e := event.AsContentBlockDelta()
			delta := e.Delta
			switch delta.Type {
			case "text_delta":
				onEvent(llm.StreamEvent{TextDelta: delta.AsTextDelta().Text})
			case "thinking_delta":
				onEvent(llm.StreamEvent{ReasoningDelta: delta.AsThinkingDelta().Thinking})
			case "input_json_delta":
				if tb := toolBlocks[e.Index]; tb != nil {
					tb.inputBuf = append(tb.inputBuf, delta.AsInputJSONDelta().PartialJSON...)
				}
			}
		case "message_start":
			inputTokens = event.AsMessageStart().Message.Usage.InputTokens
		case "message_delta":
			outputTokens = event.AsMessageDelta().Usage.OutputTokens
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("llm: anthropic stream: %w", err)
	}

	if inputTokens > 0 || outputTokens > 0 {
		onEvent(llm.StreamEvent{Usage: &llm.Usage{
			PromptTokens:     int(inputTokens),
			CompletionTokens: int(outputTokens),
			TotalTokens:      int(inputTokens + outputTokens),
		}})
	}

	for _, idx := range toolBlockOrder {
		tb := toolBlocks[idx]
		if tb == nil {
			continue
		}
		args := string(tb.inputBuf)
		if args == "" {
			args = "{}"
		}
		onEvent(llm.StreamEvent{ToolCall: &llm.ToolCall{
			ID:      tb.id,
			Name:    tb.name,
			RawArgs: args,
		}})
	}

	onEvent(llm.StreamEvent{Done: true})
	return nil
}

// toAnthropicMessages converts shell3 messages to Anthropic MessageParam slice.
// The system message (if any) is extracted and returned separately. Consecutive
// RoleTool messages collapse into a single user message with multiple
// tool_result content blocks (Anthropic requires this shape).
func toAnthropicMessages(msgs []llm.Message) ([]anthropic.MessageParam, string) {
	var system string
	var out []anthropic.MessageParam

	i := 0
	for i < len(msgs) {
		m := msgs[i]
		switch m.Role {
		case llm.RoleSystem:
			system = m.Content
			i++
		case llm.RoleUser:
			out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			i++
		case llm.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion
			if m.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(m.Content))
			}
			for _, tc := range m.ToolCalls {
				var input map[string]any
				if tc.RawArgs != "" {
					_ = json.Unmarshal([]byte(tc.RawArgs), &input)
				}
				if input == nil {
					input = map[string]any{}
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			if len(blocks) > 0 {
				out = append(out, anthropic.NewAssistantMessage(blocks...))
			}
			i++
		case llm.RoleTool:
			var resultBlocks []anthropic.ContentBlockParamUnion
			for i < len(msgs) && msgs[i].Role == llm.RoleTool {
				resultBlocks = append(resultBlocks, anthropic.NewToolResultBlock(msgs[i].ToolCallID, msgs[i].Content, false))
				i++
			}
			out = append(out, anthropic.NewUserMessage(resultBlocks...))
		default:
			i++
		}
	}
	return out, system
}

// toAnthropicTools converts shell3 tool definitions to Anthropic ToolUnionParam.
// shell3 stores Parameters as a full JSONSchema object; Anthropic auto-injects
// type=object on its InputSchema, so we extract just `properties` and `required`.
func toAnthropicTools(tools []llm.ToolDefinition) []anthropic.ToolUnionParam {
	out := make([]anthropic.ToolUnionParam, len(tools))
	for i, t := range tools {
		schema := anthropic.ToolInputSchemaParam{}
		if props, ok := t.Parameters["properties"]; ok {
			schema.Properties = props
		}
		if req, ok := t.Parameters["required"]; ok {
			switch r := req.(type) {
			case []string:
				schema.Required = r
			case []any:
				for _, v := range r {
					if s, ok := v.(string); ok {
						schema.Required = append(schema.Required, s)
					}
				}
			}
		}
		out[i] = anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        t.Name,
				Description: anthropic.String(t.Description),
				InputSchema: schema,
			},
		}
	}
	return out
}
