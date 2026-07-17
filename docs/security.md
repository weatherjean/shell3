# Security & data

shell3 runs shell commands chosen by a language model. This page covers the
threat model, the one safety hook, secrets, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell: **no approval prompt, no
built-in allowlist**. That's the point of a bash-first agent — it composes
with your whole system — but treat a session the way you'd treat running a
script someone else wrote.

The single opt-in hook is `shell3.on_tool_call(fn)`. It fires before **every**
tool (`bash`, `bash_bg`, `edit_file`, `read_media`, custom tools) and returns
a verdict: pass, rewrite, runner-swap, block, or ask a human (inline
Allow/Deny buttons in Telegram). The scaffold ships its example gate
**commented out** — a fresh config gates nothing — and, once enabled, that
example covers only the bash family, leaving `edit_file` ungated (a config
choice, not a hardcoded exemption). The full verdict contract, `t` fields,
and denylist idiom are in
[configuration.md](configuration.md#the-command-gate--on_tool_call).

**If you need hard isolation, run shell3 in a container, VM, or throwaway
user account.** `on_tool_call` is a policy hook, not a security boundary.

## What the gate does and doesn't guarantee

- **Fails closed.** A handler that raises a Lua error blocks. A returned
  table with no recognized verdict key blocks. A malformed `argv` (empty, or
  a non-string element) blocks — never runs unwrapped.
- **Whole-command matching closes the chaining hole.** Denylists
  (`shell3.regex`, Go RE2, compiled at config load) match the entire
  `t.command`, so `echo hi; rm -rf /` and `x=$(rm -rf /)` both hit
  `rm\s+-rf`; with `(?s)`, newline-splitting can't slip past `.*` either.
- **Headless sessions deny on ask.** Subagents and cron jobs have no human
  attached, so an `{ask=…}` verdict auto-denies with its `reason` (which
  flows back to the parent agent in the completion notice). Handlers see
  `t.headless` and can return a tailored `{block=…}` instead. Unanswered asks
  deny after the timeout (default 300 s). `{block=true}` never prompts.
- **Custom tools' commands are trusted.** A command-template tool's command
  is your authored template, never rewritten by the gate — but the *call*
  still fires it by name, so you can block/ask it. Bake sandboxing into the
  template itself.
- **It's a guardrail, not a boundary.** A determined model can phrase a
  destructive command your regexes don't catch. Pair with real isolation for
  anything that must not escape.

## Output redaction — `on_tool_result`

`shell3.on_tool_result(fn)` runs after every tool; return `{ output = "…" }`
to replace what the model sees (e.g. redact secrets). Errors here fail
**open** — a broken rewriter must not destroy tool output — so a throwing
redactor lets the *unredacted* output through: keep redactors simple and
total. Background jobs are out of scope — the hook sees only the "started
job…" pointer, not the streamed output — so redact at the source if a
background command can emit secrets.

## Reminder-envelope hardening

Completion notices (background jobs, subagent results) are injected into the
agent's context inside `<system-reminder>` blocks. The untrusted text they
carry is neutralized first: any embedded `<system-reminder` / 
`</system-reminder` sequence is `&lt;`-escaped (case-insensitively), so tool
output can't close the host's envelope and forge system text. The notice
header also frames the content as task *output* — data, not instructions.
Structural, always on, not configurable.

## Secrets

Secrets live in a plain-text `.env` beside `shell3.lua`, read from Lua via
`shell3.env.secret("KEY")`:

- **Never commit `.env`.** The shipped `.gitignore` excludes it.
- **Never read or display credential files** — this applies to you and to the
  agent (the system prompt says so; the dashboard and `send_media_telegram`
  refuse `.env` outright).
- Declared custom-tool `secrets` ride the command's **process environment**.
  On a shared host, same-user processes can read it
  (`/proc/<pid>/environ`) — fine single-user; on a multi-user box, treat
  tool secrets as readable by anything that user runs.

## Where data lives, and how to remove it

shell3 is file-native — no database.

- **Per-project runtime state**: `.shell3_project/` under the workdir —
  conversation history as JSONL (`runs/<id>/messages.jsonl` + `meta.json`).
  The directory ignores itself (a self-contained `.gitignore` of `*`).
  Wipe it: `rm -rf .shell3_project`.
- **Global state**: `~/.shell3/` — your config, `.env`, the app log, proxy
  and tunnel logs, and `media/` (Telegram uploads + generated images). No
  conversation history. Wipe everything: `rm -rf ~/.shell3`.

## Reporting vulnerabilities

Report privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories),
not a public issue.
