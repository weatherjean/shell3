# Configuration

Everything shell3 does is decided by one file: `shell3.lua`. Plain Lua —
versionable, diffable, programmable. `shell3 boot` writes a working config;
this page is for going beyond it.

`boot` creates three things under `~/.shell3/`:

- `shell3.lua` — models, the agent, subagents, tools, skills, host blocks.
- `lib/` — modules the config `require`s, and `lib/skills/` markdown skills.
- `.env` — secrets (API keys, tokens). **Never commit this file.**

`--config`/`-c` takes a **name** (`-c code` → `~/.shell3/code.lua`) unless the
value ends in `.lua`, which is a literal path. The working directory is never
consulted, so behavior doesn't depend on where you launch from.

## Models

A model is an endpoint plus the parameters shell3 sends it. Any
OpenAI-compatible endpoint works:

```lua
shell3.model("main", {
  base_url       = "https://api.openai.com/v1",
  api_key        = shell3.env.secret("MAIN_API_KEY"),  -- read from .env
  model          = "gpt-5.2",
  context_window = 128000,   -- the model's REAL token budget
  compact_at     = 100000,   -- auto-compact threshold; 0 = off
  -- reasoning   = "medium", -- if the model supports reasoning effort
})
```

Set `context_window` to the model's actual budget — a wrong number skews the
context-usage reminders and the compaction trigger.

### Context management

When a turn's prompt crosses `compact_at` tokens, shell3 summarizes the head
of the conversation and keeps a verbatim recent tail. Host-managed: there are
no model-driven prune/compact tools. Two optional knobs:

```lua
keep_recent = 33000,  -- verbatim tail (tokens); default compact_at * 0.33
                      --   (clamped to compact_at * 0.5 if set at/above compact_at)
prune_at    = 60000,  -- cheaper first tier: stub old tool outputs, no LLM call
                      --   default compact_at * 0.6; 0 (or ≥ compact_at) disables
```

Agents and subagents can skip the prune tier individually with
`prune = false` (the thresholds stay on the model; omitted/`true` inherits).

On demand: `/compact` forces one compaction (replies with the token delta);
`/clear` hard-resets the conversation — refused while background tasks run, so
a finishing task can't leak into the fresh session (`/stop` first).

### Provider-specific knobs — `extra`

Keys in `extra` are injected verbatim into the top-level request JSON:

```lua
extra = { reasoning_split = true },               -- MiniMax: thinking → reasoning_content
extra = { verbosity = "high" },                   -- gpt-5-style verbosity
extra = { provider = { order = {"anthropic"} } }, -- OpenRouter routing (nesting works)
```

Only set it when needed — strict endpoints reject unknown fields. More in
[cookbook/models.md](cookbook/models.md).

### Local proxies — `run_proxy`

If a model needs a shim in front of its endpoint (a Codex subscription via
`npx`, a litellm gateway), set `run_proxy`. shell3 starts the command
detached, fire-and-forget, on the model's first use; logs go to
`~/.shell3/proxy-<model>.log`. If a proxy is already listening, the spawn just
fails to bind and the first request proceeds against it.

```lua
shell3.model("codex", {
  run_proxy = "npx @some/codex-proxy --port 8787",
  base_url  = "http://localhost:8787/v1",
  -- ...
})
```

## The agent

