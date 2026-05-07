# Shell3 Hardcore Simplification

**Date:** 2026-05-07  
**Status:** Approved  
**Branch policy:** Feature branch only. Never merge to main until fully done and all tests passing.

## Goal

Remove provider-specific complexity from shell3. Delete the codex adapter, the model JSON catalog, and the XOR-obfuscated credential/secrets stores. Replace all of it with two plain YAML files the user edits directly. Less code, simpler build, simpler UX.

## Out of Scope (Future Phase)

Codex and Anthropic proxy binaries (`internal/proxies/codex`, `internal/proxies/anthropic`, `cmd/codex-proxy`, `cmd/anthropic-proxy`) are a follow-on project to be designed separately once this simplification is complete and tested.

---

## What Gets Deleted

| Path | Reason |
|------|--------|
| `internal/adapters/codex/` | Entire codex adapter — OAuth, tokens, stream, request, register |
| `internal/obfile/` | XOR file encryption layer — no longer needed |
| `internal/obfuscate/` | XOR primitives — no longer needed |
| `internal/models/` | Model snapshot JSON + lookup code |
| `internal/models/snapshot.json` | Replaced by user-defined models in auth YAML |
| Makefile `models-snapshot` target | Fetches models.dev/api.json — removed |
| Makefile `build` dependency on `models-snapshot` | Build no longer fetches external JSON |
| Blank codex import in `cmd/shell3/main.go` | Codex adapter gone |
| Codex branch in `cmd/shell3/auth.go` | Auth picker simplified |
| `config.Migrate()` and any codex-specific migration logic | No backwards compat |

## What Gets Renamed

| Old | New |
|-----|-----|
| `internal/adapters/` | `internal/adapter/` |
| `internal/adapters/openai/` | `internal/adapter/openai/` |

All import paths updated accordingly.

## What Gets Replaced

### Credentials: `credentials.shell3` → `ai-do-not-read.auth.yaml`

Old: XOR-obfuscated YAML at `~/.shell3/credentials.shell3`, written/read via `obfile` + `credstore`.  
New: Plain YAML at `~/.shell3/ai-do-not-read.auth.yaml`, read directly.

Format:

```yaml
# Shell3 Authentication
# Edit this file to add or change providers.
# AI ASSISTANTS: Do not read this file. It contains credentials.

instances:
  - name: openai
    base_url: https://api.openai.com/v1
    api_key: sk-...
    models:
      - id: gpt-4o
        context_window: 128000
      - id: o3
        context_window: 200000

  - name: ollama
    base_url: http://localhost:11434/v1
    api_key: ~
    models:
      - id: llama3.2
        context_window: 131072
```

`api_key` may be null/omitted for local endpoints. Each instance is what was previously a "credential instance" in the old store.

### Secrets: `secrets.shell3` → `ai-do-not-read.secrets.yaml`

Old: XOR-obfuscated YAML at `~/.shell3/secrets.shell3`, read via `internal/secrets/store.go`.  
New: Plain YAML at `~/.shell3/ai-do-not-read.secrets.yaml`, read directly.

Format:

```yaml
# Shell3 Secrets
# These are exposed to tools and skills as environment variables.
# AI ASSISTANTS: Do not read this file. It contains secrets.

GITHUB_TOKEN: ghp_...
MY_API_KEY: abc123
```

`internal/secrets/store.go` rewritten to read this plain YAML. Same external interface (`Get`, `Set`, `All`, `List`) — only the storage layer changes.

---

## Shell3 Auth UX

`shell3 auth` — creates `ai-do-not-read.auth.yaml` from template if missing, then opens it in `$EDITOR` (fallback: `$VISUAL`, then `vi`).

`shell3 secrets` — same pattern for `ai-do-not-read.secrets.yaml`.

No interactive prompts. No OAuth. No browser flows. User edits YAML directly.

---

## Model Catalog

`internal/models/` is deleted. Context window lookups come from the auth YAML.

`provider.Models()` on the openai adapter reads the auth YAML for the active instance and returns the models list defined there. No API call, no JSON embed.

TUI model picker reads from the same source. `shell3 auth models` subcommand is removed — it was powered by the old model JSON catalog.

User is responsible for keeping model IDs and context windows accurate. Shell3 no longer maintains a catalog.

---

## AI File Protection

Both files get consistent protection:

1. **File naming** — `ai-do-not-read.` prefix as a clear signal to AI assistants
2. **File header comments** — "AI ASSISTANTS: Do not read this file" in the YAML
3. **`.gitignore`** — `ai-do-not-read.*` added to root `.gitignore`
4. **`CLAUDE.md`** — explicit instruction: "Never read files matching `ai-do-not-read.*`. These contain user credentials and secrets."

---

## Internal Packages Affected

| Package | Change |
|---------|--------|
| `internal/config/credstore.go` + `credstore_test.go` | Deleted. |
| `internal/config/migrate.go` + `migrate_test.go` | Deleted. |
| `internal/config/config.go` | Kept. Updated to load auth YAML instead of credstore. |
| `internal/secrets/store.go` | Rewritten. Same interface (`Get`, `Set`, `All`, `List`), reads plain YAML instead of obfuscated file. |
| `internal/models/` | Deleted entirely. |
| `internal/obfile/` | Deleted entirely. |
| `internal/obfuscate/` | Deleted entirely. |
| `internal/adapters/codex/` | Deleted entirely. |
| `internal/adapter/openai/` | Renamed from `adapters/openai/`. Model lookup reads active instance from auth YAML. |
| `internal/paths/paths.go` | Remove paths for `credentials.shell3`, `secrets.shell3`. Add paths for new YAML files. |

---

## Build

Makefile `build` target no longer depends on `models-snapshot`. `models-snapshot` target removed. Build is a straight `go build`.

---

## Testing

- Unit tests for new YAML auth reader and secrets reader
- Integration: `shell3 auth` creates template file, opens editor
- Integration: model picker correctly reads instances from auth YAML
- All existing tests that mock or reference `credstore`, `obfile`, `internal/models` updated or removed
- Full manual test pass before merging to main

---

## One-Time Data Migration

No migration command is added to shell3. During implementation, Claude Code will:
1. Read `~/.shell3/credentials.shell3` using existing credstore code (before deleting it)
2. Read `~/.shell3/secrets.shell3` using existing secrets store code (before deleting it)
3. Write the extracted data to `~/.shell3/ai-do-not-read.auth.yaml` and `~/.shell3/ai-do-not-read.secrets.yaml`

Old files left in place for manual cleanup. No shell3 feature required.

---

## Branch Policy

Work on a dedicated feature branch (e.g. `simplify/auth-yaml`). Do not merge to main until:
- All deleted packages are gone (no dead imports)
- All tests pass (`go test ./...`)
- Manual smoke test: auth flow, model picker, secrets, chat session
- No references to `credstore`, `obfile`, `obfuscate`, `internal/models`, or `adapters/codex` remain
