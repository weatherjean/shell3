# Contributing to shell3

shell3 is a small Go codebase. A few conventions keep it consistent.

## Development setup

Go (version pinned in `go.mod`) is the only requirement.

```sh
make build     # build ./shell3
make test      # go test -race ./...
make lint      # gofmt drift check + go vet (what CI enforces)
```

The test suite is hermetic: temp `HOME`, a fake LLM provider
(`internal/llm/fakellm`), no network or API keys.

## Scope

shell3 aims to stay small. Prefer sharpening, fixing, and simplifying what's
already here over adding new surface — changes that make the current pieces work
better are more welcome than ones that grow the footprint.

## Workflow

- Work on feature branches; `main` only takes fully tested changes.
- Keep PRs focused — one logical change each.
- CI must be green: `gofmt` clean, `go vet` clean, race-enabled tests passing on
  Linux and macOS. Run `make test` (not bare `go test`) so concurrency-sensitive
  code — the turn loop, `internal/shell3` session lifecycle, the openai adapter's
  body tap — stays race-clean.
- Add or update tests with behavior changes.

## Code style

- `gofmt` is law; `go vet` must pass.
- Doc comments explain **why**, not what. Write down any concurrency or lifecycle
  contract at the declaration (see `internal/chat/session.go`,
  `internal/shell3/session.go`).
- shell3 is a TUI-first product, not an embeddable library — everything under
  `internal/` (including `internal/shell3`) may change freely.
- Tool failures use the typed `toolResult` path in `internal/chat`, classified in
  one place — don't introduce new string-sniffing.

## Architecture orientation

`AGENTS.md` has the package map. The short version: `cmd/shell3` is the CLI,
`internal/agentsetup` assembles a `chat.Config` from `shell3.lua`
(`internal/luacfg`), `internal/chat` runs turns against an OpenAI-compatible
provider (`internal/adapter/openai`), and `internal/tui` (built on
`internal/shell3`'s session/runtime core) is the front-end.

## Security

Never read or commit credential files (`.env` beside `shell3.lua`). shell3 is
unsafe by default — model-chosen commands run with full shell access, gated only
by the optional `shell3.on_tool_call` hook. Report vulnerabilities via GitHub
Security Advisories.
