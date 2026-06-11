# Telegram-first setup: config path + `boot --telegram` + tuned prompt

Date: 2026-06-11
Status: implemented (2026-06-11)

## Goal

Make standing up a Telegram bot a first-class, self-contained flow:

1. **Separate config location.** The Telegram host reads its config from
   `~/.shell3/telegram/shell3.lua` (with its own `.env` beside it), cleanly
   separated from the generic global config at `~/.shell3/shell3.lua`.
2. **A dedicated bootstrap.** `shell3 boot --telegram` scaffolds that directory
   interactively — model endpoint **plus** the Telegram bot token, chat id, and
   dashboard — so a new user gets a working bot without hand-editing Lua.
3. **A better default agent.** The scaffolded config ships a Telegram-tuned agent
   prompt (chat-appropriate communication + autonomy priming) so the bot behaves
   well over a phone from the first message.

This builds directly on the existing `boot`/`scaffold`/`agentsetup` machinery and
the already-shipped reload, `status`, `self-evolve`, and `scheduling-jobs`
features.

## Decisions (from brainstorming, locked)

- **Resolution order (Telegram only):**
  `--config` → `~/.shell3/telegram/shell3.lua` → `~/.shell3/shell3.lua` → `./shell3.lua`.
  Only `shell3 telegram` uses this order; the TUI and every other path keep the
  existing `ResolveConfigPath` (`./shell3.lua` → `~/.shell3/shell3.lua`).
- **Command shape:** a `--telegram` boolean flag on the existing `boot` command
  (not a new subcommand), reusing boot's prompt/scaffold/.env plumbing.
- **No migration (YAGNI).** `boot --telegram` always scaffolds a fresh config.
  An existing `~/.shell3/shell3.lua` keeps working via the fallback until the
  telegram config is created, so no migration/copy code is needed in v1.
- **Chrome MCP is opt-in, default off.** `boot --telegram` asks whether to enable
  the Chrome DevTools MCP (browser automation). Only when the user says yes does
  the scaffolded config declare the `chrome` MCP server and grant it to the agent.
  The server is started lazily (when the agent first uses it), needs Node/`npx`,
  and is the cookbook recipe (`docs/cookbook/lib/mcp.lua`).

## Non-goals

- **Migrating an existing `~/.shell3/shell3.lua` into the telegram dir.** Out of
  scope; the fallback covers the gap. (Possible future `--from <path>` helper.)
- **Changing resolution for non-telegram front-ends.** The TUI/once paths are
  untouched.
- **Multi-bot / multi-account.** Single Telegram config per host.
- **Reworking the prompt of the *generic* `boot` config.** Only the new telegram
  template carries the tuned prompt.

## Verified internals (current code, 2026-06-11)

- **`boot` command** (`cmd/shell3/boot.go`): `newBootCommand()` defines flags
  `--url/--model/--name/--key/--proxy/--brave-key/--force`. `runBoot(f)`:
  hard-codes `dir = ~/.shell3`, `cfgPath = dir/shell3.lua`; refuses to clobber
  unless `--force`; prompts via `value(...)` (flag-or-TTY); derives
  `envKey = envKeyForName(name)`; calls
  `scaffold.RenderBaseConfig(dir, scaffold.Values{Name,BaseURL,EnvKey,Model,Proxy}, force)`;
  merges + writes `~/.shell3/.env` (0600) via `mergeEnv`; prints a success note.
- **`scaffold`** (`internal/scaffold/scaffold.go`): `//go:embed all:defaults/base`.
  `RenderBaseConfig(dir string, v Values, force bool) error` renders
  `defaults/base/shell3.lua.tmpl` (funcs: `luaesc`) to `dir/shell3.lua`, then
  walks `defaults/base/lib` copying every `lib/*.lua` verbatim.
  `Values{Name, BaseURL, EnvKey, Model, Proxy string}`.