An agent is a name, a model, a system prompt, and a set of tools. **Exactly
one `shell3.agent({...})` may be declared** — a second fails the load;
specialists are [subagents](#subagents--delegation).

```lua
shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash      = true,
    bash_bg   = true,          -- background / long-running commands
    edit      = true,          -- the edit_file tool
    media     = true,          -- read_media: images, audio, PDFs, video
    custom    = { my_tool },   -- Lua-declared tools (below)
    subagents = { explorer },  -- delegatable specialists
  },
  skills = { "lib/skills" },   -- skill directories (below)
})
```

There are **no file-read tools**: the agent reads with `cat`/`sed -n`, lists
with `ls`/`find`, searches with `rg` — all through `bash` (a hallucinated
`read`/`grep` call gets an error redirecting it back to bash/edit_file). A
read-only agent is a policy, not a tool set: gate `bash` in
[`on_tool_call`](#the-command-gate--on_tool_call).

## Subagents & delegation

A subagent is a delegatable specialist: same shape as an agent plus a
`description` — what the parent model reads when deciding to delegate. Only an
agent that lists it under `tools.subagents` may spawn it. Agents and subagents
share one namespace; a duplicate name auto-suffixes (`explorer` → `explorer2`)
instead of failing the load.

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
  delegation = true,                          -- advertise the task tools
  tools      = { subagents = { explorer } },  -- who this agent may spawn
  -- ...
})
```

With **both** `delegation = true` and a non-empty `tools.subagents`, the agent
gets four tools: `task` (spawn: `{subagent_type, prompt, description}`;
returns immediately), `task_list`, `task_status <id>`, `task_cancel <id>`.
The allowed subagents — names plus descriptions — are baked into the `task`
tool's schema (an enum on `subagent_type`), so no per-turn reminder is spent.
One toggle without the other advertises nothing.

A spawned subagent is an **in-process background job** (a child-session
goroutine, not a subprocess); on completion the parent wakes with a capped
result summary in its context. Subagents run headless (an `{ask=…}` gate
verdict auto-denies), and delegation is single-level by construction — a
subagent never gets the `task` tool.

`bash_bg` runs on the same job runtime but is gated separately by
`tools = { bash_bg = true }`. A nonzero exit **wakes** an idle agent so
failures surface proactively; a clean exit queues its notice for the next
turn. A subagent's still-running `bash_bg` job keeps its session open past its
main turn; each completion resumes the subagent for a follow-up turn whose
summary reaches the main agent as a notice (capped at 5 per subagent — past
the cap, or after cancel, the raw job notice is delivered instead, so no
completion is lost). `task_cancel <sub-id>` cascades to the jobs that subagent
started. One config-global knob caps it all:

```lua
shell3.background({ max_concurrent = 8 })  -- concurrent background jobs (default 8)
```

## Custom tools

A custom tool is **not** a Lua function — it's a bash command template.
Declared parameters arrive in the command's environment as lowercase
`$`-named variables; declared `secrets` are exported too (and stay out of the
command string). Stdout is what the model sees.

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

Two habits keep tools safe: `curl --data-urlencode` for any model-supplied
parameter (never interpolate model text into a URL), and shape output with
`jq` so the model gets a clean line, not a JSON wall.

Optional fields: `background = true` (runs as an in-process background job,
like `bash_bg` — dashboard jobs view, completion notice on a later turn) and
`timeout = N` (seconds, foreground only). Full template in
[cookbook/lib/tools.lua](cookbook/lib/tools.lua).

> **Secrets note:** declared `secrets` ride the command's process environment.
> On a shared host, same-user processes can read it (`/proc/<pid>/environ`).
> Fine single-user; on a multi-user box, treat tool secrets as readable by
> anything that user runs. More in [security.md](security.md).

## The command gate — `on_tool_call`

shell3 is **unsafe by default**: nothing is gated until you register a
handler. `shell3.on_tool_call(fn)` fires before **every** tool — `bash`,
`bash_bg`, `edit_file`, `read_media`, and custom tools — and your handler
decides per tool by switching on `t.name`. Handlers are **chainable**:
declaration order, first terminal verdict wins.

Each handler receives a table `t`:

| Field | Description |
|-------|-------------|
| `t.name` | The real tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, or a custom tool's name. |
| `t.command` | The bash command string — the two bash tools only; **nil** for every other tool. |
| `t.args` | Raw arguments JSON (every tool). Gate non-bash tools by inspecting this. |
| `t.headless` | `true` when no human is attached (subagents, cron jobs) — an `{ask=…}` verdict would auto-deny. |

And returns one of:

| Return | Effect |
|--------|--------|
| `nil` | Pass; next handler (or run). |
| `{ command = "…" }` | Rewrite the bash command; continue the chain. Bash tools only — fails closed elsewhere. |
| `{ argv = { … } }` | **Terminal**: exec exactly this argv (runner swap). `bash`/`bash_bg` only. |
| `{ block = true, reason = "…" }` | **Terminal**: block; `reason` goes to the model. Any tool. |
| `{ ask = "prompt", reason = "…", ask_timeout = N }` | Ask a human (inline Allow/Deny in Telegram); declined/headless/timeout → block. Any tool. Timeout defaults to 300 s. |

A handler that raises a Lua error **fails closed** (blocks), as does a
returned table with none of the recognized keys.

### Denylists with `shell3.regex`

`shell3.regex(pattern)` compiles Go RE2 **at config load** — a bad pattern is
a load error. `:match(s)` is unanchored. Use `(?s)` so `.*` spans newlines,
and match the whole `t.command` so chaining can't hide a flagged fragment
(`echo hi; rm -rf /` still matches `rm\s+-rf`).

```lua
local re   = shell3.regex
local HARD = { re([[(?s)rm\s+-rf\s+/]]), re([[(?s)mkfs]]), re([[(?s)dd\s+if=]]) }
local ASK  = { re([[(?s)rm\s+-rf]]), re([[(?s)\bgit\s+push]]), re([[(?s)curl\b.*\|\s*(ba)?sh]]) }
local ENV  = re([[\.env]])

