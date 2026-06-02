# Design: Sync shell3 docs + scaffolding to the Lua-config era

**Date:** 2026-06-02
**Branch:** `docs/lua-config-sync`
**Status:** Approved (scope), pending spec review

## Problem

shell3's configuration system was reworked (commits `b38feff` → `2c56089`, ~June 1) from a
multi-file YAML/markdown/JSON system into a **single Lua config file** (`shell3.lua`, parsed by
`internal/luacfg`). The old packages (`internal/config`, `internal/secrets`, `pkg/hooks`,
`internal/adapter/anthropic`) and the `auth`/`secrets` subcommands and the
`--persona`/`--provider`/`--model`/`--no-bash`/`--no-memory-tools` flags were **deleted**.

The documentation and scaffolding never followed. They still describe the deleted world, so:

- `shell3 docs` (and the `shell3_docs` tool) print instructions that no longer work.
- First run scaffolds dead YAML files into `~/.shell3/` and writes **no** usable config.
- `README.md` and `CLAUDE.md` reference deleted commands, flags, and packages.
- `examples/` duplicates the old YAML system.

## Source of truth

The current system, verified against code:

- **Config discovery** (`cmd/shell3/run.go`): `--config/-c` → `./shell3.lua` → `~/.shell3/shell3.lua`.
  Error when none found: *"no shell3.lua found — pass --config or create ~/.shell3/shell3.lua"*.
  The Lua workdir (for `.env` and relative paths) is the config file's directory.
- **CLI flags** (root `shell3`): only `--config/-c` and `--out` (`--out` enables headless JSONL).
- **Subcommands** (`cmd/shell3/main.go`): `doctor`, `docs`, `widget`. No `auth`, no `secrets`.
- **Adapter**: OpenAI-compatible only. Anthropic-native is gone.
- **Secrets**: a `.env` file beside the config, read via `shell3.env.secret("KEY")` (empty string if absent).
- **Canonical example**: `shell3-example.lua` (repo root, covered by a golden-parse test).

### Lua constructor API (exact strict keys, from `internal/luacfg/register.go`)

- `shell3.model(name, { base_url, api_key, model, context_window, reasoning, max_tokens, temperature, extra })`
  — `base_url`, `api_key`, `model` required; `extra` is a free map for vendor extensions
  (e.g. `reasoning_summary`, `verbosity`, `parallel_tool_calls`).
- `shell3.skill({ name, description, body })` — all three required; returns an opaque handle.
- `shell3.tool({ name, description, parameters, handler })` — `handler` required (Lua fn, must
  return a string); `parameters` is a JSON-Schema table (`type="object"`); returns an opaque handle.
- `shell3.agent({ name, model, prompt, tools, skills, on_tool_call })`
  - `tools` gate table (strict keys): `bash, bash_bg, shell_interactive, edit, memory, history,
    docs, custom, skill`. `custom` is a list of tool handles. `skill = false` suppresses the skill
    tool **and** the skill index in the system prompt.
  - `skills` is a list of skill handles.
  - `on_tool_call` is a guard chain: Lua functions `(call) -> { action = "allow"|"block", reason }`
    and/or built-in guard handles (e.g. `shell3.guards.confirm_dangerous{ prompt = true }`).
- Handler helpers (registered on the `shell3` table): `shell3.env.secret`, `shell3.bash`,
  `shell3.http.request/get/post`, `shell3.urlencode`, and `shell3.guards.*`.
  *(Exact signatures of `bash`/`http`/guards to be confirmed against `lua_bash.go`,
  `lua_http.go`, `guards.go` during implementation.)*

### Always-on vs gated tools

- Always on: `prune_tool_result`, `compact_history`.
- Skill tool: present when the agent has ≥1 skill and `tools.skill ≠ false`.
- Gated built-ins (via the `tools` table): `bash`, `bash_bg` (background), `shell_interactive`,
  `edit_file`, `memory_*`, `history_*`, `shell3_docs`.
- Custom tools: defined with `shell3.tool()`, allow-listed in `tools.custom`.

### System prompt assembly (`internal/luacfg/persona.go`)

`BuildPersona` concatenates: the agent's verbatim `prompt`, then auto-injected `## Environment`
(workdir, model, time), `## Core memories` (from SQLite when memory is enabled), and `## Skills`
(index of available skills). No Go-template variables — the prompt is literal text.

## Work items (full deep pass — nothing dropped)

### 1. Rewrite `cmd/shell3/shell3.md` (the `shell3 docs` / `shell3_docs` output)

Full rewrite to the Lua model. Sections:

1. Intro — minimal Unix-composable agent; OpenAI-compatible providers (drop Anthropic-native).
2. Config — discovery order, `--config`, the config dir as workdir, `.env` + `shell3.env.secret`.
3. The four constructors with their real strict keys and short examples (mirroring `shell3.lua`).
4. Tools — always-on list, the gate table, custom tools, the skill tool.
5. Guards — `shell3.guards.confirm_dangerous` (note `prompt` is currently reserved/no-op: a matched
   dangerous command is always blocked) and custom `on_tool_call` functions returning `{action, reason}`.
6. Handler helpers — `shell3.bash`, `shell3.http.*`, `shell3.urlencode`, `shell3.env.secret`.
7. System prompt assembly — Environment / Core memories / Skills injection.
8. Models — reasoning levels (`none|minimal|low|medium|high|xhigh`), `extra` map.
9. Slash commands — table verified against `internal/tui` registry (do not copy the old table blind).
10. Commands — `doctor`, `docs`, `widget` only.
11. On-disk layout — `~/.shell3/shell3.lua`, `projects/<uuid>/{shell3.db,meta.json}`,
    `.env`, `./.shell3/.ref`, `./.shell3/bg.json`.
