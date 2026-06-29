# Configuration

Everything shell3 does is decided by one file: `shell3.lua`. It's plain Lua, so
it's versionable, diffable, and programmable — you configure shell3 like
software, not like a platform with a settings panel.

`shell3 boot` writes a working config for you. This page is for when you want to
go beyond the defaults: add a model, write a tool, gate the shell, or understand
how shell3 finds your config in the first place.

## Where the config lives

`boot` creates three things under `~/.shell3/`:

- `shell3.lua` — the config: your models, agents, tools, and skills.
- `lib/` — tools and skills as small Lua modules the config `require`s.
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

## Agents

An agent is a name, a model, a system prompt, and a set of tools. Declare as
many as you like and switch between them mid-session with `Tab` (when idle) or
`/agent` — your conversation history comes along.

```lua
shell3.agent({
  name   = "code",
  model  = "main",
  prompt = [[You are a careful pair-programmer…]],
  tools  = {
    bash              = true,
    read              = true,   -- paged text-file reading (see below)
    bash_bg           = true,   -- background / long-running work
    shell_interactive = true,   -- only for truly interactive programs
    edit              = true,   -- the edit_file tool
    media             = true,   -- inbound/outbound images + audio
    custom            = { my_tool },          -- Lua-defined tools (below)
    subagents         = { explorer },         -- delegatable specialists
  },
  skills = { writing_plans },                 -- skill handles (below)
})
```

The convention that ships with `boot` is two agents: a full-access `code` agent
and a read-only `plan` agent that investigates and designs but cannot edit —
design with `plan`, switch to `code` to build. But it's only a convention: add a
`review` agent, a `docs` agent, whatever fits your work. See
[cookbook/lib/extra-agents.lua](cookbook/lib/extra-agents.lua).

### The `read` tool

`read = true` gives the agent a paged, text-file reader. It accepts a `path`
(absolute or workdir-relative), an optional 1-indexed `offset` (default 1), and
an optional `limit` in lines (default 2000). Output is raw file content — no
line-number prefixes — so `edit_file` can exact-match strings straight from the
output.

Truncation is capped at **2000 lines or 50 KB**, whichever comes first. When
the file is longer, a machine-readable footer tells the model exactly where to
resume:

```
[Showing lines 1-2000 of 8421. Use offset=2001 to continue.]
```

The tool is **text-only**: binary files (detected by a ~4 KB NUL-byte / high
non-printable scan) are refused with a redirect to `read_media` or `bash xxd`.
Directories, missing files, and offsets past EOF are clean errors. Search still
belongs in bash — reach for `rg` or `grep` when you want to match patterns
across files rather than read one file's content.

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

Optional fields: `background = true` (fire-and-forget) and `timeout = N`
(seconds). See [cookbook/lib/tools.lua](cookbook/lib/tools.lua) for a full
template, including the `web_fetch` and `brave_search` tools the base config
ships with.

> **A note on secrets:** declared `secrets` are passed via the command's process
> environment. On a shared host, same-user processes can read another process's
> environment (`/proc/<pid>/environ` on Linux). That's fine for a local,
> single-user setup; on a multi-user box, treat tool secrets as readable by
> anything that user can run. More in [security.md](security.md).

## Opt-in command gate — `bash_safety`

`shell3.bash_safety` is a **regex denylist** in front of every `bash` and
`bash_bg` call. It is **off by default** — shell3 stays unsafe if you never call
it. There is no allowlist: bash runs freely, and you list the patterns you want
**blocked** or **confirmed**. Two lists:

```lua
shell3.bash_safety{
  enabled = true,
  hard_deny = { [[rm\s+-rf\s+/]], [[mkfs]], [[dd\s+if=]], "shutdown" }, -- never run
  deny      = { [[rm\s+-rf]], [[\bgit\s+push]], [[curl\b.*\|\s*(ba)?sh]] }, -- prompt
  ask_timeout = 300, -- seconds to wait for a human before denying (default 300)
}
```

### Keys

| Key | Type | Description |
|-----|------|-------------|
| `enabled` | bool | Gate is active when `true`. Absent or `false` ⇒ no gating. |
| `hard_deny` | list of regex strings | A command matching any pattern is **hard-blocked** — never run, never prompted. |
| `deny` | list of regex strings | A command matching any pattern **prompts** a human to allow/deny before running (auto-denied when headless). |
| `ask_timeout` | number (seconds) | How long a deny-prompt waits for a human before falling back to deny. Omitted ⇒ 300s (5 min); `0` ⇒ wait forever. |

Patterns are **Go regexes**, compiled at config load — an invalid pattern is a
**load error**, not a silent failure. A wrong-typed list (e.g. `deny = "rm"`
instead of `{ "rm" }`) is also a load error.

> **Migration:** the old `allow` and `read_baseline` keys are **accepted but
> ignored** — there is no allowlist or read-only baseline anymore. shell3 prints a
> load-time warning when it sees them, since an allow-only config no longer gates
> anything. Remove them and move any "dangerous" globs you had in `deny` into the
> new `deny`/`hard_deny` regex lists (note: glob `*` → regex `.*`).

### How it works

