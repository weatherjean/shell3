# Security & data

shell3 runs shell commands chosen by a language model. Be clear-eyed about what
that means, and set it up accordingly. This page covers the threat model,
the one safety hook, how secrets are handled, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell. By default there is **no
approval prompt** before a command runs and no built-in allowlist. This is a
deliberate design choice — the appeal of a bash-first agent is that it composes
with your whole system — but it means you should treat a shell3 session the way
you'd treat running a script someone else wrote.

The single opt-in hook is `shell3.on_tool_call(fn)` — it fires before **every** tool
the model calls (`bash`, `bash_bg`, `edit_file`, `read_media`, and custom
tools), and is off until you call it. Your handler decides per tool by
switching on `t.name`. See the next section for the full model. The scaffold
config that `boot` writes ships its example gate **commented out**, so a fresh
config gates **nothing**; that example, once enabled, covers only the bash
family, leaving `edit_file` ungated — a config choice you can change (e.g. to
refuse editing a secrets file), not a hardcoded exemption.

`t.command` is the bash command for the two bash tools (`bash`, `bash_bg`) and
**nil** for everything else.

A custom command-template tool's command is your trusted author template (not model
input), so it is never rewritten — but the tool **call** still fires the gate (by its
name), so you can `block`/`ask` it. Bake any command-level sandboxing into the tool's
own command string.

**If you need hard isolation, run shell3 inside a container, a VM, or a
throwaway user account.** `on_tool_call` is a policy hook, not a security
boundary on its own.

## Opt-in command gate — `on_tool_call`

`shell3.on_tool_call(fn)` runs before every tool — where a model-issued action can
be inspected, rewritten, routed, or blocked. It is **off by default** — everything
runs freely until you register a handler. Handlers are chainable: multiple
`on_tool_call` calls run in declaration order; the first **terminal** verdict wins.

Each handler receives a table `t` with:

