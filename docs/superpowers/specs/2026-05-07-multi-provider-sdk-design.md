# Multi-Provider SDK Design

**Date:** 2026-05-07
**Branch:** `feature/proxies` (off `simplify/auth-yaml`)

## Goal

Replace the third-party `sashabaranov/go-openai` SDK with the official `openai/openai-go`, add a native Anthropic adapter using `anthropics/anthropic-sdk-go`, and document Codex as a third-party proxy (`openai-oauth`) only — no first-party Codex code in the repo.

## Motivation

- **First-party SDKs** track upstream API surface accurately (parameter shapes, streaming events, helper builders), reducing hand-rolled glue and bug surface.
- **Native Anthropic** lets users run Claude models directly without an OpenAI-compatible shim. Extended thinking and the messages-API tool flow need direct adapter support.
- **No native Codex.** Codex uses OAuth, not static keys. Maintaining an in-tree OAuth flow is cost without benefit when [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) already exposes Codex as a standard OpenAI-compatible endpoint.

## Architecture

```
cmd/shell3/run.go        buildClient → llm.Get(instance.Type) → Provider.NewClient
                                             │
                  ┌──────────────────────────┼──────────────────────────┐
                  │                                                     │
internal/adapter/openai/                              internal/adapter/anthropic/
  register.go  (init: llm.Register("openai"))          register.go  (init: llm.Register("anthropic"))
  client.go    (openai/openai-go + bodyTap)            client.go    (anthropics/anthropic-sdk-go)
```

Each adapter self-registers via `init()`. `cmd/shell3/main.go` blank-imports both. Provider selection at chat-loop runtime is driven by the `type` field on `config.Instance`, not by a build-time default. Mid-session model switches across providers (e.g. openai → anthropic) re-resolve the provider on every `buildClient` call.

## Components

### `config.Instance` (extended)

Add required `Type string` field. Values: `"openai"` | `"anthropic"`. Drives registry dispatch.

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
    api_key: ant-…
    models:
      - id: claude-sonnet-4-6
        context_window: 200000
```

### `internal/adapter/openai` (rewritten)

- Switch from `sashabaranov/go-openai` to `openai/openai-go` v1.12.0.
- Use SDK helpers (`openai.SystemMessage`, `openai.UserMessage`, `openai.AssistantMessage`, `openai.ToolMessage`) for request building.
- Streaming: `client.Chat.Completions.NewStreaming(...)` returning `*ssestream.Stream[ChatCompletionChunk]`.
- **Preserve `bodyTap`** — the HTTP RoundTripper that scrapes the OpenRouter/Moonshot non-standard `reasoning` field from raw SSE. Inject via `option.WithHTTPClient(&http.Client{Transport: tap})`.
- Public surface unchanged: `Stream`, `SetModel`, `SetParams`, `ParamSpecs`, `LastTraffic`, `LastReasoning`.

### `internal/adapter/anthropic` (new)

- `anthropics/anthropic-sdk-go` v1.41.0.
- Streaming: `client.Messages.NewStreaming(...)` returning `*ssestream.Stream[MessageStreamEventUnion]`.
- Event handling via `event.Type` switch:
  - `content_block_start` (tool_use → buffer id+name)
  - `content_block_delta`:
    - `text_delta` → `StreamEvent.TextDelta`
    - `thinking_delta` → `StreamEvent.ReasoningDelta`
    - `input_json_delta` → append to buffered tool_use args
  - `message_start` / `message_delta` → token usage
- Message builders: `NewUserMessage`, `NewAssistantMessage`, `NewTextBlock`, `NewToolUseBlock`, `NewToolResultBlock`.
- Extended thinking: `anthropic.ThinkingConfigParamOfEnabled(int64(budget))`. Plumbed via new `RequestParams.ThinkingBudget int`.
- `MaxTokens` required by Anthropic API. Sourced from `RequestParams.MaxTokens` (new field, vendor-neutral, default 16000). Settable per-persona.
- **Tool result grouping**: consecutive `RoleTool` messages collapse into one user message with multiple `tool_result` blocks (Anthropic requires this).

### `cmd/shell3/run.go` (dispatch refactor)

Replace single-provider lookup with per-call resolution:

```go
buildClient := func(instName, m string) (chat.LLMClient, error) {
    inst, ok := authStore.Get(instName)
    if !ok {
        return nil, fmt.Errorf("no instance %q in auth store", instName)
    }
    p, ok := llm.Get(inst.Type)
    if !ok {
        return nil, fmt.Errorf("unknown adapter type %q for instance %q", inst.Type, instName)
    }
    return p.NewClient(ctx, authStore, instName, m)
}
```

Initial `streamer` is built via `buildClient(instance, model)` instead of capturing a `prov` variable from setup. The existing `modelSwitcher` already calls `buildClient` for cross-instance switches — no changes there.

### Codex (docs only)

- `cmd/shell3/shell3.md` and `README.md` document the [openai-oauth](https://github.com/EvanZhouDev/openai-oauth) proxy. User runs `npx openai-oauth` locally, adds it to `auth.yaml` as a regular `openai` instance pointing at `http://localhost:3000/v1`. No in-tree code, no auth flow, no Cobra subcommand.
- All non-doc traces of Codex deleted: `.shell3/plans/codex-oauth.md`, the "codex" mention in `internal/llm/types.go:44` comment, the README provider list line.

