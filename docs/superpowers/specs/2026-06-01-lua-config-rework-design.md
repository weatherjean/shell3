# shell3 Lua Config Rework — Design (Part 1)

**Date:** 2026-06-01
**Status:** Approved design, pre-implementation
**Scope:** Part 1 of the seasnail convergence — replace shell3's configuration
surface with a strict, single-file Lua config mirroring seasnail. **No
runtime/topology changes** (cron, daemon, attach, SMTP are Part 2+ and explicitly
out of scope here).

---

## 1. Goal

Make shell3's configuration **Lua-based and solid**, mirroring seasnail's
concise, strict, single-file approach. Today's config is spread across YAML auth,
YAML secrets, markdown personas with Go-template bodies, YAML user-tools, shell
hook scripts, and markdown skill files. This collapses to **one `shell3.lua`
file + one `.env`**, parsed strictly (unknown keys fail load).

This is a **lateral move**: same coding-agent behavior, new config substrate. The
interactive TUI experience does not change.

## 2. Guiding principles

- **Clean lateral move — no stale code.** Every YAML/markdown/adapter path that
  Lua replaces is **deleted in the same change**. No parallel old config system,
  no dead loaders, no compatibility shims, no migration fallback reading old
  files. When the branch lands, the only config path is Lua.
- **Strict Lua only.** Unknown keys in any block fail load with a clear
  `<ctx>: unknown key "<name>"`. Typos surface immediately. No silent ignores
  (today's `reasoning_summary`/`verbosity` silent-drop is exactly the bug we
  kill).
- **One file = one agent.** `shell3.lua` declares exactly one `shell3.agent`.
  Multiple agents = multiple files = multiple processes. No channels, no
  multi-agent, no persona registry.
- **Suckless surface.** Few knobs, sensible auto-derivation, inline definitions.
- **Mirror seasnail.** Same loader architecture (`gopher-lua`, global table +
  `DoFile`, handle-sentinel pattern, strict-key tests, split binding files), same
  `shell3.http`/`shell3.bash`/`shell3.urlencode`/`shell3.env.secret` bindings.

## 3. The Lua config surface

Global table is `shell3`. The file is evaluated once; top-level calls register
config; `shell3.tool{}` and `shell3.skill{}` return opaque **handles** stored in
locals and referenced from the agent block.

### 3.1 `shell3.env.secret("KEY")`
Reads `KEY` from `<workdir>/.env` (autoloaded `KEY=value` lines). Missing key =
load-time error. The set of `.env` values doubles as the **redaction set**:
values appearing in model/tool output are stripped before display. Replaces both
`ai-do-not-read.secrets.yaml` **and** inline `api_key` in the old auth YAML.

### 3.2 `shell3.model(name, opts)`
Defines one named OpenAI-compatible endpoint. A backend is selected by
`base_url`; there is **no `type` discriminator** (the Anthropic adapter is
deleted — see §7). All request tuning lives here, not on the agent.

Required: `base_url`, `api_key`, `model`.
Optional first-class: `context_window`, `reasoning`
(`none|minimal|low|medium|high|xhigh`), `max_tokens`, `temperature`.
Optional opaque: `extra` (table merged verbatim into the request JSON — this is
where `reasoning_summary`, `verbosity`, `parallel_tool_calls`, and any
provider-specific fields live).

`extra` is the one place where unknown keys are allowed (opaque pass-through);
every other block is strict.

### 3.3 `shell3.tool({...})` — inline, Lua handler
Defines a custom tool; returns a handle. Required: `name`, `description`,
`parameters` (JSON-schema-shaped table), `handler` (`function(args) -> string`).
The handler runs under the Lua VM mutex; the IO bindings (§4) release the mutex
around blocking work. The returned string is what the model sees.

User-tool YAML (`enabled`, `secrets[]`, `command`, `timeout`) is gone: a tool
exists iff it is defined; secrets are fetched in-handler via
`shell3.env.secret`; shelling out is `shell3.bash` with a `timeout` opt.

### 3.4 `shell3.skill({name, description, body})` — inline
Defines a skill; returns a handle. All three fields required; `name` is
kebab-case. Bodies are inline verbatim markdown (no external skill files). Per
turn, each listed skill's `(name, description)` is injected as a one-line index;
the `skill` tool (auto-enabled when the agent lists ≥1 skill) returns a body by
name on demand.

