# Configuration

Your config is a **directory** (default `~/.shell3/`), and it follows four
rules:

1. **YAML wires it** — connections and knobs live in `shell3.yaml`.
2. **Markdown prompts it** — anything with a prompt body is a `.md` file with
   frontmatter.
3. **Files enable it** — a feature is on because its file exists, off because
   it doesn't. No enable flags.
4. **One script gates it** — policy is a bash hook script, not a config
   language.

`shell3 boot` writes a working tree; this page is for going beyond it.

```
~/.shell3/
  shell3.yaml            # wiring: models, telegram, web, mcp, media
  .env                   # secrets — never commit this file
  agent.md               # THE agent: frontmatter (model, tools) + prompt body
  agents/<name>.md       # subagents; the file IS the registration
  skills/<name>.md       # skills; drop a file in, /reload
  hooks/tool-call.sh     # command gate for the main agent
  hooks/<name>.tool-call.sh   # command gate for subagent <name>
  hooks/*.tool-result.sh # output rewriters (same per-agent split)
  cron/<name>.md         # scheduled jobs
  heartbeat.md           # periodic check-in checklist
```

`--config`/`-c` takes a path to a config directory; omitted, it's `~/.shell3`.
The working directory is never consulted, so behavior doesn't depend on where
you launch from.

Secrets are referenced from YAML as `env:KEY` — resolved from the `.env`
beside `shell3.yaml`, anywhere inside a string value (`"Bearer env:LINEAR_KEY"`
works). An `env:` reference naming a missing key fails the load.

## Models

A model is an endpoint plus the parameters shell3 sends it. Any
OpenAI-compatible endpoint works:

```yaml
models:
  main:
    base_url: https://api.openai.com/v1
    api_key: env:MAIN_API_KEY      # read from .env
    model: gpt-5.2
    context_window: 128000         # the model's REAL token budget
    compact_at: 100000             # auto-compact threshold; 0 = off
    # reasoning: medium            # if the model supports reasoning effort
    # temperature: 0.7             # omitted = leave the provider default
    # max_tokens: 4096             # cap on a single reply; omitted = adapter default
```

Set `context_window` to the model's actual budget — a wrong number skews the
context-usage reminders and the compaction trigger.

### Context management

When a turn's prompt crosses `compact_at` tokens, shell3 summarizes the head
of the conversation and keeps a verbatim recent tail. Host-managed: there are
no model-driven prune/compact tools. Two optional knobs:

```yaml
    keep_recent: 33000   # verbatim tail (tokens); default compact_at * 0.33
    prune_at: 60000      # cheaper first tier: stub old tool outputs, no LLM call
                         #   default compact_at * 0.6; 0 (or ≥ compact_at) disables;
                         #   setting it without compact_at is a load error
```

The agent (and any subagent) can skip the prune tier individually with
`prune: false` in its frontmatter (the thresholds stay on the model;
omitted/`true` inherits).

On demand: `/compact` forces one compaction (replies with the token delta);
`/clear` hard-resets the conversation — refused while background tasks run, so
a finishing task can't leak into the fresh session (`/stop` first).

### Provider-specific knobs — `extra`

Keys in `extra` are injected verbatim into the top-level request JSON:

```yaml
    extra: { reasoning_split: true }                 # MiniMax: thinking → reasoning_content
    extra: { verbosity: high }                       # gpt-5-style verbosity
    extra: { provider: { order: [anthropic] } }      # OpenRouter routing (nesting works)
```

Only set it when needed — strict endpoints reject unknown fields. More in
[cookbook/models.md](cookbook/models.md).

### Local proxies — `run_proxy`

If a model needs a shim in front of its endpoint (a Codex subscription via
`npx`, a litellm gateway), set `run_proxy`. shell3 starts the command
detached, fire-and-forget, on the model's first use; logs go to
`~/.shell3/proxy-<model>.log`. If a proxy is already listening, the spawn just
fails to bind and the first request proceeds against it.

```yaml
models:
  codex:
    run_proxy: "npx @some/codex-proxy --port 8787"
    base_url: http://localhost:8787/v1
    # ...
```

## The agent — `agent.md`

