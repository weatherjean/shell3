package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"github.com/weatherjean/shell3/internal/llm"
)

// bodyTap is an http.RoundTripper that records the last request/response and
// extracts non-standard reasoning fields from SSE streams: OpenRouter's
// "reasoning" and Moonshot/DeepSeek's "reasoning_content".
type bodyTap struct {
	mu        sync.Mutex
	reqBody   []byte
	resBody   []byte
	reasoning string
	done      chan struct{}
	rt        http.RoundTripper
	// reasoningQueue is the incremental feed of reasoning fragments for onEvent
	// (reasoning, above, is the full accumulated string for snapshot/WaitReasoning).
	// The Stream goroutine drains it via drainReasoning, so onEvent is only ever
	// called from that single goroutine — never from the scan goroutine. Like
	// reasoning/done, it is per-request state reset by RoundTrip. All access is
	// under mu, so an orphaned scan goroutine appending here after a cancelled
	// turn is harmless (cleared by the next RoundTrip / drain).
	reasoningQueue []string
}

func (b *bodyTap) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(buf))
		b.mu.Lock()
		b.reqBody = buf
		b.reasoning = ""
		b.done = make(chan struct{})
		b.reasoningQueue = nil
		b.mu.Unlock()
	}
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
		return res, err
	}
	pr, pw := io.Pipe()
	teed := io.TeeReader(res.Body, pw)
	res.Body = readCloser{Reader: teed, Closer: composedCloser{res.Body, pw}}
	b.mu.Lock()
	done := b.done
	b.mu.Unlock()
	go b.scanReasoning(pr, done)
	return res, err
}

