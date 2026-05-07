# Multi-Provider SDK Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `sashabaranov/go-openai` with the official `openai/openai-go` SDK, add a native Anthropic adapter using `anthropics/anthropic-sdk-go`, wire both into the `llm.Provider` registry via a `type` field on `config.Instance`, and document Codex proxy setup.

**Architecture:** Each adapter is a minimal package (`internal/adapter/openai`, `internal/adapter/anthropic`) that self-registers via `init()`. `run.go`'s `buildClient` re-resolves the provider by `instance.Type` on every call so mid-session model switching works across providers. The `bodyTap` HTTP interceptor is preserved for OpenRouter/Moonshot `reasoning` field extraction by injecting a custom `http.Client` into the official openai-go SDK via `option.WithHTTPClient`.

**Tech Stack:** `github.com/openai/openai-go` v1.12.0, `github.com/anthropics/anthropic-sdk-go` v1.41.0, existing `llm.Provider`/`llm.Streamer` interfaces.

---

### Task 0: Purge Codex traces from active code

Codex is no longer a first-party adapter. Only the user-facing `README.md` and `cmd/shell3/shell3.md` reference it (as third-party `openai-oauth` proxy setup). Historical plans/specs in `docs/superpowers/` are dated snapshots — leave them.

**Files:**
- Delete: `.shell3/plans/codex-oauth.md`
- Modify: `internal/llm/types.go` (strip codex mention from comment)
- Modify: `README.md` (rewrite native-Codex claim to point at proxy)

- [ ] **Step 1: Delete stale plan**

```bash
git rm .shell3/plans/codex-oauth.md
```

- [ ] **Step 2: Strip codex from `internal/llm/types.go:44`**

Find the comment containing "codex `reasoning` items" and rewrite without naming codex. Example replacement:

```go
// Some providers stream reasoning items with `encrypted_content` that must
// be echoed back on the next turn.
```

- [ ] **Step 3: Rewrite README.md provider line**

Find line 18:

```
Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …) and **Codex** (ChatGPT subscription via OAuth).
```

Replace with:

```
Works with any **OpenAI-compatible API endpoint** (OpenAI, Ollama, Groq, LM Studio, OpenRouter, …) and **Anthropic** natively. Codex (ChatGPT subscription via OAuth) is supported via the third-party [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy — see `shell3 docs`.
```

- [ ] **Step 4: Verify no codex traces in active code**

```bash
grep -rli codex --exclude-dir=.git --exclude-dir=docs/superpowers .
```

Expected: empty output (only `docs/superpowers/` historical refs remain, excluded).

- [ ] **Step 5: Build + test**

```bash
go build ./...
go test ./...
```

Expected: pass.

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: purge codex from active code (proxy-only via openai-oauth)"
```

---

### Task 1: Add `type` field to `config.Instance`

**Files:**
- Modify: `internal/config/authstore.go`
- Modify: `internal/config/authstore_test.go` (create if missing)
- Modify: `cmd/shell3/auth.go` (template update)

- [ ] **Step 1: Write the failing test**

In `internal/config/authstore_test.go`:

```go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