### 3.5 `on_tool_call` middleware chain + `shell3.guards.*`
`on_tool_call` is a **list of middlewares run in order**. Each receives the
pending call and returns an action; the first non-`allow` short-circuits.

```
call   = { tool = <string>, params = <table> }
action = { action = "allow" | "block" | "cancel", reason = <string?> }
```

`allow` (or `nil`) falls through to the next middleware; `block` denies this call
only (model may retry differently); `cancel` aborts the turn. A middleware is
either a Lua `function(call)` or a built-in guard constructor from
`shell3.guards.*`. The dangerous-command denylist (today's ~180-line
`confirm-bash.sh`) ships as **`shell3.guards.confirm_dangerous{ prompt = bool }`**
— a built-in, not a user-maintained script. This is the **only** surviving
lifecycle hook; the other seven (`on_session_start/end`, `on_turn_start/end`,
`on_tool_result`, `on_context_build`, `on_error`) are removed.

### 3.6 `shell3.agent({...})`
Declares the single agent. Identity + wiring only (tuning is on the model).

- `name` — string.
- `model` — name of a `shell3.model`.
- `prompt` — **verbatim** system prompt. No Go-template vars. The engine appends
  standard system blocks (see §6).
- `tools` — per-tool boolean gating + `custom` handle list:
  `bash`, `bash_bg`, `shell_interactive`, `edit`, `memory`, `history`, `docs`,
  `custom = { handle, ... }`. (`prune_tool_result`, `compact_history` are always
  on.)
- `skills` — list of skill handles.
- `on_tool_call` — middleware chain (§3.5).
- `db` — **auto-derived** (`~/.shell3/projects/<uuid>/shell3.db` via the existing
  `.ref` UUID); no field unless we find a real need to override.

### 3.7 Strict-load contract
Every block validates its keys against a known set; unknown key → load error
naming the key and context. `extra` (§3.2) is the sole opaque exception.
Mirrors seasnail's `strict_keys_structural_test.go` / `opt_discipline_test.go`.

## 4. Handler-time bindings (mirror seasnail)

Available inside `handler = function(args)`, each releasing the VM mutex around
blocking IO so concurrent tool calls proceed:

- `shell3.http.request{ url, method, headers, body, timeout, max_bytes }`
  → `(result, nil) | (nil, "error: ...")`; `result = { status, body, truncated,
  headers }`. Defaults: `GET`, 30s (cap 120s), 1 MiB (cap 16 MiB).
- `shell3.http.get(url [, opts])`, `shell3.http.post(url [, opts])` — shorthands.
- `shell3.bash(cmd [, { timeout = N }])` → `{ exit, stdout, stderr }`; default
  10s, cap 600s; child killed on timeout; inherits process env (no sandbox —
  containment is the redaction layer per §3.1).
- `shell3.urlencode(s)` → string.
- `shell3.env.secret("KEY")` — also valid at handler time.

## 5. Built-in tool gating

Twelve built-ins map to booleans on `shell3.agent.tools`:

| Tool(s) | Gate |
|---|---|
| `bash` | `bash` |
| `bash_bg` | `bash_bg` |
| `shell_interactive` | `shell_interactive` (no-op headless; matters Part 2) |
| `edit_file` | `edit` |
| `memory_upsert` / `memory_list` / `memory_search` | `memory` |
| `history_get` / `history_search` | `history` |
| `shell3_docs` | `docs` |
| `prune_tool_result` / `compact_history` | always on |

`skill` tool auto-enables when `skills` is non-empty. Replaces today's
`no_bash`/`no_memory` bools + name-based `tools[]` allowlist.

## 6. Prompt model

The `prompt` is verbatim text. The engine appends standard system blocks around
it (seasnail-style), replacing today's Go `text/template` rendering:

- current time, working directory
- core memories (when present)
- skill index (when skills listed)
- available tools

This removes `text/template` from the config path and the `{{.CWD}}`,
`{{.Time}}`, `{{.Model}}`, `{{.CoreMemories}}`, `{{.Skills}}`, `{{.UserTools}}`,
`{{.AvailableModels}}` template variables.

## 7. Architecture & components

New/changed Go:

- **`internal/config`** — replace `authstore.go` + `config.go` with a Lua loader
  modeled on seasnail's `internal/config/load.go`:
  - `Load(path string, envSecrets map[string]string) (*Config, error)`:
    register `shell3` global table, `DoFile`, read resulting tables, validate
    strictly.
  - helper set: `optString`/`optInt`/`optFloatPtr`, strict-key checker,
    `parseToolsStruct`, `parseSkillsList`, handle-sentinel `extractHandleNames`,
    `tableToMap`/`luaToGo`.
  - split binding files: `lua_http.go`, `lua_bash.go`, `lua_bindings.go`,
    `lua_guards.go`.
