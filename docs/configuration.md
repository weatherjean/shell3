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
  shell3.yaml            # wiring: models, web, mcp, media, background, runs_keep_days
  .env                   # secrets — never commit this file
  agent.md               # THE agent: frontmatter (model, tools, context) + prompt body
  notifier.md            # the completion-triage persona (optional; see Notifier)
  memory.md              # a context: file the scaffold wires in by default
  agents/<name>.md       # subagents; the file IS the registration
  projects/<name>/       # a project: project.md brief + manager.md subagent (+ skills/)
  projects.md            # the agent's standing project index (brief)
  skills/<name>.md       # skills; drop a file in, reload
  hooks/tool-call.sh     # command gate for the main agent
  hooks/<name>.tool-call.sh   # command gate for subagent <name>
  hooks/*.tool-result.sh # output rewriters (same per-agent split)
  cron/<name>.md         # scheduled jobs (checklists included — see below)
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
    keep_recent: 33000   # verbatim tail (tokens); default compact_at * 0.33;
                         #   a value ≥ compact_at is clamped to compact_at / 2
    prune_at: 60000      # cheaper first tier: stub old tool outputs, no LLM call
                         #   default compact_at * 0.6; 0 (or ≥ compact_at) disables;
                         #   setting it without compact_at is a load error
```

The agent (and any subagent) can skip the prune tier individually with
`prune: false` in its frontmatter (the thresholds stay on the model;
omitted/`true` inherits).

Compaction is host-managed and there are no model-driven prune/compact tools.
Each browser thread is its own session, so long histories only build up in a
thread you keep returning to; a session that does grow crosses `compact_at` and
compacts on its own. A compaction posts a notification to the bell ("context
compacted") — it discards part of the conversation, so the later "why did you
forget that" has a visible answer.

You can also compact on demand: send **`/compact`** in the chat (type `/` for
the menu). It runs the same summarise-the-head, keep-the-recent-turns
compaction and replies with what it freed — "Compacted: about 42.1k tokens →
6.3k tokens". Asked for deliberately, it keeps a smaller verbatim tail than the
automatic path, which is sized as a fraction of `compact_at`; otherwise it
would answer "nothing to compact yet" every time you were below the threshold,
which is exactly when you would ask.

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
context: [memory.md]
---
You are a personal assistant running inside shell3…
```

Frontmatter keys: `model` (required), `tools` (any of `bash`, `bash_bg`,
`edit`, `media`, `read`, `list_files`), `mcp` (see [MCP](#mcp-servers)),
`prune`, `context` (see below).

### Giving the agent a memory — `context:`

A new thread starts with no conversation history — the only continuity is
staying in a thread. `context:` is how you give the agent a standing memory
instead: a YAML list of paths, relative to the config directory, globs
allowed:

```yaml
context: [memory.md, notes/*.md]
```

- Each file's contents are appended to the system prompt under a `## Context`
  heading, one `### <path>` sub-section per file — the agent knows exactly
  where to `edit_file` to update its own brain.
- Files are read **fresh at session creation**, not at config load: edit
  `memory.md` in one thread and the very next message sees the change, no
  reload needed.
- A literal (non-glob) entry that doesn't exist fails config load, same as
  any other strict-decode error. A glob matching zero files is legal —
  `shell3 health` warns about it. A file that disappears between load and a
  session being built is skipped with a `(context file missing: <path>)`
  stub in the prompt, never a turn failure.
- List order is preserved; a glob's own matches are sorted lexically within
  its entry.
- Main agent only — subagent frontmatter rejects `context` (like `skills`,
  subagents carry none). `projects.md` keeps its own separate mechanism (the
  standing portfolio brief, read at config load) — no unification with
  `context:` in this release.

`shell3 boot` scaffolds `context: [memory.md]` plus a starter `memory.md`;
existing configs are untouched since the key is optional.

The main agent is **bash-first**: it reads with `cat`/`sed -n`, lists with
`ls`/`find`, searches with `rg` — all through `bash` — and a hallucinated
`read_file`/`grep` call gets an error redirecting it back to bash/edit_file.
The `read` and `list_files` tools exist as an opt-in for agents that do better
with structured file tools (typically a [subagent](#subagents--delegation) on
a smaller model) — list them in `tools` to turn them on; leave them out and
the bash-first redirect stands. A read-only agent is a policy, not a tool set:
gate `bash` in its [hook script](#the-command-gate--hookssh).

## Subagents & delegation

A subagent is a delegatable specialist: one file in `agents/`. The filename is
its name; the file is the registration — the main agent can spawn every
subagent in the directory, and the `task` tools appear automatically as soon
as any subagent exists — a file in `agents/`, or a project's manager (no
toggle). `description` is required: it's what the main model reads when
deciding to delegate. `agent.md` and `notifier.md` are illegal here — both
names are reserved.

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
goroutine, not a subprocess). Subagents run headless (an `ask` gate verdict
auto-denies), and delegation is single-level by construction — a subagent
never gets the `task` tool.

`bash_bg` runs on the same job runtime but is gated separately by `bash_bg`
in `tools`. **Completion delivery is the notifier's** (see
[The notifier](#the-notifier--notifiermd)): each finished job — bash_bg,
subagent, or cron run — becomes one small triage turn that decides whether
the result is posted to you, handed to the main agent, or stays silent
(recorded in runs/ and the jobs list either way). Both `task` and `bash_bg`
accept two extra args:

- `direct: true` skips the notifier — the spawning agent is woken with the
  completion notice, the right choice when the user asked for the work and
  is waiting on it;
- `note: "…"` rides along as triage context ("the user is waiting on this")
  for jobs the notifier judges.

A bash_bg job's full output is persisted to
`.shell3_project/runs/<session>/jobs/<id>.log` (capped at 1 MiB, swept with
its run) so the notifier and `task_status` can read past the in-memory tail.

A subagent's still-running `bash_bg` job keeps its session open past its
main turn; each completion resumes the subagent for a follow-up turn whose
summary is triaged like any completion — or, for a `direct` job, delivered
straight to the main agent (capped at 5 follow-ups per subagent — past the
cap, or after cancel, the raw job event is triaged instead, so no completion
is lost). `task_cancel <sub-id>` cascades to the jobs that subagent started.
One global knob caps it all:

```yaml
background:
  max_concurrent: 8    # concurrent background jobs (default 8)
```

## Projects — `projects/`

A **project** groups long-running work under a dedicated manager. It's a
`projects/<name>/` directory with two files (plus an optional `skills/`):

```
projects/site/
  project.md         # the brief: frontmatter (description, workdir) + body
  manager.md         # the manager: a subagent named after the project
  skills/<name>.md   # optional; reach only this manager
```

`project.md`'s frontmatter is strict — `description` and `workdir` are both
required, and `workdir` (a `~/` is expanded) must be an existing directory.
The body is the brief the manager reads when it opens the project:

```markdown
---
description: The marketing site
workdir: ~/code/site
---
# site
State the goal, the current status, and what's next. Keep it short — deep
memory goes in sibling files in this folder.
```

`manager.md` is a subagent, parsed exactly like an `agents/<name>.md` file but
**named after the project** and run with its shell in the project's `workdir`
(not the config dir). Managers join the same flat subagent namespace as
`agents/`, so a project name that collides with a subagent — or the reserved
names `agent` and `notifier` — is a load error. Per-project `skills/` reach only that manager;
global `skills/` stay main-agent-only.

Scaffold a project with [`shell3 project new`](cli.md#shell3-project-new--scaffold-a-project);
it also appends an index line to `projects.md`. That file is the agent's
standing project index — its body is injected into the main agent's system
prompt (after the skills index, before any `## Context` section) so, in every
new thread, the agent knows which projects exist
and which manager owns each. Register a new manager for dispatch with a reload
(`POST /api/reload`) or a restart; `shell3 health` validates and lists every
project.

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

Servers connect at startup (and on reload), in parallel, each under its
own timeout; their tools join the opted-in agents' tool lists as
`mcp_<server>_<tool>` (`mcp_github_search_issues`). A server that is down
loads as a **warning** — shell3 still starts, that server's tools are just
absent until the next reload — while `shell3 health` treats it as a failure
and reports each server's state. The interface's Status view lists every
server (up/down, tool count, last error). At call time a dead server gets one
automatic reconnect; if that fails too the model sees the error as tool
output and adapts — a broken server never kills a turn.

MCP calls flow through the same [tool-call hook](#the-command-gate--hookssh)
as everything else: `name` is the prefixed tool name and `command` is null, so
gate them by name.

## The command gate — `hooks/*.sh`

shell3 gives the model a real shell, so the hook script is what limits it. A
scaffolded config **ships with the gate armed** (see below); an agent with no
hook file of its own runs ungated.
Hooks are per-agent, with no fallback or chaining — each agent is governed by
exactly one script per kind, or none:

- `hooks/tool-call.sh` — the main agent.
- `hooks/<name>.tool-call.sh` — subagent `<name>` (including when cron
  dispatches it). A subagent with no hook file runs **ungated**; the main
  hook never applies to it.

The split keeps each script trivial: the explorer's gate is a three-line
"allow rg/cat/ls, block the rest" instead of one shared script branching on
agent identity. A hook file whose `<name>` matches no subagent is a warning
(`shell3 health` fails on it — it's usually a typo). The interface's Status
view states which of the two it is, in as many words: **command gate armed**,
or **command gate off** when the main agent has no `hooks/tool-call.sh`.

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
| `{"ask": "prompt", "reason": "…", "ask_timeout": N}` | Ask a human (an Allow/Deny modal in the browser); declined/headless/timeout → block. Any tool. The modal gives up after 2 min (instantly with no browser attached); `ask_timeout` tightens the outer bound (default 300 s). |
| `{"command": "…"}` | Rewrite the bash command. Bash tools only — fails closed elsewhere. |
| `{"argv": ["…"]}` | Exec exactly this argv (runner swap). `bash`/`bash_bg` only. |

A script that exits nonzero, prints malformed JSON, or runs past 10 s **fails
closed** (blocks, with the failure as the reason). The script's cwd is the
config directory. Compose everything in the one script; there is no chain.

The scaffold ships `hooks/tool-call.sh` armed. Its shape, and the reasoning
behind it, in one line each:

- **Credentials** (`.env`, `~/.ssh`, `~/.aws`, `~/.config/gh`, …) — blocked for
  read and write, by every tool. A `lib/bin` script reads the one key it needs
  at point of use, so secrets never enter the conversation.
- **The gate itself** and `shell3.yaml` — readable, not writable. Otherwise
  "ask the operator to lift this" has an obvious shortcut.
- **The machine's plumbing** (`/etc`, `/usr/bin`, `/System`, `~/Library`) —
  writes blocked. `/usr/local` and `/opt` are not on the list: installing a
  tool is ordinary work.
- **Never** — `rm -rf /`, `mkfs`, fork bombs, and anything that stops shell3
  itself (an autonomous agent that kills its own runtime has nobody to restart
  it).
- **Unread remote code** — `curl … | sh`, `base64 -d | sh` and friends.
- **Public and permanent** — `npm publish`, `gh release create`, force-pushes.
  Ordinary `git push` is allowed: it is normal work and it is recoverable.
- **Everything else runs.** A denylist, deliberately: an allowlist means every
  new project is a refusal until someone edits this file, which is how gates
  get switched off entirely.

It never asks. shell3 usually runs unattended, and an unanswered ask parks the
turn until it times out and then denies anyway — so every rule decides at once,
and each refusal tells the model not to route around it but to raise it with
the operator. `jq` makes the JSON handling clean:

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

## Web host — `web:`

Where `shell3 serve` listens, and where the agent's shell runs.

```yaml
web:
  workdir: /home/me/.shell3/workdir # optional; default = the config dir
  addr: 127.0.0.1:8765              # listen address (default 127.0.0.1:8765)
  password: env:SHELL3_WEB_PASSWORD # REQUIRED — serve refuses to start without it
  # totp_secret: env:SHELL3_WEB_TOTP_SECRET   # optional second factor
  # tunnel: "cloudflared tunnel --url http://{addr}"   # public URL, spawned at start
  # url: https://…                                     # fixed address (wins over tunnel)
```

### Authentication — `web.password`

**Required.** `shell3 serve` refuses to start without it and `shell3 health`
fails on it, because reaching this interface means reaching a shell: the agent's
first verb is `bash`, unsandboxed unless you arm a
[hook](#command-gating--hookstool-callsh). Loading a config without one still
succeeds — `shell3 ask` serves nothing and stays usable — so the check is at
serve time, not load time.

Like every other secret it lives in `.env` and is referenced as `env:KEY`:

```
SHELL3_WEB_PASSWORD=at-least-sixteen-characters
```

Sixteen characters is the floor `shell3 boot` enforces, and `serve` warns below
it. `boot` offers a generated password; it is printed once, so save it.

Logging in exchanges the password for a session cookie (`HttpOnly`,
`SameSite=Lax`, `Secure` over https). Sessions are stored server-side as hashes
in `<config>/.shell3_project/web_sessions.json` (mode `0600`) and last 7 days,
renewed as you use them — restarting `serve` does not log you out. **Changing
the password logs out every browser**, which is what makes it a real response to
a suspected breach. Deleting that file does the same.

Failed logins get an escalating delay rather than a lockout: a lockout would let
anyone who can reach the login route hold it closed against you. Every attempt
is written to the app log with its IP and user-agent, and **every successful
login raises a notification** in the bell and over web push — that notice is
how you would find out someone else got in.

### Second factor — `web.totp_secret`

Optional, and on simply by being set. With it, the password alone is not a
session: login also asks for the six-digit code from an authenticator app, and a
code cannot be used twice inside its window.

`shell3 boot` offers enrolment and prints a QR code to scan, writing:

```
SHELL3_WEB_TOTP_SECRET=…
```

Lose the phone and there is no lockout to recover from: the secret is a line in
a file on your own machine, so delete it and restart.

**None of this replaces auth in front.** A login is a shell, so an
identity-aware proxy (Cloudflare Access, Tailscale, Authelia) is still worth
having when the interface is exposed — that is defence in depth, and it is what
gives you SSO and hardware 2FA. A non-loopback `addr` over plain http starts
anyway, with a warning: the password and cookie cross the network in clear.

`tunnel` is a shell command spawned detached at start (`{addr}` substituted);
the first bare `https://…` URL it prints is used, its output goes to
`~/.shell3/tunnel.log`, and the URL is probed until it actually serves before
being printed (a fresh quick-tunnel hostname can take a little while to
route). If no URL appears, shell3 still runs on `addr`. The example needs
[`cloudflared`](https://github.com/cloudflare/cloudflared) installed — swap in
any tunnel that prints an https URL, set a fixed `url` (a stable tunnel,
`tailscale serve`), or leave both out to stay local. A quick tunnel mints a
**new hostname on every restart**; if restarts are routine (e.g. under
systemd), prefer a stable address so bookmarks keep working.

`shell3 serve` can override both keys for one run:
[`--tunnel`](cli.md#shell3-serve--run-the-agent-and-its-web-interface) with no
value runs the cloudflared quick tunnel, `--tunnel "<command>"` replaces
`tunnel`, and `--no-tunnel` ignores it and stays local. Whichever way a tunnel
gets started, serve prints a note that it is now reachable from the internet:
the login password becomes the boundary, and a session is a shell, so
proxy-level auth in front is still worth having.

### Push notifications

Nothing to configure: on first start `shell3 serve` generates a VAPID keypair
into `<config>/.shell3_project/web_push_keys.json` (mode `0600`) — the identity
this install presents to push services — and stores per-browser subscriptions
beside it in `web_push_subs.json`. If the keypair can't be written, push is
simply off; notifications still reach an open tab.

Turning it on is per browser, from the notification bell: a toggle requests
permission, subscribes, and registers with the server, and a **Test** button
confirms the path. Every bell notification is then pushed too. Subscriptions a
push service reports as gone (`404`/`410`) are dropped automatically, so an
uninstalled or expired registration doesn't fail forever; the browser side
unsubscribes through `DELETE /api/push/subscribe`.

Push requires a **secure context**, so it works on `localhost` and over an
https tunnel but not over plain http to another host — one more reason to run
with `tunnel`/`url` rather than a bare non-loopback `addr`.

## Voice & images — `media:`

Four optional blocks under `media:`, each pointing at a model by name. All
speak the same OpenAI-compatible surface: `audio/transcriptions`,
`audio/speech`, chat completions with an image part, `images/generations`.

```yaml
media:
  stt: { model: groq-whisper }                    # dictation → text
  tts: { model: groq-tts, voice: Fritz-PlayAI }
  describe: { model: some-vision-model }          # for text-only main models
  imagegen: { model: some-image-model, size: 1024x1024 }
```

- **`stt: { model, language? }`** — backs `POST /api/stt`, the composer's
  dictation button: the recording is stored under
  `~/.shell3/media/` (as `web-*`) and transcribed into the composer, which you
  edit before sending. Transcription failures surface as an error on that
  request, not a turn. (`echo:` is still accepted but inert — the transcript
  lands in the composer, where you can already see it.)
- **`tts: { model, voice?, format? }`** — backs `POST /api/tts`, the read-aloud
  button on a reply. `voice` and `format` are passed to the model; synthesized
  audio is cached under `~/.shell3/media/` (as `tts-*`) and served back, so
  replaying a reply costs nothing. Without `tts` the browser's own speech
  synthesis is used instead. (`mode:` is still accepted but inert — the browser
  reads a reply aloud when you ask it to; a value other than
  `off`/`inbound`/`always` still fails the load.)
- **`describe: { model, prompt? }`** — captions an attached image before the
  turn, injecting `[image: <description>]`. Point it at a vision model when the
  main model is text-only — or at the main model itself (`shell3 boot` wires
  this when you answer that your model has vision). Every upload is stored
  under `~/.shell3/media/` (as `up-*`) and its path goes into the prompt, so
  the agent can re-open it later with `read_media` either way. Without
  `describe`, an attached image is passed to the model as a multimodal part
  instead of a caption.
- **`imagegen: { model, size?, api? }`** — adds an `image_generate{prompt,
  size?}` tool to **every** agent (main and subagents). `api: openai`
  (default) uses `images/generations`; `openrouter` POSTs a chat-completions
  request with `modalities=["image","text"]` — OpenRouter's image-output
  dialect — and reads the image off the reply (its dedicated `/api/v1/images`
  endpoint pre-authorizes worst-case cost, ~$2, and 402s low balances; the
  chat route charges actual usage, ~$0.03/image; `size` is ignored on this
  shape). Generated files land in `~/.shell3/media/` and the tool returns the
  path; the main agent shows one by writing a markdown image at its
  `/api/media/<file>` URL, while a subagent reports the path for the parent to
  deliver. Gate it like any tool (`name == "image_generate"` in the hook
  payload).

**Media storage.** Dictated recordings (`web-*`), uploads (`up-*`), generated
images (`img-*`), and synthesized speech (`tts-*`) live in `~/.shell3/media/`
— stable paths that survive reboots, re-readable with `read_media`, and
servable to the browser at `/api/media/<file>`. The folder grows until you
prune it — the interface's **Files** view
has it as a second root beside the config tree (`GET /api/media`), newest
first, with inline image and audio previews, so a generated image stays
reachable long after its chat message has scrolled away.

**`read_media` modalities** (needs `media` in the agent's `tools`): images
(`.jpg/.jpeg/.png/.gif/.webp`, vision models), audio
(`.wav/.mp3/.ogg/.opus/.oga`, audio models), PDFs (`.pdf` ≤ 20 MB, an
OpenAI-compatible `file` part — works on OpenAI and OpenRouter), and video
(`.mp4/.webm/.mov` ≤ 40 MB, a `video_url` part — an OpenRouter/Gemini
extension plain OpenAI endpoints reject).

Provider recipes — a one-key Groq quickstart for STT+TTS, the OpenRouter
variant — live in [cookbook/voice-images.md](cookbook/voice-images.md).

## Scheduled jobs — `cron/`

One file per job; the filename is the job name. Each fires a declared agent
on `schedule` (cron expression or `@daily`/`@hourly`/…), with the body as its
prompt. `agent` names either a subagent from `agents/` or a project's
`manager.md` — a project's cron job runs its manager in that project's
workdir, so a scheduled job can dispatch straight into a project's standing
context. The scheduler runs inside `shell3 serve`, dispatching each job
from a hidden, pinned `cron` parent session.

```markdown
---
schedule: "@daily"
agent: explorer
# direct: true          # optional; skip the notifier (see below)
# workdir: /some/path   # optional; defaults to the config dir
---
Summarize anything noteworthy from the last day.
```

A cron run's result goes to the **notifier** (see
[The notifier](#the-notifier--notifiermd)), which decides per run whether to
post it — a notification titled with the job name, in the bell — or stay
silent. A periodic checklist therefore only speaks up when something needs
attention: write its prompt to report findings plainly, and the notifier
silences the all-quiet runs (no sentinel needed). A failed run always
surfaces as an alert, whatever the notifier does.

`direct: true` skips the notifier: the result is handed straight to the main
agent as a **fresh main-agent turn** in a new thread, and what the agent says
comes back as a notification linked to it — for jobs whose results should be
acted on, not just reported. `workdir` sets the job's working directory; the
default is the dispatched agent's own — a project manager runs in its
project's `workdir`, everything else in the config dir — and setting it
overrides even a manager's. A reload arms changed files.

The interface's **Cron** view lists every job with its schedule, agent,
workdir, `direct` flag and full prompt, shows when it last ran and links to
the job that run started, and has a **Run now** button
(`POST /api/cron/{name}/run`) that fires one by hand — the result travels the
usual notifier route, exactly as a scheduled firing would.

The scaffold ships a checklist example as `cron/checklist.md.example` —
rename it to `checklist.md` (drop the `.example`) and reload to activate it.

## The notifier — `notifier.md`

Every background completion — a `bash_bg` exit, a subagent's result, a
lingering follow-up, a cron run — funnels through one reserved persona:
`notifier.md` beside `agent.md`.

```markdown
---
model: mini            # any declared model; a small one is ideal
---
You are the notifier … (the triage policy, in plain prose)
```

Per completion it runs one small headless turn over the event (kind, job id,
title, exit status, output tail, the spawner's `note:` — for cron runs, the
job's own prompt, so the judge knows what the job is *for* — and the on-disk
output path) with a fixed toolset: `read` and `list_files` to inspect
further, plus two verdict tools — `send {text}` posts a notification to you
(titled with the cron job's name for cron origins, `agent` otherwise, and
linked to the owning thread when one is live), and `wake {note}` hands the
result to the main agent (resuming the owning session, or running it in a
fresh thread when there is none, whose reply arrives as another
notification). Ending the turn without calling either means **silent**: the
result stays in the run's stored transcript, and nothing else happens.

Posted notifications persist: the server keeps the 50 most recent and replays
them to a browser when it connects, so completions that landed while the tab
was closed are still in the bell when you come back.

The hard rules live in the host, not the prompt: a failed job the notifier
leaves silent posts a `<label> failed: <error>` alert anyway; one completion can
trigger at most one wake; a triage turn is timeboxed (60s) and degrades to a
raw post on error or timeout; and with **no `notifier.md` at all, every
completion posts raw** — deterministic, zero tokens (`shell3 health` points
this out). The notifier is hookable like any agent
(`hooks/notifier.tool-call.sh`) and its turns are ordinary stored runs, so
you can audit exactly why something was silenced — and tune the policy by
editing `notifier.md`, which the main agent can do itself when you tell it
"stop pinging me about backups".

## The runs janitor — `runs_keep_days`

Every thread — and every background job the notifier runs a turn over — gets
its own `runs/<id>/` directory, so history multiplies quickly. An optional top-level `shell3.yaml` key bounds it:

```yaml
runs_keep_days: 30   # default 30; 0 = keep forever
```

At `shell3 serve` startup — before the server listens, never on
`shell3 ask` — a sweep deletes `runs/<id>/` directories whose newest file is
older than the cutoff, then rewrites `web_threads.jsonl` to drop entries
pointing at sessions that no longer exist (whether just swept or already
gone by other means). It prints one line, `janitor: removed N runs, M thread
entries` (silent when both are zero). Fail-open per directory: a dir the
sweep can't read or remove is skipped, not fatal — it's reported as a
`warning: janitor: …` line and the server still starts; one bad `runs/<id>/`
is cosmetic hygiene, never a reason to refuse startup. Start-time only — no
daemon, no timers.

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

Adding a skill = drop a file in `skills/` + a reload. An unusable file (no
frontmatter/description, empty body, duplicate name) is skipped with a
warning — `shell3 health` hardens those into errors. Granted skills are
indexed by absolute path in the system prompt under `## Skills`. Subagents
carry no skills; put a subagent's standing instructions in its prompt body.

## Putting it together

Read the tree `boot` writes (`~/.shell3/`) for a full example; the
[cookbook](cookbook/README.md) has drop-in extras — subagents, skills, proxy
and sandbox setups. Validate any edit with `shell3 health` before reloading.