func TestLoadAuthStoreType(t *testing.T) {
	dir := t.TempDir()
	yaml := `instances:
  - name: myoai
    type: openai
    base_url: https://api.openai.com/v1
    api_key: sk-test
    models:
      - id: gpt-4o
        context_window: 128000
  - name: myant
    type: anthropic
    api_key: ant-test
    models:
      - id: claude-sonnet-4-6
        context_window: 200000
`
	path := filepath.Join(dir, "auth.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := config.LoadAuthStoreFromPath(path)
	if err != nil {
		t.Fatal(err)
	}
	oai, ok := store.Get("myoai")
	if !ok || oai.Type != "openai" {
		t.Fatalf("openai instance: %+v ok=%v", oai, ok)
	}
	ant, ok := store.Get("myant")
	if !ok || ant.Type != "anthropic" {
		t.Fatalf("anthropic instance: %+v ok=%v", ant, ok)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./internal/config/... -run TestLoadAuthStoreType -v
```

Expected: FAIL — `LoadAuthStoreFromPath` undefined, `Type` field missing.

- [ ] **Step 3: Add `Type` field and `LoadAuthStoreFromPath`**

In `internal/config/authstore.go`, update `Instance` and add a path-based loader:

```go
// Instance is one configured provider in the auth YAML.
type Instance struct {
	Name    string     `yaml:"name"`
	Type    string     `yaml:"type"` // required: "openai" | "anthropic"
	BaseURL string     `yaml:"base_url,omitempty"`
	APIKey  string     `yaml:"api_key,omitempty"`
	Models  []ModelDef `yaml:"models"`
}

// LoadAuthStoreFromPath reads the auth YAML from an explicit path.
func LoadAuthStoreFromPath(path string) (*AuthStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &AuthStore{}, nil
		}
		return nil, err
	}
	var af authFile
	if err := yaml.Unmarshal(data, &af); err != nil {
		return nil, err
	}
	return &AuthStore{data: af}, nil
}

// LoadAuthStore reads the auth YAML from homeDir. Returns an empty store if
// the file does not exist.
func LoadAuthStore(homeDir string) (*AuthStore, error) {
	return LoadAuthStoreFromPath(paths.NewGlobal(homeDir).Auth)
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test ./internal/config/... -run TestLoadAuthStoreType -v
```

Expected: PASS.

- [ ] **Step 5: Update auth template in `cmd/shell3/auth.go`**

Replace the template constant body inside `openInEditor` with:

```go
template := `# Shell3 Authentication
# AI ASSISTANTS: Do not read this file. It contains credentials.
# Add one entry per provider instance.

instances: []

# Example: local Ollama (no API key needed)
# instances:
#   - name: ollama
#     type: openai
#     base_url: http://localhost:11434/v1
#     api_key: ""
#     models:
#       - id: llama3.2
#         context_window: 131072
#
# Example: Anthropic
# instances:
#   - name: anthropic
#     type: anthropic
#     api_key: ant-your-key-here
#     models:
#       - id: claude-sonnet-4-6
#         context_window: 200000
#
# For Codex proxy see: shell3 docs → Providers
`
```

- [ ] **Step 6: Run all tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/config/authstore.go internal/config/authstore_test.go cmd/shell3/auth.go
git commit -m "feat: add Type field to config.Instance for multi-provider dispatch"
```

---

### Task 2: Add SDK dependencies

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)

- [ ] **Step 1: Add both SDKs**

```bash
go get github.com/openai/openai-go@v1.12.0
go get github.com/anthropics/anthropic-sdk-go@v1.41.0
```

- [ ] **Step 2: Verify modules present**

```bash
grep -E "openai/openai-go|anthropics/anthropic" go.mod
```

Expected: both lines appear.

- [ ] **Step 3: Build compiles**

```bash
go build ./...
```

Expected: success (no code references either SDK yet).

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add openai/openai-go and anthropics/anthropic-sdk-go"
```

---

### Task 3: Rewrite OpenAI adapter using official SDK

**Files:**
- Modify: `internal/adapter/openai/client.go`
- Modify: `internal/adapter/openai/internals_test.go`
- Keep: `internal/adapter/openai/register.go` (unchanged)

The `bodyTap` type and its tests are preserved unchanged — only the `Client` struct and `Stream` method change to use the official SDK. `toOpenAI` and `toOpenAITools` are replaced by direct use of SDK helpers.

- [ ] **Step 1: Verify existing tests pass before touching anything**

```bash
go test ./internal/adapter/openai/... -v
```

Expected: all pass.

- [ ] **Step 2: Rewrite `client.go`**

Replace the entire file content:

```go
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
	"github.com/openai/openai-go/packages/ssestream"

	"github.com/weatherjean/shell3/internal/llm"
)

// bodyTap is an http.RoundTripper that records the last request/response and
// extracts the OpenRouter-style "reasoning" field from SSE streams (Moonshot/kimi).
type bodyTap struct {
	mu               sync.Mutex
	reqBody          []byte
	resBody          []byte
	reasoning        string
	done             chan struct{}
	rt               http.RoundTripper
	onReasoningDelta func(string)
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
				b.mu.Lock()
				cb := b.onReasoningDelta
				b.mu.Unlock()
				if cb != nil {
					cb(c.Delta.Reasoning)
				}
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
	oc     *openai.Client
	model  string
	tap    *bodyTap
	params llm.RequestParams
}

// NewClient creates a Client targeting baseURL with the given apiKey and model.
func NewClient(baseURL, apiKey, model string) *Client {
	tap := &bodyTap{rt: http.DefaultTransport}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Transport: tap}),
	}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Client{
		oc:     openai.NewClient(opts...),
		model:  model,
		tap:    tap,
		params: llm.RequestParams{ReasoningEffort: "medium", Verbosity: "medium"},
	}
}

