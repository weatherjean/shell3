# Configuration

Everything shell3 does is decided by one file: `shell3.lua`. It's plain Lua, so
it's versionable, diffable, and programmable — you configure shell3 like
software, not like a platform with a settings panel.

`shell3 boot` writes a working config for you. This page is for when you want to
go beyond the defaults: add a model, write a tool, gate the shell, or understand
how shell3 finds your config in the first place.

## Where the config lives

`boot` creates three things under `~/.shell3/`:

- `shell3.lua` — the config: your models, the agent, subagents, tools, and skills.
- `lib/` — tool modules the config `require`s and `lib/skills/` markdown skills.
- `.env` — your secrets (API keys, tokens). **Never commit this file.**

### How the config path is resolved

The `--config` / `-c` flag takes a **name**, not a path — unless it ends in
`.lua`:

| You pass | shell3 loads |
|----------|--------------|
| *(nothing)* | `~/.shell3/shell3.lua` |
| `-c code` | `~/.shell3/code.lua` |
| `-c ./my.lua` | that literal file |

The current working directory is never consulted for config. This keeps a
session's behavior predictable no matter where you launch it from.

## Models

A model is an endpoint plus the parameters shell3 should send it. Any
OpenAI-compatible endpoint works:

```lua
shell3.model("main", {
  base_url       = "https://api.openai.com/v1",
  api_key        = shell3.env.secret("MAIN_API_KEY"),  -- read from .env
  model          = "gpt-5.2",
  context_window = 128000,   -- the model's real token budget
  compact_at     = 100000,   -- auto-compact threshold (see below); 0 = off
  -- reasoning   = "medium", -- if the model supports reasoning effort
})
```

Set `context_window` to the model's *actual* budget — the wrong number throws
off the context-usage reminders and the compaction trigger.

`compact_at` is an absolute prompt-token threshold. When a turn's prompt crosses
it, shell3 summarizes the head of the conversation and keeps a verbatim recent
tail — so the model retains its immediate working context. This is
host-managed: there are no model-driven prune or compact tools to call. Leave
it unset (or `0`) to disable.

Two optional knobs tune the compaction policy:

```lua
shell3.model("main", {
  compact_at   = 100000,  -- full tail-preserving compaction
  keep_recent  = 33000,   -- verbatim tail size (tokens); default round(compact_at * 0.33)
  prune_at     = 60000,   -- lower threshold: stubs old tool outputs, no LLM call
                          --   default round(compact_at * 0.6); 0 = disabled
})
```

`keep_recent` controls how many recent prompt tokens are preserved verbatim
across a compaction. Only the head (everything before that boundary) is
summarized. Defaults to `round(compact_at * 0.33)`; clamped to
`round(compact_at * 0.5)` if you set it at or above `compact_at`.

`prune_at` is a cheaper first response: when the prompt crosses this lower
threshold shell3 stubs old tool outputs inline (no LLM call needed), buying
room before a full compaction is required. Defaults to
`round(compact_at * 0.6)`; set to `0` to skip this tier entirely. Ignored if
set at or above `compact_at`.

Agents and subagents can opt out of the prune tier individually:

```lua
shell3.agent({ ..., prune = false })     -- skip pruning; go straight to compact_at
shell3.subagent({ ..., prune = false })
```

`prune = false` disables the tool-output-stubbing tier for that agent or
subagent only — the thresholds themselves stay on the model. Omitted (or
`true`) inherits the model's `prune_at`.

Besides the automatic thresholds, the `/compact` chat command forces one
compaction on demand (one LLM call; replies with the token delta), and
`/clear` hard-resets the conversation — refused while background tasks are
running, so a finishing task can never leak its result into the fresh session
(`/stop` first, or let them finish).

### Provider-specific knobs

Some providers want a non-standard request field shell3 doesn't model directly.
The `extra` table is the escape hatch — its keys are injected verbatim into the
top-level request JSON:

```lua
extra = { reasoning_split = true },           -- MiniMax: route thinking to reasoning_content
extra = { verbosity = "high" },               -- gpt-5-style verbosity
extra = { provider = { order = {"anthropic"} } },  -- OpenRouter routing (nested objects work)
```

Only set `extra` when you need it — strict endpoints reject unknown fields. See
[cookbook/models.md](cookbook/models.md) for more.

