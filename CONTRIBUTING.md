# Contributing to shell3

shell3 is a small Go codebase. A few conventions keep it consistent.

## Development setup

Go (version pinned in `go.mod`) is the only requirement.

```sh
make build     # build ./shell3
make test      # go test -race ./...
make lint      # gofmt drift check + go vet + golangci-lint (what CI enforces)
make acceptance # deterministic config → wrk → runner smoke test
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
- shell3 is a local orchestrator with terminal and Telegram front ends, not an
  embeddable library — everything under `internal/` (including
  `internal/shell3`) may change freely.
- Tool failures use the typed `toolResult` path in `internal/chat`, classified in
  one place — don't introduce new string-sniffing.

## Architecture orientation

`AGENTS.md` and `docs/internals.md` have the package map. The short version:
`internal/lispconfig` parses inert configuration, `internal/orchestrator`
assembles the attached model, `internal/chat` runs turns, and
`internal/shell3` owns sessions and durable background commands. Bare shell3
and `internal/telegram` are local and remote adapters to that runtime;
`internal/wrk` dispatches typed external agent runners.

## Security

Never read, print, log, or commit credentials from the process environment or
host secret manager. shell3 is
not an operating-system sandbox: model-chosen commands run with the process's
real permissions. Use a container, VM, or restricted account for hard
isolation. Report vulnerabilities via GitHub Security Advisories.