func (c *Client) SetModel(model string)           { c.model = model }
func (c *Client) SetParams(p llm.RequestParams)   { c.params = c.params.Merge(p) }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "reasoning_effort", Enum: []string{"minimal", "low", "medium", "high"}, Default: "medium"},
		{Name: "verbosity", Enum: []string{"low", "medium", "high"}, Default: "medium"},
		{Name: "parallel_tool_calls", Enum: []string{"true", "false"}, Default: "true"},
		{Name: "temperature", Default: ""},
	}
}

func (c *Client) LastTraffic() (req, res []byte) {
	if c.tap == nil {
		return nil, nil
	}
	r, s, _ := c.tap.snapshot()
	return r, s
}

func (c *Client) LastReasoning() string {
	if c.tap == nil {
		return ""
	}
	_, _, r := c.tap.snapshot()
	return r
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
	if c.params.ReasoningEffort != "" {
		params.ReasoningEffort = openai.ReasoningEffort(c.params.ReasoningEffort)
	}
	if c.params.Temperature != nil {
		params.Temperature = openai.Float(*c.params.Temperature)
	}
	if c.params.ParallelToolCalls != nil {
		params.ParallelToolCalls = openai.Bool(*c.params.ParallelToolCalls)
	}

	if c.tap != nil {
		c.tap.mu.Lock()
		c.tap.onReasoningDelta = func(r string) { onEvent(llm.StreamEvent{ReasoningDelta: r}) }
		c.tap.mu.Unlock()
		defer func() {
			c.tap.mu.Lock()
			c.tap.onReasoningDelta = nil
			c.tap.mu.Unlock()
		}()
	}

	stream := c.oc.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	toolCalls := map[int]*llm.ToolCall{}

	for stream.Next() {
		chunk := stream.Current()

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

		for i, tc := range delta.ToolCalls {
			idx := int(tc.Index)
			if toolCalls[idx] == nil {
				id := tc.ID
				if id == "" {
					id = fmt.Sprintf("call_%d", i)
				}
				toolCalls[idx] = &llm.ToolCall{ID: id, Name: tc.Function.Name}
			}
			toolCalls[idx].RawArgs += tc.Function.Arguments
		}
	}

	if err := stream.Err(); err != nil {
		return fmt.Errorf("llm: stream: %w", err)
	}

	_ = stream.Close()
	if c.tap != nil {
		c.tap.WaitReasoning(ctx)
	}

	seen := map[string]int{}
	for i := 0; i < len(toolCalls); i++ {
		tc := toolCalls[i]
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
						parts = append(parts, openai.TextPart(p.Text))
					case llm.ContentPartTypeImageURL:
						parts = append(parts, openai.ImagePart(p.ImageURL))
					}
				}
				out = append(out, openai.UserMessageParts(parts...))
			} else {
				out = append(out, openai.UserMessage(m.Content))
			}
		case llm.RoleAssistant:
			msg := openai.AssistantMessage(m.Content)
			if len(m.ToolCalls) > 0 {
				tcs := make([]openai.ChatCompletionMessageToolCallParam, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					tcs[i] = openai.ChatCompletionMessageToolCallParam{
						ID:   tc.ID,
						Type: "function",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      tc.Name,
							Arguments: tc.RawArgs,
						},
					}
				}
				msg.OfAssistant.ToolCalls = tcs
			}
			out = append(out, msg)
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
			Type: "function",
			Function: openai.FunctionDefinitionParam{
				Name:        t.Name,
				Description: openai.String(t.Description),
				Parameters:  openai.FunctionParameters(t.Parameters),
			},
		}
	}
	return out
}
```

- [ ] **Step 3: Remove old SDK import from `go.mod`**

We'll leave `sashabaranov/go-openai` for now and run `go mod tidy` after all adapters are wired in (Task 6). For now just ensure it compiles:

```bash
go build ./internal/adapter/openai/...
```

Expected: success.

- [ ] **Step 4: Update `internals_test.go` to drop `toOpenAI`/`toOpenAITools` tests**

The bodyTap tests need no changes — they test the `bodyTap` type which is unchanged. Remove the `toOpenAI` and `toOpenAITools` test functions and replace with equivalents for the new helpers:

```go
// ---- toMessages ----