- `t.name` — the **real** tool name (`"bash"`, `"bash_bg"`, `"edit_file"`, `"read_media"`, or a custom tool's name)
- `t.command` — the bash command string (only for the two bash tools; **nil** otherwise)
- `t.args` — the raw arguments JSON string (every tool)
- `t.headless` — `true` when no human asker is attached to the session (in-process subagents, cron jobs); an `{ask=...}` verdict would auto-deny there, so branch on it to block with a clearer reason or allow a safe subset. Independent of `disable_safety` (which affects ask resolution, not human presence).

A handler returns one of:

- `nil` — pass; continue to the next handler (or run if none remain)
- `{ command = "..." }` — rewrite the bash command text; continue the chain (bash tools only — fails closed on a non-bash tool)
- `{ argv = { ... } }` — **terminal**: exec this argv exactly (runner swap, e.g. into Docker or SSH; `bash`/`bash_bg` only)
- `{ block = true, reason = "..." }` — **terminal**: block; `reason` is shown to the model
- `{ ask = "prompt", reason = "...", ask_timeout = N }` — prompt a human (inline Allow/Deny buttons in Telegram); allowed → run, declined or headless → block with `reason`. `ask_timeout` is optional (seconds, default 300).

A handler that raises a Lua error **fails closed** (blocks). Only `{block=true}`
blocks via the block verdict; a returned table that contains none of the recognized
keys (`block`/`argv`/`ask`/`command`) fails closed (is blocked) as a safety
default; return `nil` to pass.

**Denylists use `shell3.regex(pattern)`.** Patterns are compiled with Go's RE2
engine **at config load** — a bad pattern is a load error, never a silent failure.
Use `(?s)` so `.*` spans newlines; match `t.command` as a whole string so chaining
can't hide a flagged fragment:

```lua
local re   = shell3.regex
local HARD = { re([[(?s)rm\s+-rf\s+/]]), re([[(?s)mkfs]]), re([[(?s)dd\s+if=]]) }
local ASK  = { re([[(?s)rm\s+-rf]]), re([[(?s)\bgit\s+push]]), re([[(?s)curl\b.*\|\s*(ba)?sh]]) }

shell3.on_tool_call(function(t)
  -- Guard required: t.command is nil for non-bash tools, so matching it without
  -- this check would error (→ fail closed). The `or` covers both bash tools.
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
end)
```

**Whole-command matching closes the chaining hole.** Because matching scans the
entire command, a flagged command can't hide behind a benign prefix: `echo hi; rm
-rf /` and `x=$(rm -rf /)` both match `rm\s+-rf`. With `(?s)`, `.` spans newlines
too — splitting a command across lines can't slip a fragment past a `.*` rule.

**Headless subagents deny on `{ask=}` matches.** The bot shows inline Allow/Deny
buttons in the chat. In-process subagents (spawned
via the `task` tool) run headless, so an `{ask=...}` verdict is auto-denied with
its `reason`. Handlers see this ahead of time as `t.headless` and can return a
tailored `{block=...}` (or allow a safe subset) instead of an ask that will
never be answered. A prompt nobody answers falls back to deny after the timeout
(`ask_timeout`, default 300 s). `{block=true}` never prompts — it blocks
everywhere. Ordinary reads (`cat`, `rg`, `ls`) match nothing and run, so a
subagent explores freely without being gated.

**This is a guardrail, not a hard boundary.** A determined model can still phrase
a destructive command in a way your regexes don't catch. Pair it with real
isolation (a container, VM, or throwaway account) for anything that must not
escape.

**`on_tool_result` for output redaction.** The symmetric post-execution hook
`shell3.on_tool_result(fn)` runs after a tool produces output. Like `on_tool_call`,
it fires for **every** tool, and `r.name` is the real tool name — `"bash"`,
`"bash_bg"`, `"read"`, `"edit_file"`, or a custom tool's name. Gate on `r.name` only
if you mean to; for secret redaction you usually want to cover all output. The
handler receives `r` with `r.name`, `r.args` (raw
arguments JSON), and `r.output`. Return `{ output = "..." }` to replace what the
model sees (e.g. redact secrets); return `nil` to pass through unchanged.

Errors fail **open** — a broken result-rewriter must not destroy tool output — so
they are logged and the original passes through. The trade-off is deliberate but
worth stating: if your redactor throws, the **unredacted** output reaches the model,
so keep redaction handlers simple and total.

```lua
shell3.on_tool_result(function(r)
  return { output = (r.output:gsub("API_KEY=%S+", "API_KEY=[redacted]")) }
end)
```

**Background jobs are out of scope for output redaction.** For `bash_bg` (and
backgrounded custom tools) the `on_tool_result` handler sees only the "started
job…" pointer, not the process's real stdout/stderr — that streams through the
in-process job runtime (the dashboard's jobs view, job events, and the
completion notice). If a background command can emit secrets, redact at the source (or
don't run it in the background).

See [configuration.md](configuration.md#opt-in-command-gate--on_tool_call)
for the full reference.

## Reminder-envelope hardening

Completion notices (background jobs, subagent results) are injected into the
agent's context inside `<system-reminder>` blocks. The untrusted text they
carry — command output tails, a subagent's final summary — is **neutralized**
before interpolation: any embedded `<system-reminder` / `</system-reminder`
sequence is defanged (`&lt;`-escaped, case-insensitively), so output can't
close the host's envelope and forge system or user text. The notice header
also tells the model the reported content is task *output* — data, not
instructions. This is structural, always on, and not configurable.

## Secrets

Secrets live in a plain-text `.env` beside `shell3.lua` (e.g. `~/.shell3/.env`),
read from Lua via `shell3.env.secret("KEY")`. Two consequences follow:

- **Never commit `.env`.** The shipped `.gitignore` excludes it; keep it that way.
- **Never read or display credential files.** This applies to you and to the
  agent — the system prompt instructs the agent not to read `.env` either.

Declared custom-tool `secrets` are exported into the command's **process
environment**. On a shared host, same-user processes can read another process's
environment (`/proc/<pid>/environ` on Linux), so a tool secret is effectively
visible to anything that user can run. That's an acceptable cost on a local,
single-user machine; on a multi-user host, treat tool secrets as readable by that
user's other processes and scope them accordingly.

## Where data lives, and how to remove it

shell3 is file-native: there is no database. State lives in two places.

**Project-local runtime state** lives in each project's `.shell3_project/`
directory — conversation history and sessions, all as plain JSONL:

- `.shell3_project/runs/<id>/messages.jsonl` — one conversation per directory
  (`meta.json` beside it holds model/status/timestamps)

The directory ignores itself (a self-contained `.gitignore` of `*`), so it is
never committed. To wipe a project's entire history:

```sh
rm -rf .shell3_project    # this project's history and job logs
```

**Global state** lives under `~/.shell3/`: your `shell3.lua` config, the `.env`
secrets, the rotating app log, and any `run_proxy` logs. It holds no conversation
history. To wipe your global config entirely: `rm -rf ~/.shell3`.

## Reporting vulnerabilities

Please report security issues privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories)
rather than a public issue.