- **`.env` loader** — autoload `<workdir>/.env`; build the redaction set.
- **Bridge to the engine** — `*Config` produces what `pkg/chat`/`pkg/shell3`
  consume today (`chat.Config`, `persona.Persona`-equivalent, tool defs,
  `llm.RequestParams`, guard middleware). The `pkg/chat` session engine is
  unchanged; only how Config is built changes.
- **Dependency:** add `github.com/yuin/gopher-lua`.

Workdir model: `shell3 <file.lua>` → `<dir-of-file>` is the workdir; `.env` and
runtime state resolve relative to it (consistent with seasnail `--here`). Exact
CLI surface finalized in the plan.

## 8. Cleanup ledger (deleted in this branch)

- `internal/config/authstore.go`, the YAML auth file + its schema.
- `internal/secrets/` + `ai-do-not-read.secrets.yaml` handling (→ `.env`).
- `pkg/persona` markdown frontmatter parsing + `text/template` rendering (replaced
  by Lua agent block + injected blocks). Persona *concept* survives as the agent.
- `internal/adapter/anthropic` + the `type` discriminator (Anthropic via
  OpenAI-compat returns in **v0.2**).
- The **seagull** persona, the Telegram gateway, and any seagull-only code paths.
- Seven lifecycle hook events; `confirm-bash.sh` (→ `shell3.guards`).
- YAML user-tool loader (`internal/usertools` YAML path) + `tools/*.yaml`,
  `personas/*.md`, `skills/*.md`, `hooks/*.sh` as config inputs.
- Dead param keys (`reasoning_summary`, `verbosity`) — now real via `extra`.

Each deletion lands with its replacement, not after.

## 9. Migration (one-time, manual)

Old `~/.shell3` → new inputs:

- `ai-do-not-read.auth.yaml` instances/models → `shell3.model(...)` calls;
  `api_key` → `.env` + `shell3.env.secret`.
- `ai-do-not-read.secrets.yaml` → `.env`.
- `personas/base.md` → `shell3.agent{}` + prompt body (minus `{{...}}`).
- `tools/*.yaml` → `shell3.tool{}` Lua handlers.
- `skills/*.md` → inline `shell3.skill{}` bodies.
- `hooks/confirm-bash.sh` → `shell3.guards.confirm_dangerous{}`.

A worked `shell3.lua` for the current setup is the example developed during
brainstorming; it doubles as the migration target and the `shell3-example.lua`
shipped in-repo. No automatic converter (lateral move, not a long-lived dual
format).

## 10. Testing strategy

- **Strict-key tests** — every block rejects an unknown key with the right
  message (structural, table-driven; mirror seasnail).
- **Loader unit tests** — each block parses to the right Go struct; `extra`
  passes through opaque; missing required key fails.
- **Binding tests** — `shell3.http` (against a test server), `shell3.bash`
  (exit/stdout/stderr/timeout), `shell3.urlencode`, `shell3.env.secret`
  (hit/miss + redaction).
- **Guard middleware** — chain ordering, short-circuit on first non-allow,
  `confirm_dangerous` denylist matches.
- **Golden parse** — the shipped `shell3-example.lua` loads clean.
- **Engine integration** — a loaded `*Config` drives a `pkg/chat` turn (with
  `fakellm`) producing the expected tool set + params.
- **Cleanup gate** — grep/build proves the deleted packages are gone and nothing
  imports them (no stale references compile).

## 11. Out of scope (Part 2+)

Cron / `--run-cron` / `cron.json`, the daemon-free autonomous mode, SMTP-out,
the Anthropic adapter (v0.2), hot-reload/file-watching, any new transport
(IRC/web/SSH-server). This spec is config substrate only.

## 12. Open questions for the plan

- Exact CLI surface: `shell3 <file.lua>` vs `shell3 run <file.lua>` vs a
  default-path lookup; workdir resolution rules.
- Does `db` override stay as an escape hatch, or is auto-derivation total?
- `compact_at_tokens` (seasnail auto-compaction) — adopt now or leave shell3's
  manual `compact_history` as-is for Part 1? (Leaning: leave as-is.)
- Whether `prune_tool_result`/`compact_history` ever need a gate (Leaning: no).