func TestToMessagesBasic(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	out := toMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestToMessagesToolCall(t *testing.T) {
	msgs := []llm.Message{
		{
			Role: llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{
				{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			},
		},
	}
	out := toMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
}

func TestToMessagesToolResult(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleTool, Content: "output", ToolCallID: "tc1"},
	}
	out := toMessages(msgs)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
}

// ---- toTools ----

func TestToTools(t *testing.T) {
	tools := []llm.ToolDefinition{
		{Name: "bash", Description: "run shell", Parameters: map[string]any{"type": "object"}},
	}
	out := toTools(tools)
	if len(out) != 1 || out[0].Function.Name != "bash" {
		t.Fatalf("tool: %+v", out)
	}
}
```

- [ ] **Step 5: Run openai adapter tests**

```bash
go test ./internal/adapter/openai/... -v
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/openai/client.go internal/adapter/openai/internals_test.go
git commit -m "feat: rewrite openai adapter using official openai/openai-go SDK"
```

---

### Task 4: Create Anthropic adapter

**Files:**
- Create: `internal/adapter/anthropic/register.go`
- Create: `internal/adapter/anthropic/client.go`
- Create: `internal/adapter/anthropic/client_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/adapter/anthropic/client_test.go`:

```go
package anthropic

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestParamSpecs(t *testing.T) {
	c := &Client{}
	specs := c.ParamSpecs()
	names := map[string]bool{}
	for _, s := range specs {
		names[s.Name] = true
	}
	for _, want := range []string{"thinking_budget", "temperature"} {
		if !names[want] {
			t.Errorf("missing param spec %q", want)
		}
	}
}

func TestToAnthropicMessages_Basic(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "" {
		t.Fatalf("expected no system, got %q", system)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestToAnthropicMessages_SystemExtracted(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "you are helpful"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "you are helpful" {
		t.Fatalf("system: %q", system)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 non-system msg, got %d", len(out))
	}
}

func TestToAnthropicMessages_ToolResultGrouped(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			{ID: "tc2", Name: "bash", RawArgs: `{"cmd":"pwd"}`},
		}},
		{Role: llm.RoleTool, Content: "file.txt", ToolCallID: "tc1"},
		{Role: llm.RoleTool, Content: "/home", ToolCallID: "tc2"},
	}
	out, _ := toAnthropicMessages(msgs)
	// assistant (tool_use) + user (grouped tool_results)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (assistant+grouped-user), got %d", len(out))
	}
}

