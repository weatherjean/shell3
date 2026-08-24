# Configuration

Your config is a **directory** (default `~/.shell3/`), and its centre is ONE
file: `shell3.sh`, the **kit**. It holds the wiring, every agent, their tools
and the gate. [kits.md](kits.md) is the kit's own reference — its block
grammar, its `agent:`/`shared:`/`tool:`/`skill:`/`test:`/`command:`/`event:`
declarations, and the authoring loop; this page documents what you can put in the kit's `shell3:`
wiring block and the directories beside it.

Four rules:

1. **YAML wires it** — connections and knobs live in the kit's `shell3:`
   block, a YAML document inside a `#---` comment fence.
2. **Shell declares it** — an agent, a tool, a skill or a gate is a
   declaration block bound to the shell function under it.
3. **Files enable it** — a feature is on because its file exists, off because
   it doesn't. No enable flags.
4. **One function gates it** — policy is a bash function, not a config
   language.

`shell3 boot` writes a working tree; this page is for going beyond it.

```
~/.shell3/
  shell3.sh              # THE kit: wiring, agents, tools, skills, gate, tests
  .env                   # secrets — never commit this file
  memory.md              # a context: file the scaffold wires in by default
  skills/<name>.md       # main-agent skills; drop a file in, reload
  projects/<agent>/skills/<name>.md   # an employee's own skills
  lib/bin/<script>       # reusable glue the agent runs through bash
```

`--config`/`-c` takes a path to a config directory; omitted, it's `~/.shell3`.
The working directory is never consulted, so behavior doesn't depend on where
you launch from. A directory without a `shell3.sh` is not a config: the load
fails naming the file to create.

Every YAML block on this page lives inside the kit's wiring fence. Where a
snippet shows

```yaml
models:
  main:
    base_url: …
```

the kit carries it as

```bash
#---
# shell3:
#   models:
#     main:
#       base_url: …
#---
```

— same YAML, one `#` and an indent deeper. Snippets below show the bare YAML
for readability.

