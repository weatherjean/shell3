# Plan: Codex OAuth Provider for shell3

## Goal

Add ChatGPT subscription auth (Sign in with ChatGPT) as a new model provider, isolated in `internal/providers/codex/`. Zero changes to existing OpenAI/API-key flows.

## Why

OpenAI is the only major provider currently tolerating third-party clients on subscription auth (Anthropic banned Jan/Apr 2026, Google banned Mar 2026). Codex OAuth lets shell3 users with a ChatGPT plan use Codex models without per-token API billing.

## Risk Acknowledgement

- OpenAI ToS does not explicitly bless third-party Codex OAuth. Tolerated, not sanctioned.
- Could be revoked at any time (Google/Anthropic precedent).
- User accounts bear ban risk — document in README.

## Architecture

### New package: `internal/providers/codex/`

```
internal/providers/codex/
  oauth.go       # PKCE flow, browser callback server on localhost:1455
  tokens.go      # Token storage, refresh, JWT id_token parsing
  client.go      # llm.LLMClient implementation
  responses.go   # Responses API request/response + SSE stream parser
  models.go      # Hardcoded model list
  doc.go         # Package-level docs + risk notes
```

### Token storage

- File: `~/.shell3/codex_tokens.json`, mode 0600
- Schema: `{access_token, refresh_token, id_token, account_id, expires_at}`
- Separate from `credentials.yaml` — keeps schema untouched
- Refresh: lazy, inside `Stream()` when `expires_at < now()`

### Endpoints

