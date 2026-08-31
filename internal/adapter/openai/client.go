package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/weatherjean/shell3/internal/llm"
)

// bodyTap is an http.RoundTripper that records the last request/response so a
// failed turn can dump both to last_error.json. It does NOT re-parse the SSE
// stream: unknown delta keys are readable inline via Delta.JSON.ExtraFields
// (see deltaReasoning), and a second parse of the same wire format is how
// reasoning once ended up interleaved into answers.
type bodyTap struct {
	mu      sync.Mutex
	reqBody []byte
	resBody []byte
	rt      http.RoundTripper
}

func (b *bodyTap) RoundTrip(req *http.Request) (*http.Response, error) {
	// Reset the buffered response body unconditionally: a stale resBody left
	// over from a previous non-2xx attempt would otherwise pair with this
	// request in LastTraffic's snapshot and actively mislead debugging.
	b.mu.Lock()
	b.resBody = nil
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(buf))
		b.reqBody = buf
	}
	b.mu.Unlock()
	res, err := b.rt.RoundTrip(req)
	if err != nil || res == nil || res.Body == nil {
		return res, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		buf, _ := io.ReadAll(res.Body)
		res.Body = io.NopCloser(bytes.NewReader(buf))
		b.mu.Lock()
		b.resBody = buf
		b.mu.Unlock()
	}
	return res, err
}

func (b *bodyTap) snapshot() (req, res []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.reqBody...), append([]byte(nil), b.resBody...)
}

// Client is an OpenAI-compatible streaming LLM client using the official SDK.
type Client struct {
	oc     openai.Client
	model  string
	tap    *bodyTap
	params llm.RequestParams
	extra  map[string]any
}

// NewClient creates a Client targeting baseURL with the given apiKey and model.
func NewClient(baseURL, apiKey, model string) *Client {
	tap := &bodyTap{rt: http.DefaultTransport}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Transport: tap}),
		option.WithMaxRetries(maxRetries),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{
		oc:     openai.NewClient(opts...),
		model:  model,
		tap:    tap,
		params: llm.RequestParams{ReasoningEffort: defaultReasoningEffort, MaxTokens: defaultMaxTokens},
	}
}

// Default request params.
const (
	defaultReasoningEffort = "medium"
	defaultMaxTokens       = 16000
)

func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }
func (c *Client) SetExtra(m map[string]any)     { c.extra = m }

func (c *Client) LastTraffic() (req, res []byte) {
	if c.tap == nil {
		return nil, nil
	}
	return c.tap.snapshot()
}

// Stream sends msgs to the LLM and calls onEvent for each delta and completion.
func (c *Client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	params := openai.ChatCompletionNewParams{
		Model:    c.model,
		Messages: toMessages(msgs),
		StreamOptions: openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		},
	}
	if len(tools) > 0 {
		params.Tools = toTools(tools)
	}
	if eff := c.params.ReasoningEffort; eff != "" && eff != "none" {
		// OpenAI API accepts only minimal|low|medium|high; clamp xhigh→high
		// so a vendor-neutral persona that requests xhigh still works here.
		if eff == "xhigh" {
			eff = "high"
		}
		params.ReasoningEffort = shared.ReasoningEffort(eff)
	}
	if c.params.Temperature != nil {
		params.Temperature = openai.Float(*c.params.Temperature)
	}
	if c.params.ParallelToolCalls != nil {
		params.ParallelToolCalls = openai.Bool(*c.params.ParallelToolCalls)
	}
	if c.params.MaxTokens > 0 {
		params.MaxCompletionTokens = openai.Int(int64(c.params.MaxTokens))
	}

	extraOpts := make([]option.RequestOption, 0, len(c.extra)+1)
	for k, v := range c.extra {
		extraOpts = append(extraOpts, option.WithJSONSet(k, v))
	}
	// Surface the SDK's otherwise-invisible retries to the caller. The SDK only
	// retries getting the initial response, so this fires for pre-stream failures
	// (connection/5xx/429), never mid-stream after tokens emit.
	extraOpts = append(extraOpts, option.WithMiddleware(retryObserver(onEvent)))
	stream := c.oc.Chat.Completions.NewStreaming(ctx, params, extraOpts...)
	defer func() { _ = stream.Close() }()

	// emitParts forwards a partitioner's split (one delta, or the final flush):
	// answer text and thought are separate events, and an empty half emits
	// nothing.
	emitParts := func(text, thought string) {
		if text != "" {
			onEvent(llm.StreamEvent{TextDelta: text})
		}
		if thought != "" {
			onEvent(llm.StreamEvent{ReasoningDelta: thought})
		}
	}

	toolCalls := map[int64]*llm.ToolCall{}
	var toolCallOrder []int64
	var part tagPartitioner
	var truncated bool

	for stream.Next() {
		chunk := stream.Current()

		if u := chunk.Usage; u.PromptTokens > 0 || u.CompletionTokens > 0 {
			onEvent(llm.StreamEvent{Usage: &llm.Usage{
				PromptTokens:     int(u.PromptTokens),
				CompletionTokens: int(u.CompletionTokens),
				TotalTokens:      int(u.TotalTokens),
				CachedTokens:     int(u.PromptTokensDetails.CachedTokens),
			}})
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		if chunk.Choices[0].FinishReason == "length" {
			truncated = true
		}
		delta := chunk.Choices[0].Delta

		// Reasoning rides the SAME delta as content on OpenAI-compatible
		// providers, and on MiniMax the content is frequently a duplicate of
		// it. The partitioner combines both signals (see pushDelta) and is the
		// single place that decides what is answer and what is thought.
		reasoning := deltaReasoning(delta)
		if reasoning != "" {
			onEvent(llm.StreamEvent{ReasoningDelta: reasoning})
		}
		emitParts(part.pushDelta(delta.Content, reasoning))

		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if toolCalls[idx] == nil {
				toolCalls[idx] = &llm.ToolCall{}
				toolCallOrder = append(toolCallOrder, idx)
			}
			// Tool-call deltas are fragments. Most providers put ID and name in
			// the first fragment, but OpenAI-compatible endpoints are allowed to
			// deliver either later and may split the function name too.
			if toolCalls[idx].ID == "" && tc.ID != "" {
				toolCalls[idx].ID = tc.ID
			}
			toolCalls[idx].Name += tc.Function.Name
			toolCalls[idx].RawArgs += tc.Function.Arguments
		}
	}

	if err := stream.Err(); err != nil {
		return wrapStreamErr(err)
	}

	_ = stream.Close()
	emitParts(part.flush())

	seen := map[string]int{}
	for i, idx := range toolCallOrder {
		tc := toolCalls[idx]
		if tc == nil {
			continue
		}
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d", idx)
		}
		if seen[tc.ID] > 0 {
			tc.ID = fmt.Sprintf("%s_%d", tc.ID, i)
		}
		seen[tc.ID]++
		onEvent(llm.StreamEvent{ToolCall: tc})
	}

	onEvent(llm.StreamEvent{Done: true, Truncated: truncated})
	return nil
}

