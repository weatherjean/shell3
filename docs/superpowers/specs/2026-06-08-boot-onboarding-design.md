# shell3 onboarding: explicit `boot`, split-file base config, cookbook

- **Date:** 2026-06-08
- **Status:** Design — awaiting review
- **Topic:** Replace auto-bootstrap of `shell3.lua` with an explicit, interactive `shell3 boot` command; ship a clean split-file base config; preserve removed features in a repo cookbook.

---

## 1. Problem

Today, on **every** run, `agentsetup.Build → resolvePaths` calls `bootstrap.EnsureGlobal`, which silently writes a 712-line reference `shell3.lua` + `.env.example` into `~/.shell3/` the first time. The user never asked for it, never supplied credentials, and is dropped into a large config they must reverse-engineer. There is no coherent "here is how you start" moment.

We want onboarding to be **explicit and guided**:

1. No config → **fail** with a clear redirect, instead of conjuring one.
2. A dedicated `shell3 boot` command that asks the few things needed to make a working agent (url, model tag, name, key), explains the proxy, and tells the user how to edit/extend.
3. A base config that is **genuinely nice to read** — small main file, pieces split into modules.
4. Nothing valuable is lost: features the new base config omits (MCP, extra skills, more guards, proxy recipes) live in a repo **cookbook** as drop-in modules.

## 2. Goals / non-goals