func TestToAnthropicTools(t *testing.T) {
	tools := []llm.ToolDefinition{
		{Name: "bash", Description: "run shell", Parameters: map[string]any{"type": "object"}},
	}
	out := toAnthropicTools(tools)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
go test ./internal/adapter/anthropic/... -v 2>&1 | head -20
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Create `register.go`**

```go
package anthropic

import (
	"context"
	"fmt"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
)

type provider struct{}

func init() { llm.Register("anthropic", &provider{}) }

func (*provider) Name() string         { return "anthropic" }
func (*provider) SingleInstance() bool { return false }

func (*provider) NewClient(_ context.Context, store *config.AuthStore, instance, model string) (llm.Streamer, error) {
	inst, ok := store.Get(instance)
	if !ok {
		return nil, fmt.Errorf("anthropic: no instance %q — edit ~/.shell3/ai-do-not-read.auth.yaml", instance)
	}
	if model == "" && len(inst.Models) > 0 {
		model = inst.Models[0].ID
	}
	return NewClient(inst.APIKey, inst.BaseURL, model), nil
}
```

- [ ] **Step 4: Create `client.go`**

```go
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
	ac     *anthropic.Client
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
		ac:    anthropic.NewClient(opts...),
		model: model,
	}
}

func (c *Client) SetModel(model string)         { c.model = model }
func (c *Client) SetParams(p llm.RequestParams) { c.params = c.params.Merge(p) }

func (c *Client) ParamSpecs() []llm.ParamSpec {
	return []llm.ParamSpec{
		{Name: "thinking_budget", Default: ""},
		{Name: "temperature", Default: ""},
	}
}

// Stream sends msgs to Anthropic and calls onEvent for each delta and completion.
func (c *Client) Stream(ctx context.Context, msgs []llm.Message, tools []llm.ToolDefinition, onEvent func(llm.StreamEvent)) error {
	history, system := toAnthropicMessages(msgs)

	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(c.model),
		Messages:  history,
		MaxTokens: 8192,
	}
	if system != "" {
		params.System = []anthropic.TextBlockParam{{Type: "text", Text: system}}
	}
	if len(tools) > 0 {
		params.Tools = toAnthropicTools(tools)
	}
	if c.params.ThinkingBudget > 0 {
		params.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(c.params.ThinkingBudget))
	}
	if c.params.Temperature != nil {
		params.Temperature = anthropic.Float(*c.params.Temperature)
	}

	stream := c.ac.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()

	// Track tool_use blocks by content-block index so we can emit them at end.
	type toolUseBlock struct {
		id       string
		name     string
		inputBuf []byte
	}
	toolBlocks := map[int64]*toolUseBlock{}
	var inputTokens, outputTokens int64

	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "content_block_start":
			e := event.AsContentBlockStart()
			switch e.ContentBlock.Type {
			case "text":
				// nothing to initialise
			case "thinking":
				// nothing to initialise
			case "tool_use":
				toolBlocks[e.Index] = &toolUseBlock{
					id:   e.ContentBlock.ID,
					name: e.ContentBlock.Name,
				}
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

	for i := int64(0); int(i) < len(toolBlocks); i++ {
		tb := toolBlocks[i]
		if tb == nil {
			continue
		}
		onEvent(llm.StreamEvent{ToolCall: &llm.ToolCall{
			ID:      tb.id,
			Name:    tb.name,
			RawArgs: string(tb.inputBuf),
		}})
	}

	onEvent(llm.StreamEvent{Done: true})
	return nil
}

// toAnthropicMessages converts shell3 messages to Anthropic MessageParam slice.
// The system message (if any) is extracted and returned separately.
// Consecutive RoleTool messages are grouped into a single user message with
// multiple tool_result content blocks, as Anthropic requires.
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
				_ = json.Unmarshal([]byte(tc.RawArgs), &input)
				blocks = append(blocks, anthropic.NewToolUseBlock(tc.ID, input, tc.Name))
			}
			out = append(out, anthropic.NewAssistantMessage(blocks...))
			i++
		case llm.RoleTool:
			// Gather consecutive tool results into one user message.
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

func toAnthropicTools(tools []llm.ToolDefinition) []anthropic.ToolParam {
	out := make([]anthropic.ToolParam, len(tools))
	for i, t := range tools {
		out[i] = anthropic.ToolParam{
			Name:        t.Name,
			Description: anthropic.String(t.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: t.Parameters,
			},
		}
	}
	return out
}
```

- [ ] **Step 5: Add `ThinkingBudget` and `MaxTokens` to `llm.RequestParams`**

In `internal/llm/types.go` (or wherever `RequestParams` is defined), add:

```go
ThinkingBudget int // anthropic extended thinking budget tokens (0 = disabled)
MaxTokens      int // max output tokens (0 = adapter default)
```

Update `Merge` (zero value means "not set"):

```go
if p.ThinkingBudget > 0 {
    out.ThinkingBudget = p.ThinkingBudget
}
if p.MaxTokens > 0 {
    out.MaxTokens = p.MaxTokens
}
```

Default `RequestParams` value (wherever defaults are set, e.g. persona loader or adapter `NewClient`) gets `MaxTokens: 16000`.

In anthropic `client.go` `Stream`, replace `MaxTokens: 8192` with:

```go
maxTok := int64(c.params.MaxTokens)
if maxTok <= 0 {
    maxTok = 16000
}
params := anthropic.MessageNewParams{
    Model:     anthropic.Model(c.model),
    Messages:  history,
    MaxTokens: maxTok,
}
```

In openai `client.go` `Stream`, after temperature handling add:

```go
if c.params.MaxTokens > 0 {
    params.MaxCompletionTokens = openai.Int(int64(c.params.MaxTokens))
}
```

Add `max_tokens` to both adapters' `ParamSpecs()` returns:

```go
{Name: "max_tokens", Default: "16000"},
```

Append to `internal/scaffold/defaults/personas/base.md` parameters block:

```yaml
parameters:
  reasoning_effort: medium
  reasoning_summary: auto
  verbosity: medium
  parallel_tool_calls: true
  max_tokens: 16000
  thinking_budget: 0
```

In `internal/persona/persona.go`, extend `PersonaParams`:

```go
type PersonaParams struct {
    ReasoningEffort   string   `yaml:"reasoning_effort"`
    ReasoningSummary  string   `yaml:"reasoning_summary"`
    Verbosity         string   `yaml:"verbosity"`
    ParallelToolCalls *bool    `yaml:"parallel_tool_calls"`
    Temperature       *float64 `yaml:"temperature"`
    MaxTokens         int      `yaml:"max_tokens"`
    ThinkingBudget    int      `yaml:"thinking_budget"`
}
```

Update `ToRequestParams`:

```go
func (pp PersonaParams) ToRequestParams() llm.RequestParams {
    return llm.RequestParams{
        ReasoningEffort:   pp.ReasoningEffort,
        ReasoningSummary:  pp.ReasoningSummary,
        Verbosity:         pp.Verbosity,
        ParallelToolCalls: pp.ParallelToolCalls,
        Temperature:       pp.Temperature,
        MaxTokens:         pp.MaxTokens,
        ThinkingBudget:    pp.ThinkingBudget,
    }
}
```

- [ ] **Step 6: Run adapter tests**

```bash
go test ./internal/adapter/anthropic/... -v
```

Expected: all pass.

- [ ] **Step 7: Build whole project**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 8: Commit**

```bash
git add internal/adapter/anthropic/ internal/llm/
git commit -m "feat: add Anthropic adapter using official anthropics/anthropic-sdk-go SDK"
```

---

### Task 5: Update `run.go` to dispatch by instance type

**Files:**
- Modify: `cmd/shell3/run.go`

The initial `prov` variable and `buildClient` closure must both re-resolve the provider by `instance.Type` so mid-session provider switches (e.g. openai → anthropic) work correctly.

- [ ] **Step 1: Write a failing test (compile-time)**

Ensure the project still builds with the changed logic:

```bash
go build ./cmd/shell3/...
```

Expected: success (it won't yet — `run.go` still uses the old `prov` local).

- [ ] **Step 2: Refactor `runChat` in `run.go`**

Replace this block:

```go
providerHint := coalesce(f.provider, pCfg.Provider)
instance, model := resolveConnection(providerHint, pCfg.Model, authStore, f)
if instance == "" {
    return fmt.Errorf("no provider configured — run: shell3 auth")
}
prov, ok := llm.Get("openai")
if !ok {
    return fmt.Errorf("openai adapter not registered")
}
```

With:

```go
providerHint := coalesce(f.provider, pCfg.Provider)
instance, model := resolveConnection(providerHint, pCfg.Model, authStore, f)
if instance == "" {
    return fmt.Errorf("no provider configured — run: shell3 auth")
}
```

Replace `buildClient`:

```go
buildClient := func(inst, m string) (chat.LLMClient, error) {
    return prov.NewClient(ctx, authStore, inst, m)
}
```

With:

```go
buildClient := func(instName, m string) (chat.LLMClient, error) {
    instCfg, ok := authStore.Get(instName)
    if !ok {
        return nil, fmt.Errorf("no instance %q in auth store", instName)
    }
    p, ok := llm.Get(instCfg.Type)
    if !ok {
        return nil, fmt.Errorf("unknown adapter type %q for instance %q", instCfg.Type, instName)
    }
    return p.NewClient(ctx, authStore, instName, m)
}
```

Replace the streamer creation block:

```go
streamer, err := prov.NewClient(ctx, authStore, instance, model)
if err != nil {
    return err
}
```

With:

```go
streamer, err := buildClient(instance, model)
if err != nil {
    return err
}
```

- [ ] **Step 3: Build**

```bash
go build ./cmd/shell3/...
```

Expected: success.

- [ ] **Step 4: Run all tests**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/shell3/run.go
git commit -m "fix: dispatch buildClient by instance.Type for multi-provider support"
```

---

### Task 6: Wire Anthropic adapter into `main.go`, tidy modules

**Files:**
- Modify: `cmd/shell3/main.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add blank import**

In `cmd/shell3/main.go`, add alongside the openai import:

```go
import (
    // ...
    _ "github.com/weatherjean/shell3/internal/adapter/anthropic"
    _ "github.com/weatherjean/shell3/internal/adapter/openai"
    // ...
)
```

- [ ] **Step 2: Remove old SDK, tidy**

```bash
go mod tidy
```

Verify `sashabaranov/go-openai` is gone from `go.mod`:

```bash
grep sashabaranov go.mod
```

Expected: no output.

- [ ] **Step 3: Build and test**

```bash
go build ./...
go test ./...
```

Expected: both succeed.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/main.go go.mod go.sum
git commit -m "chore: register anthropic adapter, drop sashabaranov/go-openai, tidy modules"
```

---

### Task 7: Update docs and auth template

**Files:**
- Modify: `cmd/shell3/shell3.md` (providers section)
- Modify: `cmd/shell3/auth.go` (template already done in Task 1; update Codex docs here)

- [ ] **Step 1: Read current shell3.md providers section**

```bash
grep -n -A5 -B5 -i "provider\|auth\|codex\|anthropic\|openai" cmd/shell3/shell3.md | head -80
```

- [ ] **Step 2: Update Providers section in `shell3.md`**

Locate the Providers / Authentication section and replace it with:

````markdown
## Providers

shell3 supports two built-in provider types: **openai** (any OpenAI-compatible endpoint) and **anthropic** (Anthropic API directly). Configure them in `~/.shell3/ai-do-not-read.auth.yaml` using `shell3 auth`.

Each instance requires a `type` field:

```yaml
instances:
  - name: ollama
    type: openai
    base_url: http://localhost:11434/v1
    api_key: ""
    models:
      - id: llama3.2
        context_window: 131072

  - name: anthropic
    type: anthropic
    api_key: ant-your-key-here
    models:
      - id: claude-sonnet-4-6
        context_window: 200000
```

### Codex Proxy

OpenAI Codex uses OAuth, not a static API key, so shell3 doesn't support it natively. Instead run [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) locally — it exposes Codex as a standard OpenAI-compatible endpoint:

```bash
npx openai-oauth
```

Then add it to your auth file as a regular `openai` instance:

```yaml
  - name: codex
    type: openai
    base_url: http://localhost:3000/v1
    api_key: ""
    models:
      - id: codex-mini-latest
        context_window: 200000
```
````

- [ ] **Step 3: Build docs command still works**

```bash
go build ./cmd/shell3/...
shell3 docs 2>/dev/null | grep -i provider | head -5
```

Expected: provider-related content visible.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/shell3.md
git commit -m "docs: document openai/anthropic types and Codex proxy setup"
```

---

### Task 8: Full test pass and smoke test

**Files:** none — verification only.

- [ ] **Step 1: Full test suite**

```bash
go test ./... -count=1
```

Expected: all pass, zero failures.

- [ ] **Step 2: Build release binary**

```bash
make build
```

Expected: success.

- [ ] **Step 3: Smoke test with Ollama (if running)**

```bash
echo "say one word" | ./shell3 2>&1 | head -5
```

Expected: model response visible, no panics.

If Ollama not available, skip and note in commit.

- [ ] **Step 4: Verify both providers registered**

```go
// quick one-liner check
go run ./cmd/shell3 --help 2>&1 | head -5
```

Expected: help text renders without errors.

- [ ] **Step 5: Final commit**

```bash
git add -A
git commit -m "test: full pass, smoke test — multi-provider SDK complete"
```

---

## Self-Review

**Spec coverage:**
- `type` field on Instance → Task 1 ✓
- Official openai-go SDK → Task 3 ✓
- bodyTap preserved via `option.WithHTTPClient` → Task 3 ✓
- Anthropic adapter → Task 4 ✓
- Mid-session provider switch via `buildClient` re-resolve → Task 5 ✓
- Anthropic blank import → Task 6 ✓
- Old SDK removed → Task 6 ✓
- Codex proxy docs → Task 7 ✓
- Tool result grouping for Anthropic → Task 4 `toAnthropicMessages` ✓
- Extended thinking → Task 4 `ThinkingBudget` + `ThinkingConfigParamOfEnabled` ✓

**Placeholder scan:** No TBDs found. All code blocks are complete.

**Type consistency:**
- `toAnthropicMessages` / `toAnthropicTools` defined in Task 4 client.go and referenced in Task 4 test ✓
- `toMessages` / `toTools` defined in Task 3 client.go and referenced in Task 3 test ✓
- `ThinkingBudget int` added to `RequestParams` in Task 4 Step 5 ✓
- `buildClient` signature `(instName, m string)` consistent across Tasks 5 and 2 ✓
