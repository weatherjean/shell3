package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/weatherjean/shell3/pkg/llm"
)

// trafficTap is an http.RoundTripper that buffers the last request body and
// non-2xx response body so the chat layer can dump them on stream errors.
// 2xx response bodies are SSE streams — not buffered to avoid blocking.
type trafficTap struct {
	mu      sync.Mutex
	reqBody []byte
	resBody []byte
	rt      http.RoundTripper
}

func (t *trafficTap) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(buf))
		t.mu.Lock()
		t.reqBody = buf
		t.mu.Unlock()
	}
	res, err := t.rt.RoundTrip(req)
	if err != nil || res == nil || res.Body == nil {
		return res, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		buf, _ := io.ReadAll(res.Body)
		res.Body = io.NopCloser(bytes.NewReader(buf))
		t.mu.Lock()
		t.resBody = buf
		t.mu.Unlock()
	}
	return res, err
}

func (t *trafficTap) snapshot() (req, res []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.reqBody...), append([]byte(nil), t.resBody...)
}

// Client is an Anthropic streaming LLM client using the official SDK.
type Client struct {
	ac     anthropic.Client
	tap    *trafficTap
	model  string
	params llm.RequestParams
}

// NewClient creates a Client. baseURL is optional (empty = default api.anthropic.com).
func NewClient(apiKey, baseURL, model string) *Client {
	tap := &trafficTap{rt: http.DefaultTransport}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Transport: tap}),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{
		ac:     anthropic.NewClient(opts...),
		tap:    tap,
		model:  model,
		params: llm.RequestParams{MaxTokens: 16000},
	}
}

// LastTraffic returns the last request body and non-2xx response body
// captured by the underlying HTTP transport. Empty if no request has been made.
func (c *Client) LastTraffic() (req, res []byte) {
	if c.tap == nil {
		return nil, nil
	}
	return c.tap.snapshot()
}

func (c *Client) SetModel(model string)         { c.model = model }
func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, Default: "medium"},
		{Name: "max_tokens", Default: "16000"},
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

	// Map reasoning_effort onto thinking.budget_tokens. Auto-bump
	// max_tokens to cover budget + 4k output headroom (Anthropic
	// requires budget < max_tokens).
	budget := effortToBudget(c.params.ReasoningEffort)
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

// imageBlock translates a shell3 ImageURL value (either an HTTPS URL or a
// "data:image/<type>;base64,<data>" URI) into an Anthropic image content
// block. Unrecognized inputs return ok=false so the caller can drop them
// instead of poisoning the request.
func imageBlock(imageURL string) (anthropic.ContentBlockParamUnion, bool) {
	if imageURL == "" {
		return anthropic.ContentBlockParamUnion{}, false
	}
	if strings.HasPrefix(imageURL, "data:") {
		// data:<mediatype>;base64,<data>
		body, ok := strings.CutPrefix(imageURL, "data:")
		if !ok {
			return anthropic.ContentBlockParamUnion{}, false
		}
		semi := strings.Index(body, ";")
		comma := strings.Index(body, ",")
		if semi < 0 || comma < 0 || comma <= semi {
			return anthropic.ContentBlockParamUnion{}, false
		}
		mediaType := body[:semi]
		// Anthropic only accepts image/{jpeg,png,gif,webp}.
		switch mediaType {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return anthropic.ContentBlockParamUnion{}, false
		}
		data := body[comma+1:]
		return anthropic.NewImageBlockBase64(mediaType, data), true
	}
	if strings.HasPrefix(imageURL, "http://") || strings.HasPrefix(imageURL, "https://") {
		return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: imageURL}), true
	}
	return anthropic.ContentBlockParamUnion{}, false
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
			if len(m.ContentParts) > 0 {
				blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.ContentParts))
				for _, p := range m.ContentParts {
					switch p.Type {
					case llm.ContentPartTypeText:
						if p.Text != "" {
							blocks = append(blocks, anthropic.NewTextBlock(p.Text))
						}
					case llm.ContentPartTypeImageURL:
						if blk, ok := imageBlock(p.ImageURL); ok {
							blocks = append(blocks, blk)
						}
					}
				}
				if len(blocks) == 0 {
					blocks = append(blocks, anthropic.NewTextBlock(""))
				}
				out = append(out, anthropic.NewUserMessage(blocks...))
			} else {
				out = append(out, anthropic.NewUserMessage(anthropic.NewTextBlock(m.Content)))
			}
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