**Goals**
- Remove auto-write of `shell3.lua`/`.env.example`.
- Add `shell3 boot`: interactive prompts (+ optional flags), writes a working split-file config + `.env` to `~/.shell3/`.
- Two **dialed** agents: `code` (full tools) and `plan` (read-only, brainstorming-oriented).
- Curated tooling: `web_fetch` + `brave_search` example tools; `spawning-subagents` skill on `code`; a new `brainstorming` skill (ported from Claude's) on `plan`.
- A `docs/cookbook/` of copy-in recipes for everything left out.
- A real from-zero manual test path.

**Non-goals**
- No change to `EnsureProject` / project state.
- No change to the Lua API surface (`shell3.model/agent/tool/skill/mcp/env.secret`), config resolution precedence, or the loader's `require` mechanism.
- No GUI/TUI wizard — `boot` is plain stdin prompts.

---

## 3. Part A — Remove auto-bootstrap, fail with redirect

**`internal/bootstrap/bootstrap.go` — `EnsureGlobal`:**
- Keep: create `~/.shell3/` and `~/.shell3/projects/`, create/maintain `~/.shell3/.gitignore`.
- Remove: the `scaffold.WriteStarterConfig(...)` call (no more auto `shell3.lua` / `.env.example`).

**`internal/agentsetup` — `ResolveConfigPath`:** when no config is found at `--config` / `./shell3.lua` / `~/.shell3/shell3.lua`, return:

```
no shell3.lua found — run 'shell3 boot' to create one (or pass --config).
```

`EnsureProject` is unchanged. The result: a fresh machine running `shell3 "hi"` gets `~/.shell3/` + `.gitignore` created (harmless), then a clean failure pointing at `boot`.

---

## 4. Part B — `shell3 boot` command

New cobra subcommand in `cmd/shell3/boot.go`, registered on the root command in `main.go`. Because the root uses `cobra.ArbitraryArgs` with `RunE`, cobra still routes `shell3 boot` to the subcommand when the first arg matches a registered command name; the chat path is unaffected for any other first token.

### 4.1 Prompts (primary path)

Interactive, one per line, defaults in brackets:

```
shell3 boot

  Base URL   [https://api.openai.com/v1]: http://localhost:8787/v1
  Model tag  : kimi-k2.6
  Name (handle for this model) [main]: main
  API key    : ********              (input not echoed)

  Local proxy?
    Some endpoints are a proxy you launch yourself — e.g. a Codex
    subscription fronted by `npx ...`, or a local opencode-go. shell3
    can auto-start it for you (run_proxy) when the model is first used.
  Proxy command (blank to skip): npx @some/codex-proxy --port 8787

  Brave Search key (blank to add later): ********
```

- **Name → model handle** in `shell3.model("<name>", {...})`, referenced by both agents.
- **Key → `.env`** as `<NAME>_API_KEY` (uppercased name), e.g. `MAIN_API_KEY=...`.
- **Proxy command → `run_proxy`** on the model entry (omitted if blank).
- **Brave key:** `brave_search` references `shell3.env.secret("BRAVE_API_KEY")`, which is **fatal at load time if the key is absent from `.env`**. So boot **always writes `BRAVE_API_KEY=`** (empty placeholder + comment if skipped) to guarantee the config loads; the tool simply lights up once filled.

### 4.2 Flags (secondary path, for testing/scripting)

`--url`, `--model`, `--name`, `--key`, `--proxy`, `--brave-key` skip the matching prompt. `--force` allows overwriting an existing `~/.shell3/shell3.lua`. Missing non-flag values fall back to prompts when stdin is a TTY; if not a TTY and a required value is missing, error clearly.

### 4.3 File writes

Target dir: `~/.shell3/` (global; shared across projects — matches today's resolution order).

- Refuse if `~/.shell3/shell3.lua` already exists, unless `--force`.
- Render `shell3.lua` and `.env` from templates with the user's values.
- Copy the static module tree (`lib/...`) verbatim.
- `.env` handling: if `~/.shell3/.env` exists, **append** the new keys that are absent (never rewrite existing values); otherwise create it.

### 4.4 Success output

Print, in plain language:
- Paths written (`~/.shell3/shell3.lua`, `~/.shell3/lib/...`, `~/.shell3/.env`).
- "Secrets live in `~/.shell3/.env` — never commit it."
- The proxy note (what `run_proxy` does, that it's already wired if a command was given, the Codex-via-`npx` example).
- "Edit `~/.shell3/shell3.lua` (and `lib/`) to add tools, skills, MCP servers, or more agents — see the cookbook in the shell3 repo under `docs/cookbook/`."
- "Run `shell3 \"hello\"` to start."

---

## 5. Part C — Split-file base config

`boot` writes this tree under `~/.shell3/`:

```
~/.shell3/
  shell3.lua                  # main: requires modules, declares model + 2 agents (~70 lines, "really nice")
  .env                        # MAIN_API_KEY=…, BRAVE_API_KEY=…
  lib/
    tools.lua                 # returns { web_fetch=…, brave_search=… }
    guards.lua                # returns { no_env_edit=… }
    skills/
      brainstorming.lua       # returns shell3.skill({ name="brainstorming", … })
      subagents.lua           # returns shell3.skill({ name="spawning-subagents", … })
```

### 5.1 Wiring (how `require` resolves)

The loader prepends `dir/?.lua` and `dir/?/init.lua` to `package.path` (`luacfg.go:109-114`). Modules run in the same Lua state, so the `shell3` global is available inside them; each returns a value:

`shell3.lua` (templated, the only file with user values):

```lua
-- shell3.lua — base config written by `shell3 boot`. Edit freely.
local tools  = require("lib.tools")          -- { web_fetch, brave_search }
local guards = require("lib.guards")         -- { no_env_edit }
local brainstorming = require("lib.skills.brainstorming")
local subagents     = require("lib.skills.subagents")

shell3.model("{{.Name}}", {
  base_url       = "{{.BaseURL}}",
  api_key        = shell3.env.secret("{{.EnvKey}}"),  -- {{.EnvKey}} in .env
  model          = "{{.Model}}",
  context_window = 128000,
  reasoning      = "medium",
{{if .Proxy}}  run_proxy      = "{{.Proxy}}",  -- auto-started on first use; output -> ./.shell3/proxy-{{.Name}}.log
{{end}}})

shell3.agent({
  name = "code",
  model = "{{.Name}}",
  prompt = [[ …code prompt… ]],
  tools = {
    bash = true, bash_bg = true, shell_interactive = true, edit = true,
    history = true, prune = true, compact = true, media = true,
    custom = { tools.web_fetch, tools.brave_search },
  },
  skills = { subagents },
  on_tool_call = { guards.no_env_edit },
})

shell3.agent({
  name = "plan",
  model = "{{.Name}}",
  prompt = [[ …brainstorming/design prompt… ]],
  tools = {
    bash = true, edit = false, bash_bg = false,
    history = true, prune = true, compact = true, media = true,
    custom = { tools.web_fetch, tools.brave_search },
  },
  skills = { brainstorming },
  on_tool_call = { guards.no_env_edit },
})
```

`lib/tools.lua`, `lib/guards.lua`, `lib/skills/*.lua` are **static** (no user values) → embedded verbatim and copied. Only `shell3.lua` and `.env` are templated.

### 5.2 The two agents (dialed)

| | `code` | `plan` |
|---|---|---|
| gates | bash, bash_bg, shell_interactive, edit, history, prune, compact, media | bash, history, prune, compact, media (**no** edit/bash_bg/shell_interactive) |
| custom tools | web_fetch, brave_search | web_fetch, brave_search |
| skills | spawning-subagents | brainstorming |
| guard | no_env_edit | no_env_edit |
| prompt | senior pair-programmer: inspect → minimal edits → validate → summarize; concise; commit/push only when asked; context-hygiene rules | design partner: one question at a time, propose 2–3 approaches, present design in sections, write a saved design doc; does not edit code |

Prompts are **distinct** (not a shared base): `code`'s prompt references its built-in/custom tools and the subagents skill; `plan`'s prompt is brainstorming-flavored and references the brainstorming skill. Neither references tools/skills it doesn't have.

---

## 6. Part D — `brainstorming` skill (new port)

Port `superpowers:brainstorming` into `lib/skills/brainstorming.lua` as a `shell3.skill` body, **adapted to shell3**:

**Keep:** the core discipline — explore project context first; ask clarifying questions **one at a time** (multiple-choice preferred); propose 2–3 approaches with a recommendation; present the design in sections scaled to complexity, getting approval per section; YAGNI; design-for-isolation guidance; the "this is too simple to need a design" anti-pattern.

**Strip / adapt (Claude-Code-only):**
- `TodoWrite` checklist → "track the steps yourself as you go."
- `EnterPlanMode` / `ExitPlanMode` → not applicable; remove.
- Visual-companion browser section → remove.
- The terminal state "invoke writing-plans skill" → **end at a saved design doc**: write `docs/specs/YYYY-MM-DD-<topic>.md` (or project-appropriate path), commit if in git, then tell the user to **switch to the `code` agent (Tab when idle, or `/agent`) to implement.** No dependency on a writing-plans skill (we don't ship one in the base).
- References to Claude tool names → shell3 equivalents (or generic phrasing).

The `plan` agent's own prompt mirrors this posture so the agent behaves brainstorming-like even before reading the skill.

---

## 7. Part E — `spawning-subagents` skill (port as-is)

Move the existing reference `spawning-subagents` skill body into `lib/skills/subagents.lua` essentially verbatim (it already targets shell3: `bash_bg`, JSONL audit log, `SHELL3_HEADLESS=1`). Attached to `code`.

---

## 8. Part F — Cookbook (`docs/cookbook/`)

A repo directory preserving everything the slim base omits, as **drop-in Lua modules** mirroring the `lib/` layout, each with a short header comment, plus a `README.md` index.

```
docs/cookbook/
  README.md                        # index: what each recipe is, how to drop it in
  lib/
    skills/
      writing-plans.lua            # from the old reference
      executing-plans.lua
      codebase-discovery.lua
      web-search.lua
    guards.lua                     # extra guard recipes (block destructive bash, path allowlists)
    mcp.lua                        # shell3.mcp({...}) example + attaching tools={mcp=…} to an agent
    proxy.md                       # run_proxy recipes: litellm, opencode-go, Codex-via-npx
    extra-agents.lua               # adding a third agent, headless/subagent patterns
    tools.lua                      # additional custom-tool examples beyond web_fetch/brave_search
```

Usage pattern documented in `README.md`: copy a file into `~/.shell3/lib/...`, then `local x = require("lib.skills.writing-plans")` and add `x` to an agent's `skills`/`tools`. The current 712-line reference's salvageable content (MCP commented block, the four dropped skills, the env-edit guard variants) is the seed for these recipes.

---

## 9. Part G — Code changes by package

- **`internal/bootstrap/bootstrap.go`** — drop `WriteStarterConfig` call from `EnsureGlobal`; keep dir + `.gitignore` creation.
- **`internal/scaffold/`** — replace single-file embed with an embedded `defaults/base/` tree (`//go:embed all:defaults/base`). New API, e.g. `RenderBaseConfig(dir string, v Values) error` that: walks the embed FS; renders `shell3.lua.tmpl` + `.env.tmpl` via `text/template`; copies `lib/**` verbatim with `writeIfAbsent`. Delete `defaults/shell3.lua` (its content migrates to base modules + cookbook) and `defaults/env.example`.
- **`cmd/shell3/boot.go`** — new file: flag defs, prompt loop (TTY-aware; no-echo for secrets via `golang.org/x/term`), no-clobber/`--force`, `.env` merge, call `scaffold.RenderBaseConfig`, print success guidance.
- **`cmd/shell3/main.go`** — register `boot` subcommand.
- **`internal/agentsetup`** — update the no-config error message to the `boot` redirect.
- **Tests** — `scaffold` render test (templated values land, static tree copied, idempotent); `boot` `.env` merge test (append-missing, never clobber); bootstrap test asserting `EnsureGlobal` no longer writes `shell3.lua`.

---

## 10. Part H — Manual test (real from-zero)

Performed as part of the work so the user can try onboarding cold:

```bash
# Back up the entire live home config (preserves your keys to paste during boot)
mv ~/.shell3 ~/.shell3.bak

# 1. No config → expect the boot redirect, not an auto-written config
shell3 "hi"        # -> "no shell3.lua found — run 'shell3 boot' to create one"

# 2. Onboard from zero (paste your real URL / model / key from ~/.shell3.bak/.env)
shell3 boot

# 3. Works now
shell3 "hello"

# Restore anytime:
rm -rf ~/.shell3 && mv ~/.shell3.bak ~/.shell3
```

The user will be given the exact values to paste (from the backup) when it's time to test.

---

## 11. Decisions made (default unless you object)

- Base config target = **global `~/.shell3/`** (not project-local).
- `.env` key name = **`<NAME>_API_KEY`**.
- `brave_search` ships in the base (per request) with an **empty `BRAVE_API_KEY` placeholder** so load never fails.
- Skills are **distinct** per agent: `code`→subagents, `plan`→brainstorming.
- Brainstorming **ends at a saved design doc** (no writing-plans dependency).
- Split-file layout with a `lib/` module tree; only `shell3.lua` + `.env` are templated.
- From-zero test **moves `~/.shell3` aside** (recoverable) rather than irrecoverably deleting.

## 12. Out of scope

- TUI wizard; multiple-model onboarding in one `boot` run (one model; add more by editing).
- Changing config-resolution precedence or the Lua API.
- Auto-installing proxy binaries (`npx`/opencode-go/litellm) — boot only wires `run_proxy`.
