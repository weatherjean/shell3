# Contributing to shell3

Thanks for your interest! shell3 is a small codebase with a high bar for
clarity — most of these guidelines exist to keep it that way.

## Development setup

Go (version pinned in `go.mod`) is the only requirement.

```sh
make build     # build ./shell3
make test      # go test -race ./...
make lint      # gofmt drift check + go vet (what CI enforces)
```

The test suite is hermetic: it runs against a temp `HOME`, uses a fake LLM
provider (`internal/llm/fakellm`), and needs no network or API keys.

## Workflow

- Work on feature branches; `main` only takes fully tested changes.
- Keep PRs focused — one logical change per PR.
- CI must be green: `gofmt` clean, `go vet` clean, race-enabled tests passing
  on Linux and macOS.
- Add or update tests with behavior changes. Concurrency-sensitive code
  (the turn loop, `pkg/shell3` session lifecycle, the openai adapter's body
  tap) must stay race-clean — run `make test`, not bare `go test`.

## Code style

- Standard Go style; `gofmt` is law, `go vet` must pass.
- Doc comments explain **why**, not what. Where code has a concurrency or
  lifecycle contract (who closes a channel, what must be drained before
  what), the contract is written down at the declaration — follow the
  patterns in `internal/chat/session.go` and `pkg/shell3/shell3.go`.
- `pkg/shell3` is the only public package. Everything else lives under
  `internal/` and may change freely; think twice before widening the public
  surface.
- Tool failures use the typed `toolResult` path inside `internal/chat`;
  built-in handlers report in-band failures to the model as `"error: …"`
  strings (classified in exactly one place). Don't introduce new
  string-sniffing.

## Architecture orientation

`AGENTS.md` has the package map. The short version: `cmd/shell3` is the CLI,
`internal/agentsetup` assembles a `chat.Config` from `shell3.lua`
(`internal/luacfg`), `internal/chat` runs turns against an
OpenAI-compatible provider (`internal/adapter/openai`), and the TUI
(`internal/tui`, `internal/patch*`) plus the embeddable `pkg/shell3` are
front-ends over the same core.

## Security

Never read or commit credential files (`.env` beside `shell3.lua`). shell3 is
unsafe by default — model-chosen commands run with full shell access, gated only
by the optional `shell3.wrap_bash` hook. Report vulnerabilities via GitHub
Security Advisories.