Secrets are referenced from the wiring as `env:KEY` — resolved from the `.env`
beside `shell3.sh`, anywhere inside a string value (`"Bearer env:LINEAR_KEY"`
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
    # max_tokens: 4096             # cap on a single reply; omitted = adapter default (16000)
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

Both thresholds live on the model; there is no per-agent override.

Compaction is host-managed and there are no model-driven prune/compact tools.
The Telegram front-end runs one long-lived conversation PER CHAT, so each
room's history grows steadily; when a room crosses `compact_at` it compacts on
its own, keeping that conversation viable indefinitely. It happens silently — the dash shows the current
context usage, and `shell3 ask`'s verbose output narrates each compaction as
it runs.

### Provider-specific knobs — `extra`

Keys in `extra` are injected verbatim into the top-level request JSON:

```yaml
    extra: { reasoning_split: true }                 # MiniMax: thinking → reasoning_content
    extra: { verbosity: high }                       # gpt-5-style verbosity
    extra: { provider: { order: [anthropic] } }      # OpenRouter routing (nesting works)
```

Only set it when needed — strict endpoints reject unknown fields.

Reasoning models count their thinking against `max_tokens`, so a reply can be
cut mid-sentence well before its visible length looks near the cap. A cut reply
ends with `⚠️ [output cut off — hit the model's max_tokens limit]`; raise
`max_tokens` when you see it.

Without `reasoning_split`, MiniMax repeats its reasoning inside the reply
wrapped in `<think>…</think>`. shell3 strips a leading block of that shape, but
setting the knob is better — it keeps the reasoning out of the reply at the
source, instead of the provider billing you for the same text twice.

A related provider failure is a tool call that arrives as **text**: the
endpoint failed to parse its own chat template, so the reply carries raw
`<tool_call>` markup instead of a real call (seen on MiniMax; the same wrapper
is used by Qwen and GLM templates). No legitimate reply contains it, so shell3
treats such a reply as corrupt rather than an answer — a reply to you is
replaced by `⚠️ the model produced malformed output (raw tool-call markup) —
reply suppressed; the runs dash has the transcript`, and a
[task report](#task-reports) turn posts nothing at all. The transcript keeps
the raw text either way, so the run replay still shows what the model actually
emitted. It is a symptom of the endpoint, not of your config: if it recurs,
try the provider's other route for the model.

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

## Agents — `agent:` blocks

An agent is a declaration block plus the prompt function under it. The first
`agent:` the kit declares is the **main agent**; every later one is an
employee the main agent can delegate to.

```bash
#---
# agent: main
# model: main
# use: [bash, bash_bg, edit, media, history]
# context: [memory.md]
#---
main_prompt() { cat <<'SHELL3_EOF'
You are a personal assistant running inside shell3…
SHELL3_EOF
}
```

Declaration keys: `model` (a name from the wiring's `models:`; omitted, the
main agent's model), `use` (built-ins — any of `bash`, `bash_bg`, `edit`,
`media`, `read`, `list_files`, `history` — plus the names of declared tools
and `shared:` groups), `mcp` (see [MCP](#mcp-servers)), `workdir`,
`description` (what the main model reads when deciding to delegate; employees
only), and `context` (see below).

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
- Files are re-read **at every turn**, not at config load or session
  creation: edit `memory.md` (or have the agent edit it) and the very next
  message sees the change — even in the one long-lived Telegram
  conversation, no reload needed.
- A literal (non-glob) entry that doesn't exist fails config load, same as
  any other strict-decode error. A glob matching zero files is legal —
  `shell3 health` warns about it. A file that disappears between load and a
  session being built is skipped with a `(context file missing: <path>)`
  stub in the prompt, never a turn failure.
- List order is preserved; a glob's own matches are sorted lexically within
  its entry.
- **There is a size cap.** A context file is re-read into the prompt on every
  turn, so a file that grows without bound quietly multiplies the cost of
  every run — and since the agent maintains the file itself, it is a loop the
  agent drives without being able to see it. Over **64 KB** the middle of the
  file is elided from the prompt, keeping the head and the tail, with a marker
  saying so and naming the file to `cat` for the full text. Over **32 KB**
  `shell3 health` reports the size; it only *fails* on an over-cap file, where
  content is actually being dropped.

  Keep a brain file curated rather than append-only. If the agent is logging
  per-run detail, point it at a sibling file that is **not** in `context:` and
  let it `rg` that on demand — the durable conclusions belong in the context
  file, the raw log does not.
- Every agent may declare it. An employee's paths resolve against its own
  `workdir` when it declares one, the config dir otherwise — so each
  employee's `context: [memory.md]` is its own memory, not the main
  agent's.

`shell3 boot` scaffolds `context: [memory.md]` plus a starter `memory.md`;
existing configs are untouched since the key is optional.

The main agent is **bash-first**: it reads with `cat`/`sed -n`, lists with
`ls`/`find`, searches with `rg` — all through `bash` — and a hallucinated
`read_file`/`grep` call gets an error redirecting it back to bash/edit_file.
The `read` and `list_files` tools exist as an opt-in for agents that do better
with structured file tools (typically an [employee](#employees--delegation) on
a smaller model) — list them in `tools` to turn them on; leave them out and
the bash-first redirect stands. A read-only agent is a policy, not a tool set:
gate `bash` in its [gate function](#the-command-gate--gate).

### Recalling past conversations — the `history` tool

`history` in `tools` (the scaffold puts it on the main agent) lets the agent
read its own past out of the [runs store](#the-runs-store--shell3db) instead
of only the current thread:

- `{"query": "certificate renewal"}` — ranked full-text search over what you
  and the agent said, across **every** stored session, chat threads and
  cron runs alike. FTS5 syntax: bare words AND together, `"quoted phrases"`
  match exactly, `OR`/`NOT`/`prefix*` work; a malformed query is retried as
  one quoted phrase rather than erroring. Tool output is not indexed — search
  for what was said *about* a thing, not for raw command output.
- `{"session": "<id>", "around": 41}` — read the transcript around a hit.

The tool is read-only, and it is the whole interface: the agent never writes
to the database. Leave `history` out of `tools` and the name hits the same
unknown-tool redirect as `read_file`.

## Employees & delegation

An employee is a delegatable specialist: any `agent:` block after the first.
The declaration IS the registration — the main agent can spawn every employee
the kit declares, and the `task` tools appear automatically as soon as one
exists (no toggle). `description` is required on an employee: it's what the
main model reads when deciding to delegate.

```bash
#---
# agent: assistant
# description: Use for substantial, self-contained work you can hand off whole.
# use: [bash]
#---
assistant_prompt() { cat <<'SHELL3_EOF'
You are a general-purpose assistant…
SHELL3_EOF
}
```

`model` is optional (defaults to the main agent's). With at least one
employee, the main agent gets four tools: `task` (spawn: `{subagent_type, prompt,
description}`; returns immediately), `task_list`, `task_status <id>`,
`task_cancel <id>`. The employee names and descriptions are baked into the
`task` tool's schema (an enum on `subagent_type`), so no per-turn reminder is
spent.

A spawned employee is an **in-process background job** (a child-session
goroutine, not a subprocess). Employees run headless (their gate sees
`headless: true`), and delegation is single-level by construction — an
employee never gets the `task` tool.

`bash_bg` runs on the same job runtime but is gated separately by `bash_bg`
in `use`. **Completions arrive as task reports** (see
[Task reports](#task-reports)): each finished job — bash_bg, employee,
or cron run — hands the spawning agent a report, and its reply
reaches you as an ✉️ update only when worth saying (failures always post; the
result is recorded in the runs store and the jobs list either way). Both
`task` and `bash_bg` accept two extra args:

- `direct: true` — the raw result posts straight to the chat (🔔), costing
  no agent turn; the spawning session gets the notice queued for its next
  turn instead of a wake. The right choice when you asked for the work and
  just want the output;
- `note: "…"` rides along in the report as context ("the user is
  waiting on this").

A bash_bg job's full output is persisted to
`.shell3_project/runs/<session>/jobs/<id>.log` (capped at 1 MiB, swept with
its run) so the agent and `task_status` can read past the in-memory tail.

An employee's still-running `bash_bg` job keeps its session open past its
main turn; each completion resumes the employee for a follow-up turn whose
summary arrives as a task report like any other — or, for a `direct` job,
posts raw (capped at 5 follow-ups per employee — past the
cap, or after cancel, the raw job event is mailed instead, so no completion
is lost). `task_cancel <sub-id>` cascades to the jobs that employee started.
One global knob caps it all:

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
takes a bearer header from `.env`). Declare servers once in the wiring;
each agent opts in via `mcp:` in its `agent:` block:

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

```bash
#---
# agent: main
# model: main
# use: [bash]
# mcp: [github, linear]     # or mcp: all; omitted = NO MCP tools
#---
```

Servers connect at startup (and on reload), in parallel, each under its
own timeout; their tools join the opted-in agents' tool lists as
`mcp_<server>_<tool>` (`mcp_github_search_issues`). A server that is down
loads as a **warning** — shell3 still starts, that server's tools are just
absent until the next reload — while `shell3 health` treats it as a failure
and reports each server's state. The dash index lists every server (up/down,
tool count, last error). At call time a dead server gets one
automatic reconnect; if that fails too the model sees the error as tool
output and adapts — a broken server never kills a turn.

MCP calls flow through the same [tool-call hook](#the-command-gate--gate)
as everything else: `name` is the prefixed tool name and `command` is null, so
gate them by name.

## The command gate — `gate:`

shell3 gives the model a real shell, so the gate is what limits it. A
scaffolded kit **ships with the gate armed** (see below); an agent no `gate:`
block names runs ungated.

A `gate:` block names the agents it governs and binds the function under it:

```bash
#---
# gate: [main, assistant]
#---
main_gate() {
  in=$(cat)
  case "$in" in
    *'rm -rf /'*) printf '{"block":true,"reason":"refusing rm -rf /"}'; exit 0 ;;
  esac
  printf '{}'
}
```

Unlike `tool:`/`skill:`, `gate:`, `note:` and `event:` are **named, not
positional** —
one function usually governs several agents, and a copy per agent is how two
rule sets drift apart. An agent no block names runs **ungated**; there is no
fallback or chaining, and each agent is governed by exactly one function per
kind. A `gate:` naming an agent the kit does not declare is a load error.
The dash index states which of the two it is, in as many words: **command
gate armed**, or **command gate off** when the main agent has none.

Every tool call — `bash`, `bash_bg`, `edit_file`, `read_media`, host tools
like `send_media_telegram`, and `mcp_*` — sources the kit and calls the
governing function with JSON on stdin (sourcing is safe: a kit is
definitions-only, so it runs nothing):

```json
{"name": "bash", "command": "rm -rf /", "args": "{…}", "headless": false}
```

| Field | Description |
|-------|-------------|
| `name` | The real tool name: `"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, `"send_media_telegram"`, `"mcp_…"`. |
| `command` | The bash command string — the two bash tools only; **null** for every other tool. |
| `args` | Raw arguments JSON (every tool). Gate non-bash tools by inspecting this. |
| `headless` | `true` when no human is attached (employees, cron jobs, scripted `shell3 ask -p`). |

The function prints a verdict to stdout:

| Output | Effect |
|--------|--------|
| empty or `{}` | Run. |
| `{"block": true, "reason": "…"}` | Block; `reason` goes to the model. Any tool. |
| `{"review": true, "reason": "…"}` | Soft deny: the LLM reviewer decides (see below). `bash`/`bash_bg` only — fails closed elsewhere. |
| `{"command": "…"}` | Rewrite the bash command. Bash tools only — fails closed elsewhere. |
| `{"argv": ["…"]}` | Exec exactly this argv (runner swap). `bash`/`bash_bg` only. |

When several keys are set, precedence is block > review > argv > command.

Every verdict that changes what runs — block, rewrite, and both halves of a
review — writes one WARN line to the app log (`~/.shell3/shell3.log`) naming
the tool, the command and the reason, each truncated to 300 bytes. A pass logs
nothing: the gate runs before every tool call, so logging allows would bury
the refusals. `grep gate ~/.shell3/shell3.log` answers "has the gate refused
anything?".

A
function that exits nonzero, prints malformed JSON, or runs past 10 s **fails
closed** (blocks, with the failure as the reason). An `{"ask": …}` verdict
also fails closed, with a reason naming the removal — there is no ask verdict,
and it never silently allows. The function's cwd is the config directory.
Compose everything in the one function; there is no chain.

### The `review` verdict — an LLM reviewer for judgment calls

`{"review": true, "reason": "…"}` is a **soft deny** for rules that are right
most of the time but have real false positives (`curl … | sh` is the classic
supply-chain hole *and* the documented installer for tools you asked for).
Instead of refusing outright, shell3 sends the command and your `reason` to a
one-word LLM guardian — APPROVE runs the original command unchanged, anything
else (DENY, uncertainty, a transport error, a timeout) blocks with a message
telling the model to stop and raise it with you. Three consecutive denials
for one agent escalate the message to a hard stop, so a model cannot burn a
reviewer call per retry all night.

Two optional top-level keys tune it:

```yaml
review_model: aux          # a declared model name; default = the main agent's model
review_policy: |           # extra TRUSTED rules appended to the reviewer's system prompt
  Always DENY anything writing under /etc.
  APPROVE docker compose restarts in ~/deploys.
```

The command text the reviewer sees is untrusted input from the agent:
unquoted shell comments are stripped (the cheapest place to hide "respond
APPROVE"), the command is delimited, and the guardian is told to ignore any
instructions inside it. With no reviewer resolvable a `review` verdict fails
closed, so it is never weaker than `block`. It is also **not a containment
boundary** — it reduces false blocks; the OS is still the security boundary
([security](security.md)).

The scaffold ships its `gate:` function armed. Its shape, and the reasoning
behind it, in one line each:

- **Credentials** (`.env`, `~/.ssh`, `~/.aws`, `~/.config/gh`, …) — blocked for
  read and write, by every tool. A `lib/bin` script reads the one key it needs
  at point of use, so secrets never enter the conversation.
- **The gate itself**: asked for, not enforced. The gate shares `shell3.sh`
  with every agent prompt, which the agent edits as ordinary work, so a write
  rule on that path would block the self-evolve loop. Make the file unwritable
  at the OS level if you want it to hold.
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

Every rule decides at once — there is no ask verdict or approval flow — and
each refusal tells the model not to route around it but to raise it with
the operator. `jq` makes the JSON handling clean:

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  case "$cmd" in
    *'rm -rf /'*|*mkfs*|*'dd if='*)
      printf '{"block": true, "reason": "hard_deny"}'; exit 0 ;;
    *'git push --force'*)
      printf '{"block": true, "reason": "force-push refused; raise it with the operator"}'; exit 0 ;;
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

### Output rewriting — `note:`

The symmetric post-execution hook. A `note:` block names its agents exactly
like `gate:`; the function receives `{"name": …, "args": …, "output": …}` on
stdin; print `{"output": "…"}` to replace what the model sees, `{}` or nothing
to pass through. Primary use is secret redaction:

```bash
in=$(cat)
printf '%s' "$in" | jq -c '{output: (.output | gsub("API_KEY=\\S+"; "API_KEY=[redacted]"))}'
```

A failing function here also fails **closed**: the tool output is replaced by
an error notice, never passed through unredacted. Background jobs (`bash_bg`)
are out of scope: the note sees only the "started job…" pointer, not the
process's streamed output — redact at the source if a background command can
emit secrets.

## Commands and events — `command:`, `event:`

Two more hooks the kit declares, both documented in full in
[kits.md](kits.md#commands).

`command:` is a `/verb` the front-end answers by running a shell function —
no model turn, no tokens. stdout is the reply, the text after the verb arrives
as `$ARG`, empty output posts nothing, and the command joins the client's `/`
menu. It may not be named after a built-in (`/dash`, `/stop`, `/superstop`,
`/new`, `/run`, `/btw`, `/reload`, `/quiet`); that is a load error, because a
built-in is matched first and the declaration would never fire.

`event:` subscribes a function to the session event stream. It is the one hook
that only **observes** — stdout ignored, nothing to refuse, nothing to rewrite.
`on:` is mandatory and names the kinds it receives, because `assistant_token`
fires once per streamed token. Delivery is off the turn's critical path, so a
slow subscriber costs you events (the oldest are dropped and counted in the app
log), never a stalled turn.

Because a failed observer refuses nothing, it is silent at runtime.
`shell3 health` is where a broken one surfaces: it checks that each declared
command and subscriber function is defined, without running it — dry-running a
command would post the message it exists to post.

## Telegram — `telegram:`

The bot's credentials, who may drive it, where the agent's shell runs, and
per-room tuning.

```yaml
telegram:
  token: env:TELEGRAM_TOKEN         # TELEGRAM_TOKEN in .env, from @BotFather
  chat_id: "123456789"              # the HOME chat: cron results and orphans land here
  allow_from: ["123456789", "987654321"]  # who may drive the agent, anywhere
  workdir: /home/me/.shell3/workdir # optional; default = the config dir
  max_concurrent_turns: 4           # optional; global cap on simultaneous turns
  chats:                            # optional; per-room tuning, not an allowlist
    - id: "-1001234567890"
      use_description: false        # don't inject this group's description
      context: [projects/pay.md]    # files appended to this room's brief
```

**One conversation per chat.** Every Telegram chat the bot is used in keeps
its own session, its own context and its own `/new`. A chat becomes known the
first time someone on `allow_from` addresses the bot there — there is no list
of chats to maintain.

**`allow_from` is the access model.** It lists Telegram user ids, and only
those people can drive the agent, in any chat. Authorization is checked on the
sender id, which Telegram's servers populate and the sender cannot choose, and
it is checked before commands too — otherwise anyone in a group could `/stop`
a running turn or `/new` the conversation away. A message shell3 cannot
attribute to a user (a channel post) is never authorized, and a non-numeric
entry fails at startup rather than silently narrowing access. Unset, it falls
back to the owner of `chat_id` — the single-DM case, where the chat id IS the
user id.

**`chat_id` is the home chat, not an access rule.** Cron results and
completions whose room is gone land there. Leave it out and the home chat
becomes the DM of the first `allow_from` id (which only delivers once that
person has messaged the bot — `shell3 health` says so). A GROUP chat id with
an empty `allow_from` is refused at startup: the owner fallback would resolve
to nobody, and the bot would run looking healthy while ignoring everyone.

**In a group the bot answers only what is addressed to it**: `/ask <message>`,
an `@mention` of the bot, or a reply to one of its own messages. Everything
else is dropped before it enters any conversation.

`/ask` exists because Telegram's privacy mode never delivers a plain
`@yourbot do X` to a bot — only `/cmd@thisbot` and replies to the bot's own
messages arrive. So `/ask` opens a thread and ordinary replies continue it,
with no BotFather toggle and no admin rights. To use plain @mentions instead,
either promote the bot to admin in that group, or turn **Group Privacy off**
in @BotFather and re-add the bot; the group's traffic then reaches shell3,
which discards what is not for it (see
[security.md](security.md#the-telegram-boundary) for what that moves).
`/help` explains all of this in the chat, so nobody has to remember it.

**Rooms run in parallel**, one turn each, bounded by `max_concurrent_turns`
(default 4). They share ONE working directory, so two rooms can run bash in
the same tree at once; the agent's `status` tool lists every live room and
whether it is mid-turn, and the scaffold prompt tells it to look before
touching shared files. That is advice, not a lock.

**Each room gets a brief** in its prompt: the chat title, the group
description, and any files you point at. The description is the zero-config
half — edit it in Telegram and that room's standing context changes on the
next turn, no config edit and no restart.

> **The bot must be able to see group info to read the description.** Telegram
> serves the field only to a bot with that access; a default-restricted bot
> ("has no access to messages") receives the title and an empty description,
> with no error. Promote the bot to admin in that group and it arrives. This
> is not in the Bot API docs — the `description` field row mentions no rights
> requirement — but it is reproducible: the same basic group returned
> `description_bytes=0` before a promotion and `66` after, same code, same
> chat. `shell3.log` records `chat metadata … description_bytes=N` on every
> lookup, which is how you tell "the bot cannot see it" from "the chat has
> none".

**`chats:`** tunes rooms that need something other than the defaults.
Declaring a chat neither authorizes nor enrols it. Each entry takes `id`,
`use_description` (default true; set false where the room's admins are not
people you would hand a prompt to), and `context:` (files appended to that
room's brief, read like the agent's own `context:` — 64 KB cap, middle
elided). The `context:` route needs no rights and no Telegram feature, so it
is the answer when a room's brief must not depend on chat settings.

`shell3 telegram` refuses to start without `token`, or with a non-numeric
`chat_id`. Loading a config without the block still succeeds — `shell3 ask`
and `shell3 health` don't need it.

The chat needs no listener, no login, no tunnel: shell3 long-polls Telegram
outbound. (The read-only web dash is its own localhost listener — see
[`dash_port`](#the-web-dash--dash_port).) Everyone on `allow_from` — and
whoever holds the token — controls a shell on this machine. The threat model
is in [security.md](security.md#the-telegram-boundary).

### What `/reload` does and doesn't pick up

`/reload` (and the agent's own `reload` tool) re-reads the config directory
and applies it live: prompts, models, agents, skills, cron jobs,
and MCP servers. It does **not** re-apply the front-end's own wiring — a
changed `telegram.chat_id`, `telegram.allow_from` or `telegram.workdir` takes
effect at the next `shell3 telegram` start. `telegram.chats:` IS re-applied by
a reload (room briefs re-read their description and context files on the next
turn). The same goes for `dash_port`: the dash binds once
at startup, so a port change needs a restart — the *data* on its pages is
always live (resolved per request), only the listener itself is fixed.

### What a stored run contains

Every conversation is stored in `.shell3_project/shell3.db`: messages, tool
calls and results, the model's reasoning, and — since schema v10 — the
**system prompt each turn ran with**. The prompt is what makes a run
reproducible after the fact: it carries your `memory.md`, the agent's
`context:` files, the skills index and (in Telegram) the room's brief, all of
which can change under a live conversation.

Only CHANGES are recorded, content-addressed, so an untouched conversation
costs one stored body however many turns it runs. Prompts are excluded from
history search on purpose — the `history` tool searches what was said, and a
20 KB prompt in that index would bury every query. The dash's run replay
shows each version, collapsed, at the message it took effect from.

Deleting a session (the `runs_keep_days` janitor) drops its prompt references
and collects any body nothing points at any more.

## Attachments and media

There is no `media:` config block — voice transcription, speech, and image
generation are not built-in services; they are tools you declare in your
kit, with a full working `transcribe`/`say`/`image` set (each reading its
own key from `.env` at point of use) in
[cookbook/voice-images.md](cookbook/voice-images.md). What ships:

- **Attachments.** Every file sent to the bot is saved to the media dir
  (`<configDir>/media` — `~/.shell3/media/` for the default config dir,
  overridable with `$SHELL3_MEDIA_DIR`; see
  [The media janitor](#the-media-janitor--media_keep_days) for that
  variable's precedence) as `tg-*`, and its path goes into the prompt. There
  is no automatic transcription or captioning step — the agent decides
  whether and how to act on the attachment.
- **`read_media`** (needs `media` in the agent's `tools`) lets the agent
  open a file directly: images (`.jpg/.jpeg/.png/.gif/.webp`, vision
  models), audio (`.wav/.mp3/.ogg/.opus/.oga`, audio models), and PDFs
  (`.pdf` ≤ 20 MB, an OpenAI-compatible `file` part — works on OpenAI and
  OpenRouter). Video is not supported as model input; `send_media_telegram`
  can still send a video file to the chat.
- **`send_media_telegram`** (a host tool, on every non-headless session —
  headless employee children don't get it, since there's no live chat to
  send to) lets the agent push a local file back to the chat as
  photo/voice/audio/video/document.

**Media storage.** Everything you send the bot (`tg-*`) and anything a
wrapper script generates and saves there live in the media dir — stable
paths, re-readable with `read_media` and re-sendable with
`send_media_telegram` long after the message has scrolled away. The folder
grows until you prune it or set
[`media_keep_days`](#the-media-janitor--media_keep_days).

## Scheduled jobs — `cron:`

One `cron:` block per job in the kit; the block names the job. It takes
exactly one of `agent:` or `tool:` — a job is either a prompt or a tool call,
never both, never neither. A `tool:` job runs a kit tool's shell function
directly, with no agent and no model turn (see [kits.md](kits.md#cron)). An
`agent:` job fires a kit-declared agent on `schedule` (cron expression or
`@daily`/`@hourly`/…), with the heredoc in the function under the block as
its prompt. An employee that declares a `workdir` runs its job there, so a
scheduled job can dispatch straight into a project's standing context. A job
naming an agent or a tool the kit does not declare is a **load error** — the
config does not start, rather than failing on the first tick. The scheduler
runs inside
`shell3 telegram`, dispatching each job from a hidden, pinned `cron` parent
session. Interval schedules (`@every 30m`) count from when the scheduler
arms, and a `/reload` or restart re-arms it — so the tick after one lands a
full interval later, which can look like a skipped run. Cron *expressions*
(`*/30 * * * *`) fire on wall-clock times and don't shift.

```sh
#---
# cron: daily-summary
# schedule: "@daily"
# agent: assistant
# direct: true          # optional; post the raw result (see below)
# workdir: /some/path   # optional; defaults to the config dir
#---
cron_daily_summary() { cat <<'EOF'
Summarize anything noteworthy from the last day.
EOF
}
```

A cron run's result arrives as **mail to the main agent** (see
[Task reports](#task-reports)): a turn of the main conversation reads it, with the
job's prompt riding along as context so the agent knows what the job is
*for*, and its reply reaches you as an ✉️ update only when the run carries
something worth saying (NO_REPLY stays silent). A periodic checklist therefore only
speaks up when something needs attention: write its prompt to report
findings plainly.

Quiet runs stay quiet either way, but they are not free either way. Reading
the report is a full main-agent turn at live conversation context, and on a
frequent job that turn is routinely the larger half of what the job costs.
The turn is skipped only when the JOB'S OWN reply is the `NO_REPLY`
sentinel — the completion router drops that result before any turn starts —
so tell the job to end an unremarkable run with exactly `NO_REPLY`. The main
agent answering `NO_REPLY` after reading the report saves nothing; the turn
is already spent. A failed run always surfaces as a ⚠️ alert and never
spends an agent turn.

`direct: true` skips the agent: the raw result posts straight to the chat as
a ⏰ message, costing no agent turn — for jobs whose output should be
reported verbatim, not judged. `workdir` sets the job's working directory; the
default is the dispatched agent's own — a project manager runs in its
project's `workdir`, everything else in the config dir — and setting it
overrides even a manager's. `direct:` applies only to an `agent:` job; a tool
job already posts its own result, so setting both is a load error. A reload
arms changed jobs.

The dash's Cron table lists every job with its schedule, agent, `direct` flag,
last run and outcome, and its rolling 7-day dispatched-run token cost where
known — the dash is the one dashboard; there is no cron command.
`/run <name>` fires one by hand — the result travels the usual mail route,
exactly as a scheduled firing would.

The scaffold ships a checklist example commented out at the foot of the kit —
its fences are written `##---` so the parser skips them (a declaration block
*is* a comment fence, so a genuinely commented example would otherwise be an
armed job). Drop one `#` from each fence line and reload to activate it.

## Task reports

Every background completion — a `bash_bg` exit, a subagent's result, a
lingering follow-up, a cron run — is a **task report to the agent**, routed
deterministically by the host. No triage persona, no judging turn; three
rules:

- **Failures always surface.** A failed job posts `⚠️ <label> failed: …` to
  the chat, unconditionally. If the owning session is still live it also
  receives the report so the agent can react; an ownerless failure (a broken
  cron job, say) stops at the post — no agent turn is spent per broken tick.
- **`direct: true`** (bash_bg arg, task arg, cron block) posts the
  **raw result** straight to the chat — ⏰-prefixed for cron, 🔔 otherwise —
  costing no agent turn. The owning session gets the report queued, without
  a turn, so its next turn has it in context.
- **Everything else is a report to the agent.** The report queues into the
  main conversation and runs a turn over it (whichever session spawned the
  job — cron results and orphans land there too), carrying the spawner's
  `note:` — for cron runs, the job's own prompt, so the agent knows what the
  job is *for*. The report never reaches you raw; the agent's reply posts to
  the chat as an **✉️ update** — one channel, no separate tool — unless it
  replies `NO_REPLY`, which posts nothing. Silence is the expected answer
  for routine results, and for anything the conversation shows you were
  already told.

The ✉️ prefix marks an agent-initiated update, so bare chat text always
means a direct reply to something you sent. Updates are part of the one
conversation, and anything you type next continues it. ✉️ updates always
arrive without a notification ping — an update is not a page; `/quiet on`
extends that to ⏰/🔔 posts. Replies to your own messages and ⚠️ failures
always ring.

A report is delivered at the **end** of the agent's context, and leaves a
one-line trace in the conversation: `[task report delivered to you — the user
did NOT send this and has not seen it]` followed by the report's summary line.
The report body itself is not stored. Both details matter for the agent's
behaviour rather than yours. Delivering at the end keeps the report — and the
instruction to stay silent when nothing is needed — from being filed above the
agent's own previous reply, where it reads as already-answered history. The
trace keeps the *cause* of an ✉️ update in the conversation: without it the
update survives with nothing explaining it, and an agent asked later why it
sent something has no way to answer except to guess.

Report-handling turns are ordinary stored runs, so the runs dash shows exactly
what the agent did with each report.

## The runs store — `shell3.db`

Every session — chat threads, employees, cron runs, `shell3 ask` — is
stored in one SQLite database beside `shell3.sh`:
`.shell3_project/shell3.db`. It holds the sessions and their messages, each
front-end's current-conversation marker, and an FTS5 full-text index over user and
assistant text (the index the [`history` tool](#recalling-past-conversations--the-history-tool) searches;
tool output is deliberately not indexed). It is pure Go — no cgo, no
external SQLite. A background job's raw output stays a plain file under
`.shell3_project/runs/<session>/jobs/<id>.log`.

The database carries a schema version. If it doesn't match the binary you are
running, the file is **deleted and recreated empty**, with one line on stderr
saying so. shell3 data is disposable by design: there are no migrations, and
a version skew never leaves you on a half-understood schema. Keep anything
you actually care about outside the store. A corrupted version stamp that
happens to land within the valid range (e.g. a genuine `2` misread as `1`) is
indistinguishable from an actual older schema, so that database is recreated
empty too — the data is not recoverable.

## The web dash — `dash_port`

```yaml
dash_port: 7333   # default 7333; 0 = no dash listener at all
```

`shell3 telegram` and `shell3 serve` bind the read-only dashboard on
`127.0.0.1:<dash_port>` at startup (never `shell3 ask`). `/dash` replies
with its URL plus a fresh ~1h token; the base URL lives in `dash_url.txt`
beside the config (seeded with localhost, overwritten by the dash-exposing
skill when a tunnel is set up). The dashboard shows the live conversation
(linked to its folding transcript), background jobs with their captured
output logs, cron schedules and per-job detail, stored run replays, and a
read-only browser of the config directory — the kit, skills, and cron
prompts. Credential files (`.env`, `.env.*`, `ai-do-not-read*`) appear in the
listing but their contents are always redacted, never read from disk. A port outside 0–65535 is a load error; a
bind failure at startup is a warning, and `/dash` reports the dash as down.
In a kit, `dash_port` goes in the `shell3:` wiring block like every other
top-level key. Details and the token model: [cli.md](cli.md#the-web-dash)
and [security.md](security.md#the-web-dash).

## The runs janitor — `runs_keep_days`

Every thread — and every background job — is a stored session, so history
multiplies quickly. An optional top-level
top-level wiring key bounds it:

```yaml
runs_keep_days: 30   # default 30; 0 = keep forever
```

Both `runs_keep_days` and `media_keep_days` are validated at config load:
negative values are a load error (use `0` for keep-forever, not a negative
number), and either key above 36500 (100 years) is also a load error — that
bound exists because the janitor's arithmetic is
`time.Duration(days) * 24 * time.Hour`, which overflows int64 nanoseconds
around 106751 days and can wrap into a small *positive* duration, silently
turning "keep basically forever" into "delete almost everything" on the next
sweep. 36500 is nowhere near that wraparound and is already an absurd
retention window.

At `shell3 telegram` startup — before the bot starts polling, never on
`shell3 ask` — a sweep deletes sessions whose last activity is older than the
cutoff, taking their messages, FTS entries, thread-index rows and job-log
directories with them. It also removes empty crash leftovers, thread entries
pointing at sessions that are gone by any other means, and orphaned
`runs/<id>/` directories left by older builds. It prints one line, `janitor:
removed N runs, M thread entries` (silent when both are zero). Fail-open: a
sweep error is reported as a `warning: janitor: …` line and the bot still
starts — stale rows are cosmetic hygiene, never a reason to refuse startup.
Start-time only — no daemon, no timers.

## The media janitor — `media_keep_days`

The media dir (`~/.shell3/media/` by default) accumulates chat uploads —
everything you send the bot, plus anything a wrapper script writes there —
and nothing removes them on its own. An optional top-level wiring key
bounds it:

```yaml
media_keep_days: 0   # default 0 = keep forever
```

Unlike `runs_keep_days`, the default is keep-forever: delivered files and
uploads are user data, so deletion is opt-in. When set, `shell3 telegram`
deletes files in the media dir whose mtime is older than N days at startup,
before the bot starts polling. It prints `janitor: removed N media files`
(silent when zero) and is fail-open like the runs janitor. Note that once a
file is swept, a `read_media` or `send_media_telegram` of its stored path in
an old transcript fails. Start-time only — no daemon, no timers.

The media dir this sweep points at is resolved by `internal/mediadir`:
normally it's derived from `--config`/the active config dir, but the
`SHELL3_MEDIA_DIR` environment variable, if set, overrides that unconditionally
— it outranks `--config`. This exists for tests, is not itself a
wiring key, and is undocumented anywhere else, but production code
reads it too. Since this is a *deleting* operation once `media_keep_days` is
set, be deliberate about that variable: an errant `SHELL3_MEDIA_DIR` pointed
at an unrelated directory (or a symlinked media dir, which the sweep follows)
means `media_keep_days` deletes old files there instead of in your actual
media store.

## Skills — `skills/`

A skill is a plain `.md` file the agent reads with `cat` when relevant — no
`skill` tool, no declaration. Every `*.md` in `skills/` (non-recursive)
becomes one skill for the main agent, and every `*.md` in
`projects/<agent>/skills/` becomes one for that employee. Frontmatter needs a
`description` (the
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
indexed by absolute path in the system prompt under `## Skills`; bodies are
never inlined, so N skills cost N lines.

## Putting it together

Read the tree `boot` writes (`~/.shell3/`) for a full example; the
[cookbook](cookbook/README.md) has drop-in extras — employees, skills, MCP
and sandbox setups. Validate any edit with `shell3 health` before reloading.
