// Package codex provides a shell3 LLM provider backed by ChatGPT subscription
// auth ("Sign in with ChatGPT"). Users with a ChatGPT plan can run shell3
// against Codex models without per-token API billing.
//
// Risk: OpenAI's Terms of Service do not explicitly bless third-party clients
// using ChatGPT subscription auth. The flow is currently tolerated. Account-
// suspension risk is real — other providers have banned analogous flows.
// Users opt in by running `shell3 auth --provider=codex`.
//
// Tokens are stored in ~/.shell3/codex_tokens.json (mode 0600), separate from
// the existing credentials.yaml schema.
package codex