- **`.env` location**: `luacfg.Load(path, workdir)` calls
  `loadDotEnv(filepath.Join(workdir, ".env"))`; `agentsetup` builds with
  `luacfg.Load(configPath, filepath.Dir(configPath))`. So `.env` is always read
  from the **directory of `shell3.lua`** — a telegram config dir gets its own
  `.env` for free.
- **Path resolution**: `agentsetup.ResolveConfigPath(flag, cwd, homeDir) (string, error)`
  — `flag` wins; else `cwd/shell3.lua` if it exists; else
  `homeDir/.shell3/shell3.lua` if it exists; else an error. Callers:
  `agentsetup.BuildParts` (via `resolvePaths`), `pkg/shell3.NewRuntime`,
  `pkg/shell3.Runtime.ConfigPath`.
- **Telegram command** (`cmd/shell3/telegram.go`): defines `--config`, builds
  `shell3.NewRuntime(RuntimeSpec{ConfigPath: configPath, WorkDir: cwd})`.
- **`paths`** (`internal/paths/paths.go`): `Global{Root,Projects,LogFile}`,
  `NewGlobal(homeDir)`; `Local`, `Project`, constructors.

## Components

### 1. `agentsetup.ResolveTelegramConfigPath(flag, cwd, homeDir) (string, error)`

A telegram-specific resolver mirroring `ResolveConfigPath` but inserting the
telegram dir ahead of the generic global:

```
if flag != ""                                -> flag                          // explicit
if <home>/.shell3/telegram/shell3.lua exists -> it                            // new telegram default
if <home>/.shell3/shell3.lua exists          -> it                            // back-compat global fallback
if <cwd>/shell3.lua exists                   -> it                            // project-local, last
else                                         -> error (suggest `shell3 boot --telegram`)
```

> Ordering rationale (matches the approved design): for the always-on bot the
> dedicated telegram config wins, then the existing global config (so today's live
> setup keeps working), and the project-local `./shell3.lua` is last — the
> opposite of the generic `ResolveConfigPath`, which puts `./shell3.lua` first.
> This ordering is locked; do not reorder in the plan.

`cmd/shell3/telegram.go` resolves the path explicitly *before* `NewRuntime` and
passes the concrete path as `RuntimeSpec.ConfigPath`, so `Runtime.Reload` and
`Runtime.ConfigPath` read the same file (no `""` ambiguity for the bot).

`ResolveTelegramConfigPath` returning an error keeps today's behavior: the bot
exits with a clear "no config — run `shell3 boot --telegram`" message.

### 2. `boot --telegram`

Add `telegram bool` to `bootFlags` (`--telegram`). When set, `runBoot`:

- **Target dir** = `~/.shell3/telegram` (else `~/.shell3` as today). `cfgPath` =
  `dir/shell3.lua`. Same `--force` no-clobber guard on the new path.
- **Prompts** (flag-or-TTY via `value`): the existing model `url/model/name/key`
  (and `--proxy/--brave-key`), **plus** new telegram inputs:
  - `TELEGRAM_BOT_TOKEN` (secret; goes to `.env`, never the lua) — flag
    `--tg-token`.
  - `chat_id` (written into the lua) — flag `--chat-id`.
  - dashboard: enable (default yes), `addr` (default `127.0.0.1:8765`), public
    `url` (optional) — flags `--dash-addr`, `--dash-url`, `--no-dashboard`.
  - Chrome MCP: enable browser automation (default **no**) — flag `--chrome`,
    else a `[y/N]` prompt on a TTY.
- **Render** the telegram template (component 3) via a new
  `scaffold.RenderTelegramConfig(dir, scaffold.TelegramValues{...}, force)` (or
  an extended `RenderBaseConfig` with a template selector — plan picks the
  cleaner split). Copies the same `lib/` modules, plus the
  `self_evolve`/`scheduling_jobs` skill modules (see component 4).
- **`.env`**: write `~/.shell3/telegram/.env` (0600) with the model key and
  `TELEGRAM_BOT_TOKEN` via the existing `mergeEnv`.