// wrapStreamErr maps a stream.Err() into a returned error. A mid-stream EOF
// (the provider closed the SSE connection with no terminating event) gets a
// clearer, actionable message; all other errors keep the generic wrap. The
// original error is wrapped in both cases so errors.Is still works, and an SDK
// API error additionally gets an llm.StatusError shell so consumers (e.g.
// shell3.RecoveryHint) can branch on the HTTP code with errors.As.
func wrapStreamErr(err error) error {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return fmt.Errorf("llm: the model stream ended early — the provider closed the connection mid-response. "+
			"Common causes: out of credits/quota, a rate limit, or an upstream proxy/timeout. "+
			"Check your provider balance and any ~/.shell3/proxy-*.log: %w", err)
	}
	wrapped := fmt.Errorf("llm: stream: %w", err)
	var oe *openai.Error
	if errors.As(err, &oe) {
		return &llm.StatusError{Code: oe.StatusCode, Err: wrapped}
	}
	return wrapped
}

func toMessages(msgs []llm.Message) []openai.ChatCompletionMessageParamUnion {
	// Thinking mode is a conversation-level property: once any assistant
	// message carries reasoning, thinking providers (DeepSeek) require the
	// reasoning_content field on EVERY assistant message passed back — a
	// tool-call hop that happened to emit no reasoning still needs the field
	// (empty), or the next request 400s with "reasoning_content … must be
	// passed back to the API". Conversations with no reasoning anywhere keep
	// the vendor extension off the wire so strict providers stay happy.
	thinking := false
	for _, m := range msgs {
		if m.Role == llm.RoleAssistant && m.ReasoningContent != "" {
			thinking = true
			break
		}
	}
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			out = append(out, openai.UserMessage(m.Content))
		case llm.RoleAssistant:
			asst := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				asst.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				}
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]openai.ChatCompletionMessageToolCallUnionParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					tcs[i] = openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID: tc.ID,
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: tc.RawArgs,
							},
						},
					}
				}
				asst.ToolCalls = tcs
			}
			// The SDK has no field for the reasoning_content vendor extension
			// (see llm.Message.ReasoningContent); inject via SetExtraFields so
			// it survives MarshalJSON.
			if thinking {
				asst.SetExtraFields(map[string]any{"reasoning_content": m.ReasoningContent})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
		case llm.RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return out
}

func toTools(tools []llm.ToolDefinition) []openai.ChatCompletionToolUnionParam {
	out := make([]openai.ChatCompletionToolUnionParam, len(tools))
	for i, t := range tools {
		out[i] = openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:        t.Name,
			Description: openai.String(t.Description),
			Parameters:  shared.FunctionParameters(t.Parameters),
		})
	}
	return out
}
