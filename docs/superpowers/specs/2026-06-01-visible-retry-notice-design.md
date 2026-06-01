# Visible Retry Notice — Design

**Date:** 2026-06-01
**Branch:** `feat/visible-retry-notice`

## Problem

When an LLM request fails transiently, the openai-go SDK already retries it
(default 2 retries, exponential backoff + jitter, honoring `Retry-After`). But
that retry loop is sealed: it sleeps and re-attempts internally with no
callback, so the retry is **invisible** to the user — it shows up only as a
longer pause before the first token, and nothing reaches the TUI.

We want retries to be **communicated to the TUI** as a dim scrollback notice,
and we want a more persistent default (5 retries instead of 2).

## Decisions

- **Display:** dim scrollback notice with reason + attempt count, e.g.
  `⟳ stream failed (HTTP 503), retrying (2/5)`. Persistent in the transcript.
  The busy line keeps showing the normal spinner + "thinking".
- **Policy:** keep the SDK as the retry engine (do **not** disable it). Bump
  `MaxRetries` to 5. Backoff, jitter, retryable-status set, and `Retry-After`
  handling stay exactly as the SDK defines them.
- **Mid-stream:** never retried. This is automatic — the SDK only retries
  getting the *initial* response; once a 200 starts streaming, a mid-stream
  break surfaces via `stream.Err()` and is not retried.

## SDK behavior we rely on

From `github.com/openai/openai-go@v1.12.0`,
`internal/requestconfig/requestconfig.go`:

- `shouldRetry` (L242): retries when there is no response (connection error),
  or `x-should-retry: true`, or status ∈ {408, 409, 429, ≥500}. Skips retry
  when the request body can't be rewound or `x-should-retry: false`.
- `retryDelay` (L359): honors `Retry-After-Ms` / `Retry-After` when 0–60s;
  else `0.5s · 2^attempt` capped at 8s, minus up to 25% jitter. With 5 retries
  the sleeps are ≈ 0.5/1/2/4/8s → ~15s worst case.
- Retry loop (L428): middleware (`option.WithMiddleware`) runs **once per
  attempt inside** the loop; the per-attempt request carries the
  `X-Stainless-Retry-Count` header (L441).

## Architecture

Keep the SDK as the retry engine. Add a thin, **per-`Stream()`-call** observer
middleware bound to that turn's `onEvent`. On each retryable failure it emits a
new `StreamEvent` variant, which rides the existing
`onEvent → Session.events → drainTurn` pipe and renders as a dim line. No
changes to the `Streamer` interface.

## Components

1. **`pkg/llm/types.go`** — add `Retry *RetryNotice` to `StreamEvent`; new
   `RetryNotice{Attempt, Max int; Reason string}`.

2. **`internal/adapter/openai/client.go`**
   - `const maxRetries = 5`.
   - `NewClient`: add `option.WithMaxRetries(maxRetries)`.
   - `Stream`: append `option.WithMiddleware(observer)` to the existing
     `extraOpts`. The observer reads `X-Stainless-Retry-Count`, calls `next`,
     and if the result is a retryable failure **and** retries remain
     (`rc+1 <= maxRetries`), calls
     `onEvent(StreamEvent{Retry: &RetryNotice{Attempt: rc+1, Max: maxRetries,
     Reason: retryReason(res, err)}})`.
   - Helpers: `retryReason(res, err) string` ("connection error: …" or
     "HTTP 503"); `isRetryable(res, err) bool` (mirrors `shouldRetry` crudely:
     err/no-response → true; status ∈ {408,409,429,≥500} → true; else false).

3. **`pkg/chat/turn.go`** — in `streamOnce`'s callback,
   `if ev.Retry != nil { emitRetry(sess, ev.Retry) }`.

4. **`pkg/chat/event.go`** — `EventRetry` kind + `emitRetry(s, *llm.RetryNotice)`
   that formats `stream failed (HTTP 503), retrying (2/5)` into `Event.Text`.

5. **`internal/tui/interactive.go`** — `case chat.EventRetry:` renders the text
   dim with a `⟳` prefix. Does **not** touch busy state (spinner keeps showing
   "thinking").

## Data flow

failed attempt → SDK middleware observer → `onEvent` → `streamOnce` →
`emitRetry` → `Session.events` → `drainTurn` → dim scrollback line. The SDK then
sleeps and retries on its own.

## Error handling / edges

- **Final failure** (retries exhausted): the observer suppresses the notice on
  the last attempt, so we don't print "retrying" right before the turn errors
  out. The existing `EventError` path handles the give-up.
- **Mid-stream breaks:** excluded automatically.
- **Non-retryable 4xx** (400/401/etc.): no notice; surfaces as error
  immediately — unchanged behavior.

## Testing (TDD)

- **`pkg/chat`:** fake `LLMClient` emits `StreamEvent{Retry}` → assert an
  `EventRetry` with the expected text lands on `Session.events`.
- **openai adapter:** unit-test `isRetryable` / `retryReason` over synthetic
  res/err; test the observer closure directly with a fake `next` returning a
  503 → assert it fires `onEvent` once with the right `Attempt/Max/Reason`. No
  network required.
