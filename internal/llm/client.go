package llm

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

	openai "github.com/sashabaranov/go-openai"
)

// bodyTap is an http.RoundTripper that records the last request body and
// last response body it sees. For successful streaming responses it also
// tees the body into a per-request reasoning extractor so we can capture
// fields go-openai does not parse — notably OpenRouter-style "reasoning"
// (Moonshot/kimi via opencode-go).
type bodyTap struct {
	mu        sync.Mutex
	reqBody   []byte
	resBody   []byte
	reasoning string
	done      chan struct{} // closed when scanReasoning finishes
	rt        http.RoundTripper
}

func (b *bodyTap) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		buf, _ := io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(buf))
		b.mu.Lock()
		b.reqBody = buf
		b.reasoning = ""
		b.done = make(chan struct{})
		b.mu.Unlock()
	}
	res, err := b.rt.RoundTrip(req)
	if err != nil || res == nil || res.Body == nil {
		return res, err
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Capture full error body — small JSON, worth the diagnostic value.
		buf, _ := io.ReadAll(res.Body)
		res.Body = io.NopCloser(bytes.NewReader(buf))
		b.mu.Lock()
		b.resBody = buf
		b.mu.Unlock()
		return res, err
	}
	// 2xx streaming: tee into a side reader without buffering the whole
	// body. The side goroutine parses SSE chunks for non-standard fields
	// (e.g. "reasoning") that go-openai discards.
	pr, pw := io.Pipe()
	teed := io.TeeReader(res.Body, pw)
	res.Body = readCloser{Reader: teed, Closer: composedCloser{res.Body, pw}}
	b.mu.Lock()
	done := b.done
	b.mu.Unlock()
	go b.scanReasoning(pr, done)
	return res, err
}

// scanReasoning reads SSE chunks from r, accumulating the OpenRouter-style
// "reasoning" delta field. Stops when the stream ends or any read error fires.
// Closes b.done when finished so callers can wait for the result.
func (b *bodyTap) scanReasoning(r io.ReadCloser, done chan struct{}) {
	defer r.Close()
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
					Reasoning string `json:"reasoning"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}
		for _, c := range chunk.Choices {
			if c.Delta.Reasoning != "" {
				sb.WriteString(c.Delta.Reasoning)
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

// readCloser composes an io.Reader with an io.Closer.
type readCloser struct {
	io.Reader
	io.Closer
}

// composedCloser closes multiple closers, returning the first error.
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

// Client is an OpenAI-compatible streaming LLM client.
type Client struct {
	oc    *openai.Client
	model string
	tap   *bodyTap
}

// NewClient creates a Client targeting baseURL with the given apiKey and model.
func NewClient(baseURL, apiKey, model string) *Client {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = baseURL
	tap := &bodyTap{rt: http.DefaultTransport}
	cfg.HTTPClient = &http.Client{Transport: tap}
	return &Client{
		oc:    openai.NewClientWithConfig(cfg),
		model: model,
		tap:   tap,
	}
}

// LastTraffic returns the last request body sent and last response body
// received by the underlying HTTP client. Empty if no request has been made.
func (c *Client) LastTraffic() (req, res []byte) {
	if c.tap == nil {
		return nil, nil
	}
	r, s, _ := c.tap.snapshot()
	return r, s
}

// LastReasoning returns the OpenRouter-style "reasoning" text accumulated
// from the last successful streaming response. Empty when the provider does
// not emit it.
func (c *Client) LastReasoning() string {
	if c.tap == nil {
		return ""
	}
	_, _, r := c.tap.snapshot()
	return r
}

// SetModel swaps the active model for subsequent requests.
func (c *Client) SetModel(model string) {
	c.model = model
}

// Stream sends msgs to the LLM and calls onEvent for each token, tool call, and completion.
func (c *Client) Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error {
	req := openai.ChatCompletionRequest{
		Model:         c.model,
		Messages:      toOpenAI(msgs),
		Stream:        true,
		StreamOptions: &openai.StreamOptions{IncludeUsage: true},
		// Disable provider-side "thinking" by default. Moonshot/kimi rejects
		// follow-up turns when assistant tool-call messages lack a
		// reasoning_content field — and proxies often strip reasoning from
		// streamed chunks, so we never have one to echo back. Models that do
		// not understand this kwarg ignore it.
		ChatTemplateKwargs: map[string]any{"enable_thinking": false},
	}
	if len(tools) > 0 {
		req.Tools = toOpenAITools(tools)
	}

	stream, err := c.oc.CreateChatCompletionStream(ctx, req)
	if err != nil {
		return fmt.Errorf("llm: stream: %w", err)
	}
	defer stream.Close() // safe to call twice; we close explicitly below to flush the tee

	toolCalls := map[int]*ToolCall{}

	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("llm: recv: %w", err)
		}
		if chunk.Usage != nil {
			onEvent(StreamEvent{Usage: &Usage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}})
		}

		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta

		if delta.Content != "" {
			onEvent(StreamEvent{TextDelta: delta.Content})
		}
		if delta.ReasoningContent != "" {
			onEvent(StreamEvent{ReasoningDelta: delta.ReasoningContent})
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

	}

	// Emit accumulated tool calls once the stream ends. Some proxies
	// (opencode-go → Moonshot) omit or vary the FinishReason field, so
	// gating emit on FinishReason="tool_calls" misses calls entirely.
	// Dedupe IDs here too: parallel calls sometimes share or omit IDs.
	if len(toolCalls) > 0 {
		seen := map[string]int{}
		for i := 0; i < len(toolCalls); i++ {
			tc := toolCalls[i]
			if tc == nil {
				continue
			}
			if tc.ID == "" {
				tc.ID = fmt.Sprintf("call_%d", i)
			}
			if seen[tc.ID] > 0 {
				tc.ID = fmt.Sprintf("%s_%d", tc.ID, i)
			}
			seen[tc.ID]++
			onEvent(StreamEvent{ToolCall: tc})
		}
	}

	// Side-band reasoning capture: close the stream so the body finishes
	// being read (which lets the tee pipe close), then wait for the
	// scanner goroutine to publish accumulated "reasoning" text.
	stream.Close()
	if c.tap != nil {
		if r := c.tap.WaitReasoning(ctx); r != "" {
			onEvent(StreamEvent{ReasoningDelta: r})
		}
	}

	onEvent(StreamEvent{Done: true})
	return nil
}

// WaitReasoning blocks until the scanReasoning goroutine for the current
// request finishes (or ctx is cancelled), then returns the accumulated text.
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

func toOpenAI(msgs []Message) []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(msgs))
	for i, m := range msgs {
		msg := openai.ChatCompletionMessage{
			Role:             string(m.Role),
			Content:          m.Content,
			ToolCallID:       m.ToolCallID,
			Name:             m.Name,
			ReasoningContent: m.ReasoningContent,
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