- **Success note**: print the telegram config path, `.env` path, and the exact
  run command (`shell3 telegram`).

Non-telegram `boot` is unchanged.

### 3. Telegram scaffold template (`defaults/telegram/shell3.lua.tmpl`)

A sibling of `defaults/base/shell3.lua.tmpl`, rendering a complete telegram host:

- the `shell3.model(...)` block (same templated fields as base);
- an `explorer` subagent (read-only investigator) — cron jobs reference it;
- a **Telegram-tuned `code` agent** carrying the Communication + Autonomy prompt
  sections (component 5), with tools `bash/bash_bg/edit/history/prune/compact/
  media/subagents{explorer}/custom{web_fetch,brave_search}`, granted the
  `self_evolve` + `scheduling_jobs` skills, guarded by
  `no_env_edit, confirm_destructive`;
- a `shell3.telegram{ token = shell3.env.secret("TELEGRAM_BOT_TOKEN"), chat_id =
  "{{.ChatID}}", agent = "code", workdir = ..., dashboard = {...} }` block wired
  to the prompted values;
- a **commented** sample `shell3.cron{}` showing the `@every`/subagent pattern
  (disarmed by default);
- **when Chrome MCP is enabled**, a `shell3.mcp({ name="chrome", command="npx",
  args={"-y","chrome-devtools-mcp@latest","--autoConnect","--no-usage-statistics"} })`
  declaration and `mcp = { chrome }` added to the `code` agent's `tools` block —
  both gated behind a `{{if .Chrome}}` template conditional.

`scaffold.TelegramValues` extends `Values` with `ChatID string`,
`DashboardEnabled bool`, `DashboardAddr string`, `DashboardURL string`, and
`Chrome bool`. The bot token is **not** a template value — the template always
emits `shell3.env.secret("TELEGRAM_BOT_TOKEN")`.

### 4. Skill modules in the scaffold

`self_evolve` and `scheduling_jobs` already exist as content (shipped in
`defaults/base/shell3.lua.tmpl` inline, and as `lib/skills/*.lua` in the live
home config). For the telegram template, ship them as `lib/skills/self_evolve.lua`
and `lib/skills/scheduling_jobs.lua` embedded modules and `require` them in the
template (matching the home-config idiom). The plan decides whether to factor the
existing inline base-template skill into a shared module or keep separate copies;
default: embedded `lib/skills/*.lua` for the telegram template, leave base as-is.

### 5. Telegram agent prompt (approved wording)

Two sections added to the telegram template's `code` agent prompt:

```
## Communication (you talk over Telegram)
- Short, natural, human. No memo voice, no long preambles, no walls of text, no
  restating the question. One phone screen, not three.
- Lead with the answer/result; add detail only if asked.
- Plain text by default; light markdown (`code`, bullets) when it helps. Avoid
  wide tables — they wrap badly on mobile.
- Don't paste huge output — write it to a file and send it with
  send_media_telegram.
- Emoji sparse, for warmth or a quick ✅/⚠️.

## Autonomy (you run unattended)
- Actionable request → act this turn; don't reply with a plan you could just
  execute.
- Continue until done or genuinely blocked; vary approach on weak results before
  giving up.
- Reversible/local actions: just do them. Irreversible/destructive/external/
  privacy-sensitive: ask first, one concise question.
- Ask at most one question, only when a single missing decision blocks safe
  progress; otherwise pick a sensible default and note it.
- Long jobs: send a one-line "on it", then keep going; hand parallel work to a
  subagent (spawn_agent) and don't poll — results come back to you automatically.
- Final claims need evidence (ran the test/build, read the file) or a named
  blocker.
- You can reshape yourself: `status` shows your config path, the self-evolve
  skill applies edits via reload, and scheduling-jobs adds cron.
```