func (b *bodyTap) scanReasoning(r io.ReadCloser, done chan struct{}) {
	defer func() { _ = r.Close() }()
	defer close(done)
	var sb strings.Builder
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		payload, ok := strings.CutPrefix(line, "data: ")
		if !ok || payload == "[DONE]" {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Reasoning        string `json:"reasoning"`
					ReasoningContent string `json:"reasoning_content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			// Prefer "reasoning" (OpenRouter), fall back to "reasoning_content"
			// (Moonshot/DeepSeek); see bodyTap for the field-naming note.
			frag := c.Delta.Reasoning
			if frag == "" {
				frag = c.Delta.ReasoningContent
			}
			if frag != "" {
				sb.WriteString(frag)
				b.mu.Lock()
				b.reasoningQueue = append(b.reasoningQueue, frag)
				b.mu.Unlock()
			}
		}
	}
	b.mu.Lock()
	b.reasoning = sb.String()
	b.mu.Unlock()
}

func (b *bodyTap) snapshot() (req, res []byte, reasoning string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.reqBody...), append([]byte(nil), b.resBody...), b.reasoning
}

// WaitReasoning blocks until scanReasoning finishes or ctx is cancelled.
func (b *bodyTap) WaitReasoning(ctx context.Context) string {
	b.mu.Lock()
	done := b.done
	b.mu.Unlock()
	if done == nil {
		return ""
	}
	select {
	case <-done:
	case <-ctx.Done():
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reasoning
}

// drainReasoning returns and clears the reasoning fragments queued by the
// scanReasoning goroutine since the last drain. The Stream goroutine calls
// this and emits the fragments, keeping onEvent single-goroutine.
func (b *bodyTap) drainReasoning() []string {
	b.mu.Lock()
	q := b.reasoningQueue
	b.reasoningQueue = nil
	b.mu.Unlock()
	return q
}

type readCloser struct {
	io.Reader
	io.Closer
}

type composedCloser []io.Closer

func (cc composedCloser) Close() error {
	var firstErr error
	for _, c := range cc {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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
		params: llm.RequestParams{ReasoningEffort: "medium", MaxTokens: 16000},
	}
}

func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }
func (c *Client) SetExtra(m map[string]any)     { c.extra = m }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, Default: "medium"},
		{Name: "parallel_tool_calls", Enum: []string{"true", "false"}, Default: "true"},
		{Name: "temperature", Default: ""},
		{Name: "max_tokens", Default: "16000"},
	}
}

func (c *Client) LastTraffic() (req, res []byte) {
	if c.tap == nil {
		return nil, nil
	}
	r, s, _ := c.tap.snapshot()
	return r, s
}

// Stream sends msgs to the LLM and calls onEvent for each delta and completion.
func (c *Client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(c.model),
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

	var extraOpts []option.RequestOption
	for k, v := range c.extra {
		extraOpts = append(extraOpts, option.WithJSONSet(k, v))
	}
	// Surface the SDK's otherwise-invisible retries to the caller. The SDK
	// only retries getting the initial response, so this fires for pre-stream
	// failures (connection/5xx/429) — never mid-stream after tokens emit.
	extraOpts = append(extraOpts, option.WithMiddleware(retryObserver(onEvent, maxRetries)))
	stream := c.oc.Chat.Completions.NewStreaming(ctx, params, extraOpts...)
	defer func() { _ = stream.Close() }()

	toolCalls := map[int64]*llm.ToolCall{}
	var toolCallOrder []int64

	for stream.Next() {
		chunk := stream.Current()

		if c.tap != nil {
			for _, frag := range c.tap.drainReasoning() {
				onEvent(llm.StreamEvent{ReasoningDelta: frag})
			}
		}

		if u := chunk.Usage; u.PromptTokens > 0 || u.CompletionTokens > 0 {
			onEvent(llm.StreamEvent{Usage: &llm.Usage{
				PromptTokens:     int(u.PromptTokens),
				CompletionTokens: int(u.CompletionTokens),
				TotalTokens:      int(u.TotalTokens),
			}})
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			onEvent(llm.StreamEvent{TextDelta: delta.Content})
		}

		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if toolCalls[idx] == nil {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", idx)
				}
				toolCalls[idx] = &llm.ToolCall{ID: id, Name: tc.Function.Name}
				toolCallOrder = append(toolCallOrder, idx)
			}
			toolCalls[idx].RawArgs += tc.Function.Arguments
		}
	}

	if err := stream.Err(); err != nil {
		// On error/ctx-cancel we return here without the final drain below, so
		// any reasoning queued after the last in-loop drain is discarded.
		// Reasoning is best-effort on a failed/cancelled turn, whose partial
		// output the caller abandons anyway — matching the pre-funnel behavior.
		return fmt.Errorf("llm: stream: %w", err)
	}

	_ = stream.Close()
	if c.tap != nil {
		// WaitReasoning blocks until scanReasoning finishes (on the success
		// path it returns promptly), so this final drain captures any fragments
		// queued after the last content chunk, before the Done event.
		c.tap.WaitReasoning(ctx)
		for _, frag := range c.tap.drainReasoning() {
			onEvent(llm.StreamEvent{ReasoningDelta: frag})
		}
	}

	seen := map[string]int{}
	for i, idx := range toolCallOrder {
		tc := toolCalls[idx]
		if tc == nil {
			continue
		}
		if seen[tc.ID] > 0 {
			tc.ID = fmt.Sprintf("%s_%d", tc.ID, i)
		}
		seen[tc.ID]++
		onEvent(llm.StreamEvent{ToolCall: tc})
	}

	onEvent(llm.StreamEvent{Done: true})
	return nil
}

func toMessages(msgs []llm.Message) []openai.ChatCompletionMessageParamUnion {
	out := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs))
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleSystem:
			out = append(out, openai.SystemMessage(m.Content))
		case llm.RoleUser:
			if len(m.ContentParts) > 0 {
				parts := make([]openai.ChatCompletionContentPartUnionParam, 0, len(m.ContentParts))
				for _, p := range m.ContentParts {
					switch p.Type {
					case llm.ContentPartTypeText:
						parts = append(parts, openai.TextContentPart(p.Text))
					case llm.ContentPartTypeImageURL:
						parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
							URL: p.ImageURL,
						}))
					case llm.ContentPartTypeInputAudio:
						parts = append(parts, openai.InputAudioContentPart(openai.ChatCompletionContentPartInputAudioInputAudioParam{
							Data:   p.AudioData,
							Format: p.AudioFormat,
						}))
					}
				}
				out = append(out, openai.UserMessage(parts))
			} else {
				out = append(out, openai.UserMessage(m.Content))
			}
		case llm.RoleAssistant:
			asst := openai.ChatCompletionAssistantMessageParam{}
			if m.Content != "" {
				asst.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(m.Content),
				}
			}
			if len(m.ToolCalls) > 0 {
				tcs := make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					tcs[i] = openai.ChatCompletionMessageToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.RawArgs,
						},
					}
				}
				asst.ToolCalls = tcs
			}
			// The SDK has no field for the reasoning_content vendor extension
			// (see llm.Message.ReasoningContent); inject via SetExtraFields so
			// it survives MarshalJSON.
			if m.ReasoningContent != "" {
				asst.SetExtraFields(map[string]any{"reasoning_content": m.ReasoningContent})
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
		case llm.RoleTool:
			out = append(out, openai.ToolMessage(m.Content, m.ToolCallID))
		}
	}
	return out
}

func toTools(tools []llm.ToolDefinition) []openai.ChatCompletionToolParam {
	out := make([]openai.ChatCompletionToolParam, len(tools))
	for i, t := range tools {
		out[i] = openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  shared.FunctionParameters(t.Parameters),
			},
		}
	}
	return out
}