shell3.on_tool_call(function(t)
  -- Guard REQUIRED: t.command is nil for non-bash tools; matching it without
  -- the check errors (→ fail closed).
  if t.name == "bash" or t.name == "bash_bg" then
    for _, p in ipairs(HARD) do
      if p:match(t.command) then return { block = true, reason = "hard_deny" } end
    end
    for _, p in ipairs(ASK) do
      if p:match(t.command) then
        -- Headless: an ask would auto-deny; block with an actionable reason.
        if t.headless then return { block = true, reason = "needs approval; rerun interactively" } end
        return { ask = "Run?\n" .. t.command, reason = "denied" }
      end
    end
  end
  -- Gate non-bash tools by name + args, e.g. protect the secrets file:
  if t.name == "edit_file" and ENV:match(t.args) then
    return { block = true, reason = "no editing .env" }
  end
end)
```

There's no allowlist: ordinary reads (`cat`, `rg`, `ls`) match nothing and
just run — a headless subagent explores freely; only what you gate is
affected. An ask nobody answers denies after `ask_timeout`. `{block=true}`
never prompts — it blocks everywhere.

### Runner swap (container, SSH, firejail)

`{ argv = { … } }` chooses the program that runs the agent's command; the
command arrives as one argv element, so nothing re-parses or re-quotes it:

```lua
shell3.on_tool_call(function(t)
  if t.name == "bash" or t.name == "bash_bg" then
    return { argv = {"docker", "exec", "mycontainer", "bash", "-c", t.command} }
  end
end)
```

A malformed argv (empty, or any non-string element) fails **closed**. A
custom tool's command is your trusted template, never rewritten — but the
tool **call** still fires the gate by name, so you can `block`/`ask` it.
Recipes in [cookbook/sandbox.md](cookbook/sandbox.md).

### Tool-result rewriting — `on_tool_result`

The symmetric post-execution hook: fires for every tool with `r.name`,
`r.args`, `r.output`; return `{ output = "…" }` to replace what the model
sees, `nil` to pass through. Primary use is secret redaction:

```lua
shell3.on_tool_result(function(r)
  return { output = (r.output:gsub("API_KEY=%S+", "API_KEY=[redacted]")) }
end)
```

Errors here fail **open** — a broken rewriter must not destroy tool output —
so a throwing redactor lets the unredacted output through. Keep redactors
simple and total. Background jobs (`bash_bg`, backgrounded custom tools) are
out of scope: the hook sees only the "started job…" pointer, not the
process's streamed output — redact at the source if a background command can
emit secrets.

## Telegram host — `shell3.telegram`

The bot answers exactly one `chat_id` and runs the one configured agent.

```lua
shell3.telegram({
  token   = shell3.env.secret("TELEGRAM_BOT_TOKEN"),  -- from @BotFather, in .env
  chat_id = "8701499393",
  workdir = "/home/me/.shell3/workdir",               -- "" → the runtime root
  dashboard = {
    enabled = true,
    addr    = "127.0.0.1:8765",
    tunnel  = "cloudflared tunnel --url http://{addr}",  -- scaffold default
    -- url  = "https://…",                               -- fixed address (wins over tunnel)
  },
})
```

The dashboard serves the Mini App on `addr`; given a public https address the
bot wires the chat's menu button to it automatically (no BotFather step).
`tunnel` is a shell command spawned detached at start (`{addr}` substituted);
the first bare `https://…` URL it prints is used, output goes to
`~/.shell3/tunnel.log`, and if no URL appears within 30 s the bot still runs
with a local-only dashboard. The default needs
[`cloudflared`](https://github.com/cloudflare/cloudflared) installed — swap in
any tunnel that prints an https URL, set a fixed `url` (a stable tunnel,
`tailscale serve`), or delete both to stay local.

## Standalone web front-end — `shell3.web`

Configures `shell3 web`: the same dashboard plus a simple chat over plain
HTTP, gated by a shared secret instead of Telegram.

```lua
shell3.web({
  addr   = "127.0.0.1:8787",
  secret = shell3.env.secret("SHELL3_WEB_SECRET"),   -- required; boot generates one
  tunnel = "cloudflared tunnel --url http://{addr}", -- optional, same semantics as above
  -- url = "https://…",
})
```

`addr` and `secret` are required — an empty secret never means "no auth".
Open `http://<addr>/?key=<secret>` once; the page stores the key and sends it
as `X-Auth-Token` (constant-time compare) on every `/api/*` call. Cron keeps
running under `shell3 web`; the heartbeat doesn't (it's a
Telegram-notification feature). Run one front-end at a time — `telegram` and
`web` own the same history. Chat details in [cli.md](cli.md#shell3-web--standalone-web-front-end).

## Voice & images — `shell3.stt` / `tts` / `describe` / `imagegen`

Four optional top-level blocks, each pointing at a `shell3.model` by name
(declaration order doesn't matter; each may be declared once). All speak the
same OpenAI-compatible surface: `audio/transcriptions`, `audio/speech`, chat
completions with an image part, `images/generations`.

```lua
shell3.stt{ model = "groq-whisper" }                       -- voice notes → text
shell3.tts{ model = "groq-tts", voice = "Fritz-PlayAI", mode = "inbound" }
shell3.describe{ model = "some-vision-model" }             -- for text-only main models
shell3.imagegen{ model = "some-image-model", size = "1024x1024" }
```

- **`stt{ model, language?, echo? }`** — every inbound voice note is
  transcribed before the turn runs and injected as quoted text. `echo`
  (default `true`) also sends a `📝 "…"` transcript to the chat. Failures
  surface as `⚠️` notices.
- **`tts{ model, voice?, mode?, format? }`** — speaks outbound replies.
  `mode`: `"off"`, `"inbound"` (default — voice reply only to a voice
  message), `"always"`; overridable at runtime with `/voice` (persisted).
  `format` defaults to `"opus"` (Telegram voice bubbles). Voice **replaces**
  the text reply; a synthesis failure falls back to text plus a `⚠️` notice.
- **`describe{ model, prompt? }`** — captions an inbound image before the
  turn. Success injects `[image: <description>]`; on failure the agent still
  sees the file path and can retry with `read_media`. Point it at a vision
  model when the main model is text-only — or at the main model itself so it
  sees a caption without a `read_media` round-trip (`shell3 boot` wires this
  when you answer that your model has vision).
- **`imagegen{ model, size?, api? }`** — adds an `image_generate{prompt,
  size?}` tool to **every** agent (main and subagents, under every
  front-end). `api = "openai"` (default) uses `images/generations`;
  `"openrouter"` POSTs a chat-completions request with
  `modalities=["image","text"]` — OpenRouter's image-output dialect — and
  reads the image off the reply (its dedicated `/api/v1/images` endpoint
  pre-authorizes worst-case cost, ~$2, and 402s low balances; the chat route
  charges actual usage, ~$0.03/image; `size` is ignored on this shape).
  Generated files land in `~/.shell3/media/` and the tool returns the path;
  under Telegram the main agent delivers it with `send_media_telegram`, while
  a subagent reports the path for the parent to deliver. Gate it like any
  tool (`t.name == "image_generate"`).

**Media storage.** Inbound Telegram attachments (`tg-*`) and generated images
(`img-*`) live in `~/.shell3/media/` — stable paths that survive reboots,
re-readable with `read_media`, re-sendable, browsable in the dashboard.
Synthesized TTS audio is the exception (sent and deleted). The folder grows
until you prune it.

**`read_media` modalities** (needs `tools = { media = true }`): images
(`.jpg/.jpeg/.png/.gif/.webp`, vision models), audio
(`.wav/.mp3/.ogg/.opus/.oga`, audio models), PDFs (`.pdf` ≤ 20 MB, an
OpenAI-compatible `file` part — works on OpenAI and OpenRouter), and video
(`.mp4/.webm/.mov` ≤ 40 MB, a `video_url` part — an OpenRouter/Gemini
extension plain OpenAI endpoints reject).

**`send_media_telegram`** (Telegram only) takes `kind`: `"photo"`
(recompressed to ~1280px), `"voice"` (`.ogg`/`.opus`), `"audio"`, `"video"`
(`.mp4`/`.webm`/`.mov`), or `"document"` (default — pixel-exact). 50 MB cap.

Provider recipes — a one-key Groq quickstart for STT+TTS, the OpenRouter
variant — live in [cookbook/voice-images.md](cookbook/voice-images.md).

## Scheduled jobs — `shell3.cron`

A top-level flat list; each job fires a declared **subagent** on `schedule`
(cron expression or `@daily`/`@hourly`/…). The scheduler runs inside
`shell3 telegram` and `shell3 web`.

```lua
shell3.cron({
  { name = "daily", schedule = "@daily", agent = "explorer", notify = true,
    prompt = "Summarize anything noteworthy from the last day." },
})
```

`notify = true` wakes the chat with the result; `false` queues it quietly for
the agent's next turn (failures always wake). `/reload` arms a changed list;
`/run <name>` fires a job on demand.

## Heartbeat — `shell3.heartbeat`

Cron fires a fresh, contextless subagent at an exact time; the heartbeat
periodically wakes the **main session** — full conversation context — with a
standing checklist, and stays silent unless something needs attention. Use it
for standing awareness (inbox, "did anything break?", promised follow-ups);
use cron for exact-time isolated jobs.

```lua
shell3.heartbeat({
  every     = "30m",                 -- required; Go duration
  checklist = [[
- anything urgent in the inbox?
- any background work you promised and haven't finished?
]],                                  -- required
  active    = { from = "08:00", to = "23:00", tz = "Europe/Berlin" },
  -- prompt = "...",                 -- optional preamble override
})
```

Each tick that lands while the session is **idle** and inside the `active`
window injects the checklist as a queued turn. The model replies exactly
`HEARTBEAT_OK` when nothing needs attention; the bot strips the sentinel and
sends nothing. Busy or out-of-window ticks are **skipped, not queued** —
timing is approximate by design.

- `active` is optional (omit for 24/7); `from` inclusive, `to` exclusive,
  `"HH:MM"`; `from > to` spans midnight. `tz` is an IANA zone (validated at
  load), default host-local.
- `/reload` picks up changes; a removed block stops the ticking. Only
  `shell3 telegram` ticks — the dashboard's Status view shows the declared
  heartbeat and whether the running front-end arms it.
- Test end-to-end with `shell3 dev --heartbeat`: fires one tick and prints
  whether the reply would be suppressed or delivered.

## Skills

A skill is a plain `.md` file the agent reads with `cat` when relevant — no
`skill` tool, no Lua declaration. An agent lists directories
(`skills = { "lib/skills" }`, relative to `shell3.lua`); every `*.md` inside
(non-recursive) becomes one skill. Frontmatter needs a `description` (the
one-liner the agent uses to decide whether to read the body); `name` defaults
to the filename:

```markdown
---
description: Planning + approval gate before any non-trivial change.
---
When asked for a non-trivial change, first...
```

Adding a skill = drop a file in a listed dir + `/reload`. A missing directory
fails the load; an unusable file (no frontmatter/description, empty body,
duplicate name) is skipped with a warning — `shell3 health` hardens those
into errors. Granted skills are indexed by absolute path in the system prompt
under `## Skills`.

## Putting it together

Read the base config `boot` writes (`~/.shell3/shell3.lua`) for a full
example; the [cookbook](cookbook/README.md) has drop-in extras — subagents,
skills, proxy and sandbox setups.