12. Headless — `--out` JSONL (link `docs/headless.md`).
13. Moving config to a new machine — just `shell3.lua` + `.env`; `projects/` is machine-local.

**Remove entirely:** YAML personas, `auth`/`secrets` subcommands, hook-YAML protocol, user-tool
YAML schema, `--persona`/`--provider`/`--model`/`--no-bash`/`--no-memory-tools` flags,
Anthropic-native provider rows, `meta.json`/`last_error.json` references that no longer hold.

### 2. Scaffolding behavior fix (`internal/scaffold` + `internal/bootstrap`)

Current bug: `bootstrap.EnsureGlobal` mkdirs `~/.shell3/{skills,tools,hooks,personas}` and calls
`scaffold.WriteDefaults`, which writes old-world YAML/markdown that the Lua loader never reads —
and never writes a `shell3.lua`.

New behavior:

- `internal/scaffold/defaults/` becomes: `shell3.lua` (canonical, see item 3) and `env.example`.
- `scaffold.WriteDefaults(...)` → replaced by `WriteStarterConfig(configPath, envExamplePath string)`
  that writes each file only if absent (idempotent, safe every run).
- `bootstrap.EnsureGlobal`: stop creating the vestigial `skills/tools/hooks/personas` dirs; write
  `~/.shell3/shell3.lua` + `~/.shell3/.env.example` (if absent); keep `projects/` + the global
  `.gitignore` (which already ignores `ai-do-not-read.*`, `shell3.log*`, `projects/`).
- `internal/bootstrap.EnsureProject`: stop creating vestigial `.shell3/{skills,tools,hooks,personas}`
  dirs; keep `.shell3/`, `.ref`, project `.gitignore`.
- `cmd/shell3/doctor.go`: drop the "global skills dir" check (it already validates `shell3.lua`
  parses, model count, agent name).
- `internal/paths`: remove the now-unused `Global.{Skills,Tools,Hooks,Personas}` and
  `Local.{Skills,Tools,Hooks,Personas}` fields — **only after grep-verifying no remaining
  references** (luacfg, tests, etc.). If any live reference exists, keep the field and note why.

### 3. Single-source the canonical example

`go:embed` cannot reach the repo-root file from `internal/scaffold`, so the runtime starter must
live inside the package. **Approved:** move canonical content to
`internal/scaffold/defaults/shell3.lua`, delete the root `shell3-example.lua`, and repoint:

- the golden-parse test (find via `grep -rn shell3-example.lua`) to the new path,
- `README.md` and `cmd/shell3/shell3.md` references,
- the `## shell3 self-configuration` note in the embedded agent prompt (it currently points readers
  at `cmd/shell3/shell3.md`, which stays valid).

Also add `internal/scaffold/defaults/env.example` (the `.env` template the config references —
`OPENCODE_KEY`, `BRAVE_API_KEY`). The current `shell3-example.lua` line 3 references a
`shell3-example.env.example` that does not exist; this fixes that gap.

### 4. `examples/` consolidation

- Delete `examples/tools/`, `examples/skills/`, `examples/hooks/`, and `examples/README.md`
  (all old YAML / old skills, now superseded by the single Lua config).
- `examples/webui/main.go`: verify it still compiles against current `pkg/shell3`. If it does, keep
  (optionally add a one-line README pointing at the canonical config); if it does not, update it to
  the current embedding API. Do not silently delete a working example.

### 5. `README.md`

Replace the quick-start (`shell3 auth`, auth.yaml, "Anthropic natively") with the Lua + `.env`
flow: `make build`, copy the example config to `~/.shell3/shell3.lua` (or rely on first-run
scaffold), create `.env`, run `shell3`. Fix the "Removing a project's data" section if needed and
link the canonical example + `shell3 docs`.

### 6. `CLAUDE.md`

Fix the **Project Layout** block: drop `internal/config` and `internal/secrets`; add `internal/luacfg`,
`internal/bootstrap`, `internal/scaffold`, and the current `pkg/` layout (`pkg/chat`, `pkg/llm`,
`pkg/persona`, `pkg/shell3`, `pkg/applog`). Keep the "do not read `ai-do-not-read.*`" section
(those files still exist as the `.env`-adjacent credential convention referenced in README).

### 7. `docs/headless.md` + misc

- Scrub `docs/headless.md` for removed flags (`--persona` etc.) and the old `confirm-bash` hook;
  align with `--out`, `SHELL3_HEADLESS*` env, and `shell3.guards`.
- Verify `docs/refactor-smoke.md` for stale references; update only if it describes config.

## Non-goals

- `docs/superpowers/plans/*` and `docs/superpowers/specs/*` (except this file) are point-in-time
  historical records — **left untouched**.
- No behavior changes to `internal/luacfg`, `internal/chat`/`pkg/chat`, or the TUI. The only code
  changes are scaffolding/bootstrap/doctor/paths cleanup required by items 2–3.

## Validation

- `go build ./...` and `go test ./...` pass (golden-parse, bootstrap, doctor tests).
- Manual read-through of `shell3 docs` output for accuracy against `shell3.lua`.
- Fresh-bootstrap check: with a temp `HOME`, run `shell3` once → `~/.shell3/shell3.lua` +
  `.env.example` written, vestigial dirs absent, `shell3 doctor` passes.
- `grep -rn` sweep for residual stale terms: `auth.yaml`, `secrets.yaml`, `--persona`,
  `--provider`, `personas/`, `anthropic` (in user-facing docs), to catch anything missed.

## Risks

- Removing `paths.Global/Local` fields could break an unseen consumer — mitigated by the grep-verify
  gate before deletion (keep the field if still referenced).
- `examples/webui/main.go` may already be broken against current `pkg/shell3` — handled by the
  compile check in item 4.