## Data Flow

Chat loop is unchanged. Stream events emitted by either adapter use the existing `llm.StreamEvent` shape (`TextDelta` / `ReasoningDelta` / `ToolCall` / `Usage` / `Done`). The TUI consumes the same shape — zero changes to `internal/chat`, `internal/patchapp`, widgets, or renderer.

## Error Handling

- Missing instance type → fail at `buildClient` with `unknown adapter type %q for instance %q`. Surfaces as a chat error, doesn't crash.
- Anthropic streaming errors propagate via `stream.Err()` and wrap as `llm: anthropic stream: %w`.
- OpenAI streaming errors propagate via `stream.Err()` and wrap as `llm: stream: %w` (matches existing message).
- Invalid `type` value at config-load time: not validated at load — caught at first dispatch. Acceptable trade-off; auth file already trusted.

## Testing

- `internal/config/authstore_test.go`: `Type` field round-trips correctly for both providers.
- `internal/adapter/openai/internals_test.go`: existing `bodyTap` + `scanReasoning` tests preserved (no SDK dependency). New `toMessages` / `toTools` tests for SDK helper wiring.
- `internal/adapter/anthropic/client_test.go`: `toAnthropicMessages` (system extraction, tool-result grouping), `toAnthropicTools`, `ParamSpecs`.
- Smoke test: live Ollama call after rebuild.

## What Does Not Change

- `internal/llm/{types,provider}.go` interfaces (only an additive `RequestParams.ThinkingBudget` field).
- `internal/chat/*` — fully untouched.
- `internal/patchapp/*` and widgets — fully untouched.
- `cmd/shell3/{auth,secrets,doctor,docs,widget}.go` Cobra wiring (auth template body updates only).

## Risks

- **Tool-call IDs across providers.** OpenAI uses `call_…`, Anthropic uses `toolu_…`. shell3's chat loop already round-trips opaque IDs, so no breakage expected, but smoke test must verify a tool-using turn on Anthropic.
- **Anthropic `MaxTokens` is required.** Surfaced as vendor-neutral persona param `max_tokens` (default 16000, double Cursor's 8k baseline since shell3 is a coding agent that emits long diffs and multi-file changes). Plumbed to anthropic `MaxTokens` and openai `MaxCompletionTokens`.
- **bodyTap and the official SDK.** The official SDK respects `option.WithHTTPClient`, but its Transport injection path is less battle-tested than `sashabaranov`. Existing `bodyTap` tests stay green only because they exercise `bodyTap` in isolation; integration is verified by the smoke test.

## Out of Scope

- Codex OAuth in-tree.
- Anthropic prompt caching.
- Streaming back-pressure / retry policy changes.
- Switching the OpenAI helper from chat-completions to responses API.