- OAuth: `https://auth.openai.com/oauth/authorize`, `/oauth/token`
- Inference: `https://chatgpt.com/backend-api/codex/responses`
- Client ID: `app_EMoamEEZ73f0CkXaXp7hrann` (official Codex CLI's; shared)
- Redirect: `http://localhost:1455/auth/callback`
- Scope: `openid profile email offline_access`
- Extras: `id_token_add_organizations=true`, `codex_cli_simplified_flow=true`, `originator=shell3`

### Required headers per request

```
authorization: Bearer <access_token>
ChatGPT-Account-Id: <account_id from JWT>
originator: shell3
User-Agent: shell3/<version> (<platform> <release>; <arch>)
session_id: <uuid>
```

### Models (initial)

Hardcoded list, mirror what subscription tier allows:
- `gpt-5.1-codex`
- `gpt-5.1-codex-mini`
- `gpt-5.2`
- `gpt-5.3-codex`
- `gpt-5.4`

Refresh list when OpenAI updates Codex CLI's allowed models.

## Core Touchpoints (Interface-Driven, No Codex Names)

**Principle:** main app stays generic. No `if provider == "codex"` anywhere. Anywhere codex specifics leak unavoidably → `// codex-compat:` comment.

### 1. New: `internal/llm/provider.go` (generic registry)

```go
type Provider interface {
    Auth(ctx context.Context, w io.Writer) error
    NewClient(ctx context.Context, model string) (Streamer, error)
    Models() []string
}

type Streamer interface {
    Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

var registry = map[string]Provider{}
func Register(name string, p Provider)
func Get(name string) (Provider, bool)
```

`*Client` (existing OpenAI client) already satisfies `Streamer`. No change to it.

### 2. New: `internal/providers/codex/register.go`

```go
func init() { llm.Register("codex", &provider{}) }
```

Codex self-registers. Main app never names it.

### 3. `cmd/shell3/main.go`

Side-effect import — only place `codex` literal appears in main app:
```go
import _ "github.com/weatherjean/shell3/internal/providers/codex" // codex-compat: registers OAuth provider
```

### 4. `cmd/shell3/auth.go`

Generic dispatch:
```go
// Try registered OAuth providers first; fall back to API-key prompt.
if p, ok := llm.Get(providerFlag); ok {
    return p.Auth(ctx, os.Stdout)
}
return config.RunAuthInteractive(homeDir, os.Stdin, os.Stdout)
```

Add `--provider` flag to `auth` command.

### 5. `cmd/shell3/run.go` (resolveConnection)

Generic dispatch:
```go
if p, ok := llm.Get(providerName); ok {
    return p.NewClient(ctx, modelName)  // OAuth path
}
return llm.NewClient(baseURL, apiKey, modelName)  // existing API-key path
```

### 6. No changes to:
- `internal/config/credentials.go` (schema untouched; codex tokens go in own file)
- `internal/chat/model_picker.go` (works through `Streamer` interface)
- `internal/llm/types.go` (stream events stay same shape)
- `internal/llm/client.go` (existing OpenAI client untouched)

### Adding next OAuth provider

Drop new package under `internal/providers/<name>/`, call `llm.Register("<name>", ...)` in init(), add side-effect import in main.go. Zero changes to dispatch code.

## Implementation Phases

### Phase 1: OAuth + Token Storage (~250 LOC)
- [ ] PKCE generator (S256)
- [ ] Browser callback server on localhost:1455
- [ ] Token exchange POST to `/oauth/token`
- [ ] JWT decode for `chatgpt_account_id` claim
- [ ] Token file read/write with 0600 perms
- [ ] Refresh-token grant on expiry
- [ ] `RunAuth()` entry point

**Verify:** `shell3 auth --provider=codex` opens browser, completes login, writes `~/.shell3/codex_tokens.json` with valid `access_token` and `account_id`.

### Phase 2: Responses API Client (~200 LOC)
- [ ] HTTP client with custom transport (sets Bearer + ChatGPT-Account-Id + originator + UA)
- [ ] Request body builder — convert shell3 internal messages → Responses API `input` array
- [ ] Tool definitions translation (`tools` field shape differs from chat/completions)
- [ ] SSE stream parser for Responses API events
- [ ] Map Responses events → `llm.StreamEvent` (text, tool_call, reasoning, done)

**Verify:** raw curl-equivalent test hitting `/backend-api/codex/responses` returns valid stream. Parse to internal events.

### Phase 3: LLMClient Interface (~150 LOC)
- [ ] `codex.Client` struct implementing `llm.LLMClient`
- [ ] `Stream(ctx, msgs, tools, onEvent)` — orchestrates token refresh + Responses call
- [ ] Tool call accumulation + dedup (mirror existing `internal/llm/client.go` logic)
- [ ] Error mapping (401 → re-auth prompt, 429 → rate-limit message)

**Verify:** `shell3 --provider=codex --model=gpt-5.1-codex` runs an interactive chat with tool calls end-to-end.

### Phase 4: Wiring + Polish (~50 LOC)
- [ ] Factory branch in `internal/llm/client.go`
- [ ] Auth branch in `cmd/shell3/auth.go`
- [ ] Model list integration with `/model` picker
- [ ] README section: how to use, ToS risk disclosure
- [ ] Error message when tokens missing → guides user to `shell3 auth --provider=codex`

**Verify:** existing API-key providers still work unchanged. Codex provider works in parallel.

## Out of Scope (Defer)

- Background token refresh goroutine (do lazy)
- Encrypted token storage (file perms 0600 sufficient for v1)
- Multiple Codex accounts (single account per shell3 install)
- Web search / image input (text + tools only for v1)
- Codex Cloud / async tasks integration
- Migration helper from API key → OAuth

## Open Questions

1. Responses API streaming format — fully documented? May need to capture and replay traffic to map event shapes.
2. Tool result format — Responses API uses `function_call_output` items. Verify mapping from shell3's tool-result messages.
3. Reasoning content — does Codex Responses API expose reasoning deltas? If yes, route to existing `reasoning_content` capture.

## Done When

- [ ] `shell3 auth --provider=codex` completes OAuth flow
- [ ] `shell3 --provider=codex --model=gpt-5.1-codex` runs chat with tool calls
- [ ] Token refresh works automatically across sessions
- [ ] Existing OpenAI / Anthropic / etc. providers unchanged
- [ ] README documents setup + risk
- [ ] Delete `internal/providers/codex/` + 5-line wiring = feature cleanly removed

## Effort Estimate

~650 LOC, 6-7 files, 2-3 days focused work.
