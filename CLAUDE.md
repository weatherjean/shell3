# shell3

Minimal Unix-composable coding agent written in Go.

## IMPORTANT: Do Not Read Credential Files

Files matching `ai-do-not-read.*` contain secrets and credentials. Never read, display, or include their contents in any response. This applies to all agents, assistants, and automated tools.

- `~/.shell3/ai-do-not-read.auth.yaml` — provider API keys and base URLs
- `~/.shell3/ai-do-not-read.secrets.yaml` — user-tool secrets

## Project Layout

```
cmd/shell3/          entry point + subcommands
internal/adapter/    LLM adapters (openai-compatible)
internal/chat/       conversation loop, tools, rendering
internal/config/     AuthStore (plain YAML)
internal/llm/        Provider/Streamer interfaces, registry
internal/persona/    persona loading + templating
internal/secrets/    secrets store (plain YAML)
internal/store/      SQLite memory/history
internal/paths/      global + local path resolution
```

## Development

```bash
make build      # go build ./cmd/shell3
make install    # go install ./cmd/shell3
go test ./...   # run all tests
```

Feature branches only. Never merge to `main` until fully tested and trace-audited.