The agent is one markdown file: frontmatter for the wiring, body for the
system prompt. There is exactly one agent because there is exactly one
`agent.md` — specialists are [subagents](#subagents--delegation).

```markdown
---
model: main
tools: [bash, bash_bg, edit, media]
---
You are a careful pair-programmer…
```

Frontmatter keys: `model` (required), `tools` (any of `bash`, `bash_bg`,
`edit`, `media`), `mcp` (see [MCP](#mcp-servers)), `prune`.

There are **no file-read tools**: the agent reads with `cat`/`sed -n`, lists
with `ls`/`find`, searches with `rg` — all through `bash` (a hallucinated
`read`/`grep` call gets an error redirecting it back to bash/edit_file). A
read-only agent is a policy, not a tool set: gate `bash` in its
[hook script](#the-command-gate--hookssh).

## Subagents & delegation

A subagent is a delegatable specialist: one file in `agents/`. The filename is
its name; the file is the registration — the main agent can spawn every
subagent in the directory, and the `task` tools appear automatically as soon
as `agents/` is non-empty (no toggle). `description` is required: it's what
the main model reads when deciding to delegate.

```markdown
---
description: Read-only investigation of the codebase. No edits.
tools: [bash]
---
You are a focused code explorer…
```

`model` is optional (defaults to the main agent's). With at least one
subagent, the agent gets four tools: `task` (spawn: `{subagent_type, prompt,
description}`; returns immediately), `task_list`, `task_status <id>`,
`task_cancel <id>`. The subagent names and descriptions are baked into the
`task` tool's schema (an enum on `subagent_type`), so no per-turn reminder is
spent.

A spawned subagent is an **in-process background job** (a child-session
goroutine, not a subprocess); on completion the parent wakes with a capped
result summary in its context. Subagents run headless (an `ask` gate verdict
auto-denies), and delegation is single-level by construction — a subagent
never gets the `task` tool.

`bash_bg` runs on the same job runtime but is gated separately by `bash_bg`
in `tools`. A nonzero exit **wakes** an idle agent so failures surface
proactively; a clean exit queues its notice for the next turn — unless the
call set `force_wake: true`, which makes clean completions wake too (for
jobs whose result the agent wants to act on immediately). A subagent's
still-running `bash_bg` job keeps its session open past its main turn; each
completion resumes the subagent for a follow-up turn whose summary reaches the
main agent as a notice (capped at 5 per subagent — past the cap, or after
cancel, the raw job notice is delivered instead, so no completion is lost).
`task_cancel <sub-id>` cascades to the jobs that subagent started. One global
knob caps it all:

```yaml
background:
  max_concurrent: 8    # concurrent background jobs (default 8)
```

## Scripts & secrets

There is no custom-tool declaration: reusable glue is a **script** the agent
runs through `bash`, documented by a skill when it needs one. The scaffold
ships a `scripting` skill that teaches the pattern — reusable scripts live in
`~/.shell3/lib/bin/`, and a script that needs an API key reads it from
`~/.shell3/.env` itself, at point of use:

```bash
key="$(grep '^WEATHER_API_KEY=' ~/.shell3/.env | cut -d= -f2-)"
```

The secret enters exactly one process for exactly one call and never appears
in the conversation. Pair it with the hook example's `.env` deny (block
commands that read `.env` directly) and, if you like, a
[`tool-result.sh`](#output-rewriting--tool-resultsh) redaction as backstop.
More in [security.md](security.md).

## MCP servers

For tools that live behind the [Model Context Protocol](https://modelcontextprotocol.io),
shell3 ships a tools-only MCP client (official Go SDK): stdio and streamable
HTTP transports, no OAuth/resources/prompts (a remote server that needs auth
takes a bearer header from `.env`). Declare servers once in `shell3.yaml`;
each agent opts in via `mcp:` in its frontmatter:

```yaml
mcp:
  github:
    command: [github-mcp-server, stdio]        # stdio: argv list
    env: { GITHUB_TOKEN: env:GITHUB_TOKEN }
  linear:
    url: https://mcp.linear.app/mcp            # streamable HTTP
    headers: { Authorization: "Bearer env:LINEAR_KEY" }
    timeout: 30                    # seconds, connect + per call (default 10)
    allow: [search_issues, get_issue]   # or deny: [...] (not both)
```

```markdown
---
model: main
tools: [bash]
mcp: [github, linear]     # or mcp: all; omitted = NO MCP tools
---
```

Servers connect at startup (and on `/reload`), in parallel, each under its
own timeout; their tools join the opted-in agents' tool lists as
`mcp_<server>_<tool>` (`mcp_github_search_issues`). A server that is down
loads as a **warning** — the bot still starts, that server's tools are just
absent until the next reload — while `shell3 health` treats it as a failure
and reports each server's state. The dashboard's Status view lists every
server (up/down, tool count, last error). At call time a dead server gets one
automatic reconnect; if that fails too the model sees the error as tool
output and adapts — a broken server never kills a turn.

MCP calls flow through the same [tool-call hook](#the-command-gate--hookssh)
as everything else: `name` is the prefixed tool name and `command` is null, so
gate them by name.

## The command gate — `hooks/*.sh`

shell3 is **unsafe by default**: nothing is gated until a hook script exists.
Hooks are per-agent, with no fallback or chaining — each agent is governed by
exactly one script per kind, or none:

- `hooks/tool-call.sh` — the main agent.
- `hooks/<name>.tool-call.sh` — subagent `<name>` (including when cron
  dispatches it). A subagent with no hook file runs **ungated**; the main
  hook never applies to it.

The split keeps each script trivial: the explorer's gate is a three-line
"allow rg/cat/ls, block the rest" instead of one shared script branching on
agent identity. A hook file whose `<name>` matches no subagent is a warning
(`shell3 health` fails on it — it's usually a typo).

Every tool call — `bash`, `bash_bg`, `edit_file`, `read_media`, host tools
like `image_generate`, and `mcp_*` — runs the governing script as
`bash hooks/….sh` with JSON on stdin:

```json
{"name": "bash", "command": "rm -rf /", "args": "{…}", "headless": false}
```

| Field | Description |
|-------|-------------|
| `name` | The real tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, `"image_generate"`, `"mcp_…"`. |
| `command` | The bash command string — the two bash tools only; **null** for every other tool. |
| `args` | Raw arguments JSON (every tool). Gate non-bash tools by inspecting this. |
| `headless` | `true` when no human is attached (subagents, cron jobs) — an ask verdict would auto-deny. |

The script prints a verdict to stdout:

| Output | Effect |
|--------|--------|
| empty or `{}` | Run. |
| `{"block": true, "reason": "…"}` | Block; `reason` goes to the model. Any tool. |
| `{"ask": "prompt", "reason": "…", "ask_timeout": N}` | Ask a human (inline Allow/Deny in Telegram or the web chat); declined/headless/timeout → block. Any tool. Timeout defaults to 300 s. |
| `{"command": "…"}` | Rewrite the bash command. Bash tools only — fails closed elsewhere. |
| `{"argv": ["…"]}` | Exec exactly this argv (runner swap). `bash`/`bash_bg` only. |

A script that exits nonzero, prints malformed JSON, or runs past 10 s **fails
closed** (blocks, with the failure as the reason). The script's cwd is the
config directory. Compose everything in the one script; there is no chain.

The scaffold ships `hooks/tool-call.sh` with a full example gate (hard-deny
`rm -rf /`, ask on `git push`, block `.env` reads) **commented out** — `jq`
makes the JSON handling clean:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  case "$cmd" in
    *'rm -rf /'*|*mkfs*|*'dd if='*)
      printf '{"block": true, "reason": "hard_deny"}'; exit 0 ;;
    *'git push'*)
      printf '{"ask": "Run?\n%s", "reason": "denied"}' "$cmd"; exit 0 ;;
    *.env*)
      printf '{"block": true, "reason": "read secrets via a lib/bin script (scripting skill)"}'; exit 0 ;;
  esac
fi
exit 0
```

There's no allowlist by default: ordinary reads (`cat`, `rg`, `ls`) match
nothing and just run; only what you gate is affected. A hook is any program
bash can start — exec into Python if a gate outgrows shell.

### Runner swap (container, SSH, firejail)

`{"argv": […]}` chooses the program that runs the agent's command; the
command arrives as one argv element, so nothing re-parses or re-quotes it:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  jq -cn --arg cmd "$cmd" '{"argv": ["docker", "exec", "mycontainer", "bash", "-c", $cmd]}'
fi
```

A malformed argv (empty, or any empty element) fails **closed**. Recipes in
[cookbook/sandbox.md](cookbook/sandbox.md).

### Output rewriting — `tool-result.sh`

The symmetric post-execution hook: `hooks/tool-result.sh` (main agent) /
`hooks/<name>.tool-result.sh` (subagent) receives
`{"name": …, "args": …, "output": …}` on stdin; print `{"output": "…"}` to
replace what the model sees, `{}` or nothing to pass through. Primary use is
secret redaction:

```bash
in=$(cat)
printf '%s' "$in" | jq -c '{output: (.output | gsub("API_KEY=\\S+"; "API_KEY=[redacted]"))}'
```

A failing script here also fails **closed**: the tool output is replaced by an
error notice, never passed through unredacted. Background jobs (`bash_bg`)
are out of scope: the hook sees only the "started job…" pointer, not the
process's streamed output — redact at the source if a background command can
emit secrets.

## Telegram host — `telegram:`

The bot answers exactly one `chat_id` and runs the one configured agent.

```yaml
telegram:
  token: env:TELEGRAM_BOT_TOKEN     # from @BotFather, in .env
  chat_id: "8701499393"
  workdir: /home/me/.shell3/workdir # optional; default = the config dir
  dashboard:
    addr: 127.0.0.1:8765
    tunnel: "cloudflared tunnel --url http://{addr}"   # scaffold default
    # url: https://…                                   # fixed address (wins over tunnel)
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

## Standalone web front-end — `web:`

Configures `shell3 web`: the same dashboard plus a simple chat over plain
HTTP, gated by a shared secret instead of Telegram.

```yaml
web:
  addr: 127.0.0.1:8787
  secret: env:SHELL3_WEB_SECRET     # required; boot generates one
  # tunnel: "cloudflared tunnel --url http://{addr}"   # optional, as above
  # url: https://…
```

`addr` and `secret` are required — an empty secret never means "no auth".
Open `http://<addr>/?key=<secret>` once; the page stores the key and sends it
as `X-Auth-Token` (constant-time compare) on every `/api/*` call. Cron keeps
running under `shell3 web`; the heartbeat doesn't (it's a
Telegram-notification feature). Run one front-end at a time — `telegram` and
`web` own the same history. Chat details in [cli.md](cli.md#shell3-web--standalone-web-front-end).

## Voice & images — `media:`

Four optional blocks under `media:`, each pointing at a model by name. All
speak the same OpenAI-compatible surface: `audio/transcriptions`,
`audio/speech`, chat completions with an image part, `images/generations`.

```yaml
media:
  stt: { model: groq-whisper }                    # voice notes → text
  tts: { model: groq-tts, voice: Fritz-PlayAI, mode: inbound }
  describe: { model: some-vision-model }          # for text-only main models
  imagegen: { model: some-image-model, size: 1024x1024 }
```

- **`stt: { model, language?, echo? }`** — every inbound voice note is
  transcribed before the turn runs and injected as quoted text. Set
  `echo: true` to also send a `📝 "…"` transcript to the chat (default
  `false`). Failures surface as `⚠️` notices.
- **`tts: { model, voice?, mode?, format? }`** — speaks outbound replies.
  `mode`: `off`, `inbound` (default — voice reply only to a voice message),
  `always`; overridable at runtime with `/voice` (persisted). `format`
  defaults to `opus` (Telegram voice bubbles). Voice **replaces** the text
  reply; a synthesis failure falls back to text plus a `⚠️` notice.
- **`describe: { model, prompt? }`** — captions an inbound image before the
  turn. Success injects `[image: <description>]`; on failure the agent still
  sees the file path and can retry with `read_media`. Point it at a vision
  model when the main model is text-only — or at the main model itself so it
  sees a caption without a `read_media` round-trip (`shell3 boot` wires this
  when you answer that your model has vision).
- **`imagegen: { model, size?, api? }`** — adds an `image_generate{prompt,
  size?}` tool to **every** agent (main and subagents, under every
  front-end). `api: openai` (default) uses `images/generations`;
  `openrouter` POSTs a chat-completions request with
  `modalities=["image","text"]` — OpenRouter's image-output dialect — and
  reads the image off the reply (its dedicated `/api/v1/images` endpoint
  pre-authorizes worst-case cost, ~$2, and 402s low balances; the chat route
  charges actual usage, ~$0.03/image; `size` is ignored on this shape).
  Generated files land in `~/.shell3/media/` and the tool returns the path;
  under Telegram the main agent delivers it with `send_media_telegram`, while
  a subagent reports the path for the parent to deliver. Gate it like any
  tool (`name == "image_generate"` in the hook payload).

**Media storage.** Inbound Telegram attachments (`tg-*`) and generated images
(`img-*`) live in `~/.shell3/media/` — stable paths that survive reboots,
re-readable with `read_media`, re-sendable, browsable in the dashboard.
Synthesized TTS audio is the exception (sent and deleted). The folder grows
until you prune it.

**`read_media` modalities** (needs `media` in the agent's `tools`): images
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

## Scheduled jobs — `cron/`

One file per job; the filename is the job name. Each fires a declared
**subagent** on `schedule` (cron expression or `@daily`/`@hourly`/…), with the
body as its prompt. The scheduler runs inside `shell3 telegram` and
`shell3 web`.

```markdown
---
schedule: "@daily"
agent: explorer
notify: true
# workdir: /some/path   # optional; defaults to the config dir
---
Summarize anything noteworthy from the last day.
```

`notify: true` wakes the chat with the result; `false` queues it quietly for
the agent's next turn (failures always wake). `workdir` sets the job's working
directory (default: the config dir). `/reload` arms changed files;
`/run <name>` fires a job on demand.

## Heartbeat — `heartbeat.md`

Cron fires a fresh, contextless subagent at an exact time; the heartbeat
periodically wakes the **main session** — full conversation context — with a
standing checklist, and stays silent unless something needs attention. Use it
for standing awareness (inbox, "did anything break?", promised follow-ups);
use cron for exact-time isolated jobs. The file existing arms it; deleting it
(+ `/reload`) stops it.

```markdown
---
every: 30m
active: { from: "08:00", to: "23:00", tz: Europe/Berlin }
---
- anything urgent in the inbox?
- any background work you promised and haven't finished?
```

Each tick that lands while the session is **idle** and inside the `active`
window injects the checklist as a queued turn. The model replies exactly
`HEARTBEAT_OK` when nothing needs attention; the bot strips the sentinel and
sends nothing. Busy or out-of-window ticks are **skipped, not queued** —
timing is approximate by design.

- `active` is optional (omit for 24/7); `from` inclusive, `to` exclusive,
  `"HH:MM"`; `from > to` spans midnight. `tz` is an IANA zone (validated at
  load), default host-local. An optional `prompt` key overrides the preamble.
- Only `shell3 telegram` ticks — the dashboard's Status view shows the
  declared heartbeat and whether the running front-end arms it.
- Test end-to-end with `shell3 dev --heartbeat`: fires one tick and prints
  whether the reply would be suppressed or delivered.

## Skills — `skills/`

A skill is a plain `.md` file the agent reads with `cat` when relevant — no
`skill` tool, no declaration. Every `*.md` in `skills/` (non-recursive)
becomes one skill for the main agent. Frontmatter needs a `description` (the
one-liner the agent uses to decide whether to read the body); `name` defaults
to the filename:

```markdown
---
description: Planning + approval gate before any non-trivial change.
---
When asked for a non-trivial change, first...
```

Adding a skill = drop a file in `skills/` + `/reload`. An unusable file (no
frontmatter/description, empty body, duplicate name) is skipped with a
warning — `shell3 health` hardens those into errors. Granted skills are
indexed by absolute path in the system prompt under `## Skills`. Subagents
carry no skills; put a subagent's standing instructions in its prompt body.

## Putting it together

Read the tree `boot` writes (`~/.shell3/`) for a full example; the
[cookbook](cookbook/README.md) has drop-in extras — subagents, skills, proxy
and sandbox setups. Validate any edit with `shell3 health` before `/reload`.