Each command is matched **as a whole string** (no splitting) against the regex
lists with `regexp.MatchString` (unanchored and DOTALL — a match anywhere counts,
and `.` spans newlines, so a command split across lines can't dodge a `.*` rule):

1. If any **`hard_deny`** pattern matches ⇒ **block** (no prompt, ever).
2. Else if any **`deny`** pattern matches ⇒ **prompt** the human (allow / deny).
3. Otherwise ⇒ **run**.

`hard_deny` is checked first, so it wins. Because matching is over the whole
command, chaining or substitution **cannot hide** a flagged command behind a
benign prefix — `echo hi; rm -rf /` still matches `rm\s+-rf`, and
`x=$(rm -rf /)` matches too. Write patterns against the dangerous form you want
to catch (`rm\s+-rf`, `\bgit\s+push`, `curl\b.*\|\s*sh`); `\b` word boundaries
and `\s+` keep them from over- or under-matching.

> **Anchors:** only `(?s)` (DOTALL) is applied, not `(?m)` (multiline). So `^`
> and `$` anchor to the start/end of the **whole** command, not to each line —
> `^git\s+push` will *not* match `make build && git push` or a `git push` on the
> third line of a multi-line command. Prefer `\b`-anchored fragments
> (`\bgit\s+push\b`) over `^`/`$` unless you specifically mean the command's ends.

This is a **guardrail, not a sandbox**: a determined model can still phrase a
destructive command in a way your regexes don't catch. For anything that needs
real logic (rewriting, container routing, programmatic policy), use
[`wrap_bash`](#gating-the-shell--wrap_bash).

### Deny-prompt confirmation and headless degradation

When a `deny` pattern matches, a human must confirm. The interactive **TUI shows
an inline `y/N` prompt**; the **Telegram host sends inline `Allow` / `Deny`
buttons**. **Headless subagents** (background `shell3` processes spawned via
`bash_bg`) have no attached human, so **a deny match is automatically denied**;
the block reason flows back to the parent agent via the completion inbox, so the
parent — where a human *is* attached — can decide how to proceed. A prompt nobody
answers does not hang the agent: after `ask_timeout` seconds (default 300; `0` =
wait forever) it falls back to deny. (`hard_deny` never prompts — it blocks
everywhere, headless or not.)

> Because there's no allowlist, ordinary reads (`cat`, `rg`, `ls`, …) match no
> pattern and just run — a headless subagent explores freely. Only commands you
> explicitly flag in `deny`/`hard_deny` are gated.

### Ordering relative to `wrap_bash`

`bash_safety` runs **before** `shell3.wrap_bash`. Only a command the gate lets
through (verdict: run, or an approved deny-prompt) is handed to `wrap_bash` for
any further inspection, rewriting, or sandboxing. Use `bash_safety` for the
declarative regex denylist, and `wrap_bash` for anything that needs Lua logic.

## Gating the shell — `wrap_bash`

shell3 is **unsafe by default**: `bash` and `bash_bg` run with no restrictions.
The single place to inspect, rewrite, or block commands is `shell3.wrap_bash`.
It receives the command string and returns one of:

- **a string** → run it under `bash -c` (you can rewrite the text),
- **a table** (list of strings) → an argv list exec'd directly — this swaps the
  *runner*, not just the text, and the command arrives as one argv element so
  nothing re-parses or re-quotes it,
- **nil / false `[, reason]`** → block it.

```lua
shell3.wrap_bash(function(cmd)
  if cmd:match("rm%s+%-rf%s+/") then return nil, "refusing rm -rf /" end
  return cmd                       -- allow (optionally rewritten)
end)
```

The table form is what makes this a real wrapper — you choose the program that
runs the agent's command, so you can route everything through a container, an
SSH host, or `firejail`:

```lua
shell3.wrap_bash(function(cmd)
  return {"docker", "exec", "mycontainer", "bash", "-c", cmd}
end)
```

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped. Custom command-template tools bypass
`wrap_bash` by design — that command is your trusted template, not model input —
so bake any sandboxing into the tool's own command. The full recipe set is in
[cookbook/sandbox.md](cookbook/sandbox.md).

## Redirecting hallucinated tools — `stub_tools`

Models trained on other harnesses reflexively reach for `read_file`, `grep`, or
`write_file`. Register those names as stubs and shell3 returns a one-line
redirect instead of erroring — a self-correcting nudge back to bash and
`edit_file`:

```lua
shell3.stub_tools({
  read_file  = "Use the read tool.",
  grep       = "Use bash: rg <pattern>.",
  write_file = "Use edit_file (empty old_string creates/overwrites).",
})
```

Stubs are config-global (every agent sees them). Later keys override earlier
ones, and a stub whose name collides with a real tool is ignored.

## Skills

A skill is a plain `.md` file the agent reads with `cat` when it's relevant —
there is no `skill` tool. You declare the skill with a name, a one-line
description (which the agent sees and uses to decide whether to read the body),
and a path to the markdown:

```lua
local plans = shell3.skill({
  name        = "writing-plans",
  description = "Planning + approval gate before any non-trivial change.",
  path        = "lib/skills/writing-plans.md",
})
-- then grant it to an agent: skills = { plans },
```

Skills granted to an agent are listed by absolute path in its system prompt
under `## Skills`, so the model knows they exist and when to reach for them. The
[cookbook](cookbook/) ships ready-to-use skills for planning, executing plans,
codebase discovery, and web search.

## Putting it together

For a full example, read the base config `boot` writes —
`~/.shell3/shell3.lua`. For drop-in additions (extra agents, more skills, the
browser skill, proxy and sandbox setups), see the
[cookbook](cookbook/README.md).
