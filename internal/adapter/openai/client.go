package openai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"

	"github.com/weatherjean/shell3/internal/llm"
)

// bodyTap is an http.RoundTripper that records the last request/response so a
// failed turn can dump both to last_error.json.
//
// It used to do more: it tee'd the response body through an io.Pipe into a
// second, independent SSE scanner running on its own goroutine, purely to
// recover the non-standard "reasoning"/"reasoning_content" delta fields — with
// a done-channel barrier and a mutex-guarded fragment queue to get those
// fragments back onto the Stream goroutine. None of that was necessary:
// openai-go parses unknown delta keys into Delta.JSON.ExtraFields, so the
// fields are readable inline in the normal loop (see deltaReasoning). Parsing
// the same stream twice also meant two sources of truth for one wire format,
// which is how reasoning ended up interleaved into answers.
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

// Default request params, declared once so the live params and the ParamSpecs
// documentation strings cannot drift.
const (
	defaultReasoningEffort = "medium"
	defaultMaxTokens       = 16000
)

func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }
func (c *Client) SetExtra(m map[string]any)     { c.extra = m }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"none", "minimal", "low", "medium", "high", "xhigh"}, Default: defaultReasoningEffort},
		// Default "" — the adapter never sets ParallelToolCalls unless asked
		// (nil leaves the provider's own default in effect).
		{Name: "parallel_tool_calls", Enum: []string{"true", "false"}, Default: ""},
		{Name: "temperature", Default: ""},
		{Name: "max_tokens", Default: strconv.Itoa(defaultMaxTokens)},
	}
}

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
		if text, thought := part.pushDelta(delta.Content, reasoning); text != "" || thought != "" {
			if text != "" {
				onEvent(llm.StreamEvent{TextDelta: text})
			}
			if thought != "" {
				onEvent(llm.StreamEvent{ReasoningDelta: thought})
			}
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
		return wrapStreamErr(err)
	}

	_ = stream.Close()
	if out, thought := part.flush(); out != "" || thought != "" {
		if out != "" {
			onEvent(llm.StreamEvent{TextDelta: out})
		}
		if thought != "" {
			onEvent(llm.StreamEvent{ReasoningDelta: thought})
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

// hasVideoPart reports whether any part is a video_url part — the one
// ContentPart type with no openai-go SDK representation.
func hasVideoPart(parts []llm.ContentPart) bool {
	for _, p := range parts {
		if p.Type == llm.ContentPartTypeVideoURL {
			return true
		}
	}
	return false
}

// Raw content-part shapes for rawContentParts. Plain structs (rather than
// map[string]any) so encoding/json preserves field declaration order —
// data field first, "type" last, matching the wire shape the openai-go SDK produces.
type rawURLField struct {
	URL string `json:"url"`
}
type rawTextPart struct {
	Text string `json:"text"`
	Type string `json:"type"`
}
type rawImageURLPart struct {
	ImageURL rawURLField `json:"image_url"`
	Type     string      `json:"type"`
}
type rawInputAudioField struct {
	Data   string `json:"data"`
	Format string `json:"format"`
}
type rawInputAudioPart struct {
	InputAudio rawInputAudioField `json:"input_audio"`
	Type       string             `json:"type"`
}
type rawFileField struct {
	FileData string `json:"file_data"`
	Filename string `json:"filename"`
}
type rawFilePart struct {
	File rawFileField `json:"file"`
	Type string       `json:"type"`
}
type rawVideoURLPart struct {
	VideoURL rawURLField `json:"video_url"`
	Type     string      `json:"type"`
}

// rawContentParts builds a message's "content" array by hand, matching
// exactly the wire shape the openai-go SDK produces for
// text/image_url/input_audio/file parts, plus the video_url extension the
// SDK has no type for. Used only for messages containing a video_url part
// (see toMessages), so every part in the message goes through this path
// rather than mixing SDK-typed and raw JSON.
func rawContentParts(parts []llm.ContentPart) []any {
	out := make([]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llm.ContentPartTypeText:
			out = append(out, rawTextPart{Type: "text", Text: p.Text})
		case llm.ContentPartTypeImageURL:
			out = append(out, rawImageURLPart{Type: "image_url", ImageURL: rawURLField{URL: p.ImageURL}})
		case llm.ContentPartTypeInputAudio:
			out = append(out, rawInputAudioPart{Type: "input_audio", InputAudio: rawInputAudioField{Data: p.AudioData, Format: p.AudioFormat}})
		case llm.ContentPartTypeFile:
			out = append(out, rawFilePart{Type: "file", File: rawFileField{Filename: p.FileName, FileData: p.FileData}})
		case llm.ContentPartTypeVideoURL:
			out = append(out, rawVideoURLPart{Type: "video_url", VideoURL: rawURLField{URL: p.VideoURL}})
		}
	}
	return out
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
			if len(m.ContentParts) > 0 {
				if hasVideoPart(m.ContentParts) {
					// video_url is an OpenRouter/Gemini-style extension of the
					// OpenAI dialect with no openai-go SDK representation
					// (the union param type has no video variant), so once a
					// message needs it we build its whole "content" array by
					// hand and inject it via SetExtraFields rather than mix
					// SDK-typed and raw parts.
					user := openai.ChatCompletionUserMessageParam{}
					user.SetExtraFields(map[string]any{"content": rawContentParts(m.ContentParts)})
					out = append(out, openai.ChatCompletionMessageParamUnion{OfUser: &user})
				} else {
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
						case llm.ContentPartTypeFile:
							parts = append(parts, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
								Filename: openai.String(p.FileName),
								FileData: openai.String(p.FileData),
							}))
						}
					}
					out = append(out, openai.UserMessage(parts))
				}
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