### Local proxies (`run_proxy`)

If a model needs a shim in front of its endpoint — say a Codex subscription
fronted by `npx`, or a litellm gateway — set `run_proxy`. shell3 starts that
command (detached, fire-and-forget) the first time an agent uses the model, and
logs go to `~/.shell3/proxy-<model>.log`:

```lua
shell3.model("codex", {
  run_proxy = "npx @some/codex-proxy --port 8787",
  base_url  = "http://localhost:8787/v1",
  -- ...
})
```

If a proxy is already listening, the spawn just fails to bind and the first
request proceeds against whatever is there. See
[cookbook/proxy.md](cookbook/proxy.md).

## The agent

An agent is a name, a model, a system prompt, and a set of tools. **Exactly one
`shell3.agent({...})` may be declared** — a second declaration fails the load.
Specialists are [subagents](#subagents--delegation), spawned by the agent as
background jobs; see
[cookbook/lib/extra-agents.lua](cookbook/lib/extra-agents.lua).

```lua
shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash      = true,
    bash_bg   = true,   -- background / long-running work
    edit      = true,   -- the edit_file tool
    media     = true,   -- read_media: images, audio, PDFs, video (+ outbound voice/image via the media blocks below)
    custom    = { my_tool },          -- Lua-defined tools (below)
    subagents = { explorer },         -- delegatable specialists
  },
  skills = { "lib/skills" },                  -- skill directories (below)
})
```

There are **no file-read tools**: the agent reads with `cat`/`sed -n`, lists
with `ls`/`find`, and searches with `rg` — all through `bash` (hallucinated
`read`/`grep` calls are redirected, see [stub tools](#stub-tools)). A
*read-only* agent is a policy, not a tool set: gate `bash` in
[`on_tool_call`](#the-command-gate--on_tool_call) to inspection-only commands.

## Subagents & delegation

A subagent is a delegatable specialist: declared with `shell3.subagent({...})`
instead of `shell3.agent({...})` — only an agent that lists it under
`tools.subagents` may spawn it. The shape is the same as
an agent's (name, model, prompt, tools) plus a `description`, which is what the
parent model reads when deciding what to delegate. Subagent names are
deduplicated rather than rejected: agents and subagents share one namespace, so
a second entry named `explorer` auto-suffixes to `explorer2` (then
`explorer3`, …) while the first keeps its bare name.

```lua
local explorer = shell3.subagent({
  name        = "explorer",
  description = "Read-only investigation of the codebase. No edits.",
  model       = "main",
  prompt      = [[You are a focused code explorer…]],
  tools       = { bash = true },
})

shell3.agent({
  name       = "code",
  delegation = true,                      -- advertise the task tools
  tools      = { subagents = { explorer } },  -- who this agent may spawn
  -- ...
})
```

When an agent sets **both** `delegation = true` and a non-empty
`tools.subagents` list, shell3 advertises four tools to it — `task` (spawn one:
`{subagent_type, prompt, description}`; returns immediately), `task_list`,
`task_status <id>`, and `task_cancel <id>`. The allowed subagents — names plus
their `description`s — are baked into the `task` tool's own schema (an enum on
`subagent_type`), so the model learns what it may spawn from the tool itself;
no per-turn reminder is injected. One toggle without the other advertises
nothing.

A spawned subagent runs as an **in-process background job** — a child-session
goroutine, not a subprocess — and on completion the parent is woken with a
capped result summary injected into its context. Subagents run headless (an
`{ask=...}` gate verdict is auto-denied; see
[the gate section](#deny-prompt-confirmation-and-headless-degradation)), and
delegation is single-level by construction: a subagent is never given the
`task` tool, so it cannot spawn subagents of its own.

One optional config-global knob caps the machinery:

```lua
shell3.background({ max_concurrent = 8 })  -- concurrent background jobs (default 8)
```

The same job runtime backs `bash_bg`, which is gated separately by
`tools = { bash_bg = true }` — not by `delegation`. A `bash_bg` job that exits
**nonzero** wakes an idle agent so the failure is narrated proactively; a clean
exit queues its notice quietly for the next turn.

**Subagents and `bash_bg`.** A subagent's still-running `bash_bg` job keeps
its session open past its main turn; each completion **resumes** the subagent
for a follow-up turn whose summary reaches the main agent as a notice (capped
at 5 per subagent — past the cap, or after cancel, the raw job notice is
delivered instead, so no completion is lost). `task_cancel <sub-id>` cascades
to the jobs that subagent started.

## Custom tools

A custom tool is **not** a Lua function — it's a bash command template. You give
it a name, a JSON schema for its parameters, and a command. Declared parameters
arrive in the command's environment as lowercase `$`-named variables; declared
`secrets` are exported too (and kept out of the command string). The command's
stdout is what the model sees.

```lua
local weather = shell3.tool({
  name        = "weather",
  description = "Current weather for a city.",
  parameters  = {
    type       = "object",
    properties = { city = { type = "string", description = "City name." } },
    required   = { "city" },
  },
  secrets = { "WEATHER_API_KEY" },           -- exported as $WEATHER_API_KEY
  command = [[
curl -sf -G "https://api.example.com/v1/current" \
  -H "Authorization: Bearer $WEATHER_API_KEY" \
  --data-urlencode "q=$city" \
| jq -r '.location.name + ": " + (.current.temp_c|tostring) + "°C"'
]],
})
```

Two habits keep tools safe and tidy: use `curl --data-urlencode` for any
user-supplied parameter (never interpolate model text straight into a URL), and
shape the output with `jq` so you return a clean line, not a wall of JSON.

Optional fields: `background = true` (runs the command as an in-process
background job, like `bash_bg`: it shows up in the dashboard's jobs view and the
agent is notified with a completion notice on a later turn) and
`timeout = N` (seconds; foreground tools only). See [cookbook/lib/tools.lua](cookbook/lib/tools.lua) for a full
template, including the `web_fetch` and `brave_search` tools the base config
ships with.

> **A note on secrets:** declared `secrets` are passed via the command's process
> environment. On a shared host, same-user processes can read another process's
> environment (`/proc/<pid>/environ` on Linux). That's fine for a local,
> single-user setup; on a multi-user box, treat tool secrets as readable by
> anything that user can run. More in [security.md](security.md).

## Opt-in command gate — `on_tool_call`

shell3 is **unsafe by default**: bash commands run with no restrictions.
`on_tool_call` fires before **every** tool the model calls — `bash`, `bash_bg`,
`edit_file`, `read_media`, and custom tools — and the
handler decides per tool by switching on `t.name`. It is off until you register it;
a fresh config gates nothing. `t.command` carries the bash command for the two
bash tools and is **nil** for everything else, so a denylist that matches
`t.command` must first check `t.name` (see the idiom below). Handlers are
**chainable** — multiple `on_tool_call` calls run in declaration order; the first
**terminal** verdict wins.

### The `t` event

Each handler receives a table `t`:

| Field | Description |
|-------|-------------|
| `t.name` | The **real** tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, or a custom tool's name. |
| `t.command` | The bash command string — only for the two bash tools; **nil** for every other tool. |
| `t.args` | Raw arguments JSON string (every tool). Gate a non-bash tool by inspecting this, e.g. an `edit_file` path. |
| `t.headless` | `true` when no human asker is attached to the session (in-process subagents, cron jobs) — an `{ask=...}` verdict would auto-deny there. Independent of `disable_safety`. See [headless degradation](#deny-prompt-confirmation-and-headless-degradation). |

### Verdict contract

A handler returns one of:

| Return value | Effect |
|---|---|
| `nil` | Pass; continue to the next handler (or run). |
| `{ command = "..." }` | Rewrite the bash command text; continue the chain. **Bash tools only** — on a non-bash tool this fails closed. |
| `{ argv = { ... } }` | **Terminal**: exec this argv exactly (runner swap). **`bash`/`bash_bg` only** — non-bash tools fail closed. |
| `{ block = true, reason = "..." }` | **Terminal**: block; `reason` is surfaced to the model. Works for any tool. |
| `{ ask = "prompt", reason = "...", ask_timeout = N }` | Prompt a human; allowed → run, declined/headless → block with `reason`. Works for any tool. `ask_timeout` optional (seconds, default 300). |

A handler that raises a Lua error **fails closed** (blocks). Only `{block=true}`
blocks via the block verdict; a returned table that contains none of the recognized
keys (`block`/`argv`/`ask`/`command`) fails closed (is blocked) as a safety
default; return `nil` to pass.

### Writing a denylist with `shell3.regex`

`shell3.regex(pattern)` compiles a Go RE2 pattern **at config load** — a bad
pattern is a load error, never a runtime surprise. Returns an object with
`:match(s) -> bool (unanchored)`.

Recommended idiom for a hard-block / ask-human denylist:

```lua
local re   = shell3.regex
-- (?s) so .* spans newlines; match t.command (the whole string) so
-- chaining can't hide a flagged fragment behind a benign prefix.
local HARD = { re([[(?s)rm\s+-rf\s+/]]), re([[(?s)mkfs]]), re([[(?s)dd\s+if=]]) }
local ASK  = { re([[(?s)rm\s+-rf]]), re([[(?s)\bgit\s+push]]), re([[(?s)curl\b.*\|\s*(ba)?sh]]) }
local ENV  = re([[\.env]]) -- hoisted like the lists above: compiled once at load

shell3.on_tool_call(function(t)
  -- Gate the bash family. This guard is REQUIRED: t.command is nil for non-bash
  -- tools, so matching it without the check would error (→ fail closed).
  if t.name == "bash" or t.name == "bash_bg" then
    for _, p in ipairs(HARD) do
      if p:match(t.command) then return { block = true, reason = "hard_deny" } end
    end
    for _, p in ipairs(ASK) do
      if p:match(t.command) then
        -- Headless (subagent / cron job): an ask would auto-deny anyway,
        -- so block with a reason the parent agent can act on.
        if t.headless then return { block = true, reason = "needs approval; rerun interactively" } end
        return { ask = "Run?\n" .. t.command, reason = "denied" }
      end
    end
  end
  -- Other tools fall through to nil (run). Gate them by name + args if you want,
  -- e.g. refuse to edit the secrets file:
  if t.name == "edit_file" and ENV:match(t.args) then
    return { block = true, reason = "no editing .env" }
  end
end)
```

Because the gate sees every tool, the same hook can enforce things like "never
read `.env`" or "no `edit_file` under `/etc`" — match on `t.name` and `t.args`.
`{command}`/`{argv}` rewrites only make sense for the bash family, so returning one
for a non-bash tool fails closed.

> **No implicit `(?s)`.** `shell3.regex` does not prepend `(?s)` automatically —
> add it yourself on patterns where `.*` must span newlines. `^`/`$` anchor to the
> start/end of the whole command (not each line); prefer `\b`-anchored fragments
> (`\bgit\s+push`) over `^`/`$` unless you specifically mean the command's ends.

### Deny-prompt confirmation and headless degradation

When a handler returns `{ask=...}`, a human must confirm. **The bot shows inline
Allow/Deny buttons in the chat**. **Headless subagents and cron jobs** have no attached human, so an `{ask=...}` verdict
is auto-denied with its `reason`; the block reason flows back to the parent agent
in the in-process completion notice so the parent — where a human *is* attached — can decide
how to proceed. Handlers see this ahead of time as `t.headless` and can return a
tailored `{block=...}` (or allow a safe subset) instead of an ask that will never
be answered. A prompt nobody answers falls back to deny after the timeout
(`ask_timeout`, default 300 s). `{block=true}` never prompts — it blocks
everywhere, headless or not.

Because there's no allowlist, ordinary reads (`cat`, `rg`, `ls`, …) match no
pattern and just run — a headless subagent explores freely. Only commands you
explicitly gate in a handler are affected.

### Runner swap (container, SSH, firejail)

The `{ argv = { ... } }` verdict lets you choose the program that runs the
agent's command — the command arrives as one argv element so nothing re-parses or
re-quotes it:

```lua
shell3.on_tool_call(function(t)
  -- Wrap every bash command in the container.
  if t.name == "bash" or t.name == "bash_bg" then
    return { argv = {"docker", "exec", "mycontainer", "bash", "-c", t.command} }
  end
end)
```

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped. A custom
command-template tool's command is your trusted template (not model input), so it is
never rewritten — but the tool **call** still fires `on_tool_call` (by its name, with
`t.command` nil), so you can `block`/`ask` it. The full recipe set is in
[cookbook/sandbox.md](cookbook/sandbox.md).

### Tool-result rewriting — `on_tool_result`

The symmetric post-execution hook `shell3.on_tool_result(fn)` runs after a tool
produces output. Like `on_tool_call`, it fires for **every** tool, and `r.name` is
the real tool name — `"bash"`, `"bash_bg"`, `"read"`, `"edit_file"`, or a custom
tool's name. The
handler receives `r` with `r.name`, `r.args` (raw arguments JSON), and `r.output`.
Return `{ output = "..." }` to replace what the model sees; return `nil` to pass
through unchanged. Primary use: secret redaction — for which you usually want to
cover all output, not just one tool:

```lua
shell3.on_tool_result(function(r)
  return { output = (r.output:gsub("API_KEY=%S+", "API_KEY=[redacted]")) }
end)
```

Errors in `on_tool_result` handlers fail **open** — a broken result-rewriter
must not destroy tool output — so they are logged and the original passes through.
(Contrast `on_tool_call`, which fails closed: blocking is safe, silently nuking
output is not.) The flip side: if your redactor errors, the **unredacted** output
reaches the model, so keep redaction handlers simple and total.

## Redirecting hallucinated tools — `stub_tools`

shell3 has no file-read tools, and models trained on other harnesses
reflexively reach for `read`, `read_file`, `grep`, or `write_file`. Register
those names as stubs and shell3 returns a one-line redirect instead of erroring
— a self-correcting nudge back to bash and `edit_file`:

```lua
shell3.stub_tools({
  read       = "Use bash: cat <path>, or sed -n 'START,ENDp' <path> for a slice.",
  read_file  = "Use bash: cat <path>.",
  list_files = "Use bash: ls <dir> or find <dir> -maxdepth 2.",
  grep       = "Use bash: rg <pattern>.",
  write_file = "Use edit_file (empty old_string creates/overwrites).",
})
```

The scaffold ships this block enabled. Stubs are config-global (every agent
sees them). Later keys override earlier ones, and a stub whose name collides
with a real tool is ignored.

## Telegram host — `shell3.telegram`

shell3 runs as a Telegram bot; `shell3.telegram{}` configures it. The bot
answers exactly one `chat_id` and runs the one configured agent (which may
spawn subagents).

```lua
shell3.telegram({
  token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),  -- from @BotFather, in .env
  chat_id = "8701499393",                             -- the one chat the bot answers
  workdir = "/home/me/.shell3/workdir",               -- "" → the runtime root
  dashboard = {
    enabled = true,
    addr    = "127.0.0.1:8765",
    tunnel  = "cloudflared tunnel --url http://{addr}",  -- the scaffold default (see below)
    -- url  = "https://…",                               -- optional fixed address
  },
})
```

- **`token` / `chat_id`** are required. Keep the token in `.env` and reference it
  with `shell3.env.secret`. Only messages from `chat_id` are handled.
- **`dashboard`** serves the Mini App over HTTP on `addr`. Give it a public
  `https` address and the bot **wires the chat's menu button to it
  automatically** (`setChatMenuButton` — no BotFather step):
  - **`tunnel`** is a shell command shell3 spawns detached at start, with
    `{addr}` replaced by the dashboard addr; the first bare `https://…` URL it
    prints is used. The scaffolded default, `cloudflared tunnel --url
    http://{addr}`, works with zero account but requires
    [`cloudflared`](https://github.com/cloudflare/cloudflared) to be installed
    (e.g. `brew install cloudflared` on macOS) — swap the command for any
    tunnel that prints an https URL, or delete the line to stay local-only.
    Output goes to `~/.shell3/tunnel.log`; if no URL appears within 30s (e.g.
    the binary is missing) the bot still runs and the dashboard just stays
    local.
  - **`url`** is a fixed public address you run yourself (a stable tunnel,
    `tailscale serve`). It overrides `tunnel`.
  - Leave both empty to reach the dashboard only locally (or with
    `shell3 dash`).

## Standalone web front-end — `shell3.web`

`shell3.web{}` configures `shell3 web`: the same dashboard plus a simple chat,
served over plain HTTP with a shared secret instead of Telegram. It is the
Telegram-free fallback — it resumes the latest stored session, so a
conversation started over Telegram continues in the browser.

```lua
shell3.web({
  addr   = "127.0.0.1:8787",
  secret = shell3.env.secret("SHELL3_WEB_SECRET"),       -- required; keep it in .env
  tunnel = "cloudflared tunnel --url http://{addr}",     -- optional public https URL
  -- url = "https://…",                                  -- optional fixed address (wins over tunnel)
})
```

- **`addr` / `secret`** are required (`shell3 web` refuses to start without
  them — an empty secret never means "no auth"). Generate one with
  `openssl rand -hex 24` and put it in `.env` as `SHELL3_WEB_SECRET=…`.
- **Auth**: open `http://<addr>/?key=<secret>` once; the page stores the key
  and sends it as an `X-Auth-Token` header from then on. Every `/api/*`
  endpoint is gated on it (constant-time compare).
- **`tunnel` / `url`** have the same semantics as the Telegram dashboard's
  (tunnel command spawned at start, `{addr}` replaced, first printed
  `https://…` used; output in `~/.shell3/tunnel.log`).
- **Chat**: the dashboard's Chat tab gains a send box, a Stop button, and
  Allow/Deny cards for `on_tool_call` asks. Replies arrive by polling — a
  deliberately simple fallback transport. The bot's slash commands work here
  too (`/stop`, `/clear`, `/compact`, `/set`, `/rollback`, `/run <job>`,
  `/reload`, plus a
  web-only `/help`); command replies render as ephemeral notices, not history.
- Declared `shell3.cron` jobs keep running under `shell3 web`; the heartbeat
  does not (it's a Telegram-notification feature).
- Run **one front-end at a time** — `shell3 telegram` and `shell3 web` own the
  same runs store and history.

## Voice & images — `shell3.stt` / `shell3.tts` / `shell3.describe` / `shell3.imagegen`

Four optional top-level blocks add voice and image capability, each pointing
at a `shell3.model` by name (declaration order doesn't matter — the reference
is resolved after the whole file loads). Everything runs over the same
OpenAI-compatible surface as the rest of shell3:
`audio/transcriptions`, `audio/speech`, chat completions with an image part,
and `images/generations` — except `imagegen` with `api = "openrouter"`, which
generates through chat completions with `modalities=["image","text"]`,
OpenRouter's image-output dialect (see below).

```lua
shell3.stt{ model = "groq-whisper" }                       -- voice notes → text
shell3.tts{ model = "groq-tts", voice = "Fritz-PlayAI", mode = "inbound" }
shell3.describe{ model = "some-vision-model" }              -- only if your main model can't see images
shell3.imagegen{ model = "some-image-model", size = "1024x1024" }
```

Each may be declared **once**; a second declaration fails the load, same as
`shell3.agent`.

- **`shell3.stt{ model, language?, echo? }`** — speech-to-text. Every inbound
  Telegram voice note is transcribed before the model turn runs and the
  transcript is injected as quoted text, so the agent sees it like a normal
  message. `language` is an optional hint passed to the transcription
  endpoint. `echo` (default `true`) also sends the transcript back to the
  chat as a `📝 "…"` message, so you can see what was heard. A transcription
  failure sends the provider error to the chat as a `⚠️` notice.
- **`shell3.tts{ model, voice?, mode?, format? }`** — text-to-speech for
  outbound replies. `mode` is `"off"`, `"inbound"` (default — reply with voice
  only when the turn started from a voice note), or `"always"`. `format`
  (default `"opus"`) is the synthesized audio codec. `voice` is passed through
  to the endpoint (provider-specific voice name). Voice **replaces** the text
  reply, it's never sent in addition. The `/voice off|inbound|always` chat
  command overrides `mode` at runtime (persisted to
  `~/.shell3/voice_mode.json`, so it survives a restart); bare `/voice` shows
  a menu with the current mode. A synthesis failure falls back to the plain
  text reply plus a `⚠️` notice carrying the error.
- **`shell3.describe{ model, prompt? }`** — captions an inbound image before
  the model turn runs, for a **text-only** main model that can't see the
  image itself (a vision-capable main model doesn't need this — it already
  gets the image directly via `read_media`/inline attachment). `prompt`
  (default `"Describe the image."`) is the instruction sent with the image.
  Success injects `[image: <description>]` into the turn; on failure the
  agent still sees the file path (so it can retry with `read_media`) and the
  error is sent to the chat as a `⚠️` notice.
- **`shell3.imagegen{ model, size?, api? }`** — image generation. `size`
  (default `"1024x1024"`) is the requested output dimensions, overridable per
  call. `api` selects the wire shape used to talk to the model's `base_url`:
  `"openai"` (default) uses the OpenAI SDK's `Images.Generate`
  (`images/generations`); `"openrouter"` instead POSTs a raw chat-completions
  request with `modalities=["image","text"]` — the dialect OpenRouter's
  image-output models speak — and reads the generated image off the reply
  message as a base64 data URL. (OpenRouter also has a dedicated
  `/api/v1/images` endpoint, but it pre-authorizes the request's worst-case
  token cost — around $2 for a Gemini image model — and returns 402 on any
  lower balance; the chat route charges only actual usage, ~$0.03/image.)
  `size` is ignored on the `"openrouter"` shape (the chat route has no size
  parameter), and the saved file's extension follows the returned media type
  (png/jpg/webp), not a fixed `.png`. When declared, **every agent gets an
  `image_generate{prompt, size?}` tool** — the main agent and each subagent —
  under every front-end (`shell3 telegram`, `shell3 web`, `shell3 dev`).
  Generated files are saved to `~/.shell3/media/` and the tool returns the
  path. Under Telegram, the main agent then delivers the file with
  `send_media_telegram{path=..., kind="photo"}`; a subagent (which has no
  send tool) is told to include the path in its report instead, so the main
  agent can deliver it from the completion notice. To keep some agent from
  generating, gate it like any other tool:

  ```lua
  shell3.on_tool_call(function(t)
    if t.name == "image_generate" and t.headless then
      return { block = true, reason = "no imagegen from background jobs" }
    end
  end)
  ```

  Note: this is image generation only — OpenRouter's video-generation
  endpoint (`/api/v1/videos`) is an async job API with a different shape and
  is not wired up (a known follow-up, not a current feature).

**Media storage.** Everything the agent has seen or made lives in
`~/.shell3/media/`: inbound Telegram attachments (`tg-*` files) and generated
images (`img-*` files) alike, so every media file keeps a stable path that
survives reboots and OS temp cleaning — re-readable with `read_media`,
re-sendable with `send_media_telegram`, browsable from the dashboard's file
explorer. The one exception is synthesized TTS audio, which is sent to the
chat and deleted immediately. The folder grows until you prune it; shell3
does no automatic cleanup.

### `read_media`'s modalities

`read_media` (advertised when the agent sets `tools = { media = true }`) loads
a file from disk and attaches it as a user-message part on the next step, so a
capable model can perceive it. Supported files, by modality:

- **Images** (`.jpg`, `.jpeg`, `.png`, `.gif`, `.webp`) — an image content
  part; needs a vision-capable model.
- **Audio** (`.wav`, `.mp3`, `.ogg`/`.opus`, `.oga`) — an input-audio content part;
  needs an audio-capable model.
- **PDFs** (`.pdf`, ≤20 MB) — an OpenAI-compatible `file` content part
  (base64 `file_data`); this works on both OpenAI and OpenRouter endpoints.
- **Video** (`.mp4`, `.webm`, `.mov`, ≤40 MB) — a `video_url` content part
  (base64 data URI). This is an **OpenRouter/Gemini extension** to the
  chat-completions dialect, not part of vanilla OpenAI — plain OpenAI
  endpoints reject it, so video input needs a model/provider that accepts
  `video_url` parts.

### `send_media_telegram`'s `kind` param

`send_media_telegram` is registered only under `shell3 telegram` (there is no
web equivalent). It takes an optional `kind`: `"photo"`, `"voice"`, `"audio"`,
`"video"`, or `"document"` (default). `"voice"` requires a `.ogg`/`.opus`
file; `"video"` requires an `.mp4`/`.webm`/`.mov` file (subject to the same
50 MB global send cap as every other kind). `"photo"` is recompressed by
Telegram to roughly 1280px — use `"document"` when you need pixel-exact
delivery (e.g. a screenshot with fine text).

### Groq quickstart (one free key, STT + TTS)

Groq's free tier serves both an OpenAI-compatible transcription and a
text-to-speech model, so one key covers voice in and out:

```lua
shell3.model("groq-whisper", { base_url = "https://api.groq.com/openai/v1",
  api_key = shell3.env.secret("GROQ_API_KEY"), model = "whisper-large-v3-turbo" })
shell3.model("groq-tts", { base_url = "https://api.groq.com/openai/v1",
  api_key = shell3.env.secret("GROQ_API_KEY"), model = "playai-tts" })

shell3.stt{ model = "groq-whisper" }
shell3.tts{ model = "groq-tts", voice = "Fritz-PlayAI", mode = "inbound" }
```

Add `GROQ_API_KEY=...` to `.env`. `shell3 boot` scaffolds this block
commented out at the bottom of the model section — uncomment and fill in a
key to turn it on, then `/reload`.

## Scheduled jobs — `shell3.cron`

`shell3.cron({...})` is a top-level flat list of jobs. Each fires a subagent
(`agent` must name a declared subagent) on `schedule` (a cron expression or
`@daily`/`@hourly`/…). The scheduler runs inside `shell3 telegram`.

```lua
shell3.cron({
  { name = "daily", schedule = "@daily", agent = "explorer", notify = true,
    prompt = "Summarize anything noteworthy from the last day." },
})
```

`notify = true` wakes the chat with the result; `notify = false` delivers it
quietly for the agent's next turn. Arm a changed cron list with `/reload`; run
a job on demand with `/run <name>`.

## Heartbeat — `shell3.heartbeat`

Where cron fires a **fresh, contextless subagent** at an exact time, the
heartbeat periodically wakes the **main session** — full conversation context —
with an open-ended "anything need attention?" checklist, and stays silent
unless something does. Use it for standing awareness (inbox, calendar, "did
anything break?", following up on work the agent promised); use cron for
exact-time, isolated jobs.

```lua
shell3.heartbeat({
  every     = "30m",                 -- required; a Go duration ("30m", "1h")
  checklist = [[
- anything urgent in the inbox?
- any background work you promised earlier and haven't finished?
]],                                  -- required; the standing orders each tick carries
  active    = { from = "08:00", to = "23:00", tz = "Europe/Berlin" },
  -- prompt = "...",                 -- optional preamble override (checklist is still appended)
})
```

Mechanics: each tick that lands while the session is **idle** and inside the
`active` window injects the checklist prompt as a queued turn (the same wake
path background-job notices use). The prompt instructs the model to reply
exactly `HEARTBEAT_OK` when nothing needs attention; the bot strips that
sentinel and sends nothing, so the chat only hears real alerts. Busy or
out-of-window ticks are **skipped, not queued** — heartbeat timing is
approximate by design. Declaring a second `shell3.heartbeat` fails the load.

- `active` is optional (omit for 24/7); `from` is inclusive, `to` exclusive,
  both `"HH:MM"` 24h clock, and `from > to` spans midnight (`22:00`–`06:00`).
- `tz` is an IANA zone name for the window, defaulting to the host's local
  zone; it is validated at load time.
- `/reload` picks up heartbeat changes (a removed block stops the ticking).
- The dashboard's Status view shows the declared heartbeat — interval, active
  window, checklist — and whether the running front-end actually arms it
  (only `shell3 telegram` ticks; under `shell3 web`/`dash` it shows as inert).
- Test a checklist end-to-end with `shell3 dev --heartbeat`: it fires one tick
  and prints whether the reply would be suppressed or delivered.

## Skills

A skill is a plain `.md` file the agent reads with `cat` when it's relevant —
there is no `skill` tool and no Lua declaration. An agent lists directories
(`skills = { "lib/skills" }`, resolved relative to `shell3.lua`), and every
`*.md` inside (non-recursive) becomes one skill. A file is markdown with YAML
frontmatter — a required `description` (the one-liner the agent sees and uses
to decide whether to read the body) and an optional `name` defaulting to the
filename:

```markdown
---
description: Planning + approval gate before any non-trivial change.
---
When asked for a non-trivial change, first...
```

Adding a skill is dropping a file into a listed dir and `/reload`-ing. A
missing directory fails the load; a file the loader can't use (no
frontmatter/`description`, empty body, duplicate name) is skipped with a
warning — `shell3 health` turns those warnings into hard errors. Granted
skills are indexed by absolute path in the system prompt under `## Skills`;
the [cookbook](cookbook/) ships ready-to-use ones.

## Putting it together

For a full example, read the base config `boot` writes —
`~/.shell3/shell3.lua`. For drop-in additions (extra subagents, more skills, the
browser skill, proxy and sandbox setups), see the
[cookbook](cookbook/README.md).