Derived from openclaw's production prompts (`src/agents/system-prompt.ts`
Execution Bias; `src/agents/gpt5-prompt-overlay.ts` live-chat tone + execution
policy; `src/agents/subagent-system-prompt.ts` no-polling delegation), reworded
for shell3's actual tools (`send_media_telegram`, `spawn_agent`, `reload`,
`status`, the two skills) and chat surface.

## Data flow

**Setup:** `shell3 boot --telegram` → prompts → `~/.shell3/telegram/{shell3.lua,
.env, lib/...}` written → prints `shell3 telegram` to run.

**Run:** `shell3 telegram` → `ResolveTelegramConfigPath` picks
`~/.shell3/telegram/shell3.lua` → `NewRuntime(ConfigPath: <that path>)` →
`.env` loaded from the same dir → bot runs; `/reload` and the reload tool re-read
the same path.

## Error handling & edge cases

- **No telegram config yet:** resolver falls back to `~/.shell3/shell3.lua` (the
  current live setup keeps working); if nothing exists, the bot exits with
  "run `shell3 boot --telegram`".
- **Clobber:** `boot --telegram` refuses to overwrite an existing
  `~/.shell3/telegram/shell3.lua` without `--force` (same guard as today).
- **Secret hygiene:** `TELEGRAM_BOT_TOKEN` only ever lands in `.env`; the lua
  references it via `shell3.env.secret`. Honors the CLAUDE.md never-print-secrets
  rule (boot already writes `.env` at 0600 without echoing).
- **Non-TTY / flags:** all telegram inputs have flags, so `boot --telegram
  --url ... --model ... --tg-token ... --chat-id ...` works headlessly (mirrors
  the existing flag-or-prompt pattern). Chrome MCP defaults off without a TTY
  unless `--chrome` is passed.
- **Chrome MCP needs Node/`npx`:** the bootstrap prints a one-line note when Chrome
  is enabled. The server is started lazily by the MCP manager on first use (not at
  config load), so a missing `npx` surfaces as the existing MCP-discovery warning
  at runtime, never as a boot/scaffold failure.

## Testing

- **`ResolveTelegramConfigPath` ordering (unit, agentsetup):** with a temp home,
  assert each precedence step (flag; telegram dir; project-local; global
  fallback; error when none).
- **`boot --telegram` end-to-end (cmd/shell3):** run with flags into a temp home;
  assert `~/.shell3/telegram/shell3.lua` + `.env` exist, `.env` has the model key
  + `TELEGRAM_BOT_TOKEN`, the rendered config **loads through `luacfg`**, and
  `rt.Telegram()` reflects the prompted token/chat_id/dashboard. Assert the
  generic `boot` (no flag) still targets `~/.shell3`.
- **Telegram template scaffold (internal/scaffold):** render `TelegramValues`,
  load the result, assert the `code` agent, `explorer` subagent, telegram block,
  and the two skills are present. A second case with `Chrome: true` asserts the
  `chrome` MCP server is declared (`luacfg` records it without spawning `npx`);
  the default (`Chrome: false`) asserts no MCP server is present.
- **`shell3 telegram` uses the resolver:** assert `telegram.go` resolves to the
  telegram path when present (and that `Runtime.ConfigPath()` returns it).
- `go build ./... && go vet ./... && gofmt -l . && go test -race ./...` green.

## Implementation approach

Build on the existing machinery with small, isolated additions: a pure resolver
function (testable in isolation), a flag + a branch in `runBoot`, a new scaffold
template + `TelegramValues`, and the telegram-tuned prompt as template text. No
engine changes; no changes to non-telegram resolution. Sonnet subagents over the
disjoint pieces (resolver; boot flag + .env; template + prompt; telegram.go
wiring), orchestrator verifies after each.

## Future work (out of scope)

- `boot --telegram --from <path>` to migrate an existing config into the telegram
  dir.
- A `shell3 telegram` first-run hint that offers to run `boot --telegram` when no
  config is found.
- Per-skill versioning / re-read prompts (openclaw's `<version>` pattern) if the
  skill set grows.
