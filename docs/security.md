# Security & data

shell3 runs shell commands chosen by a language model. This page covers the
threat model, the one safety hook, secrets, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell: **no approval prompt, no
built-in allowlist**. That's the point of a bash-first agent — it composes
with your whole system — but treat a session the way you'd treat running a
script someone else wrote.

The opt-in gate is a bash hook script per agent: `hooks/tool-call.sh` for
the main agent, `hooks/<name>.tool-call.sh` for subagent `<name>` — no
fallback between them, so each agent is governed by exactly one script or
none. The script runs before **every** tool (`bash`, `bash_bg`, `edit_file`,
`read_media`, MCP tools as `mcp_<server>_<tool>`, host tools like
`image_generate`) with the call as JSON on stdin, and prints a verdict: pass,
rewrite, runner-swap, block, or ask a human (inline Allow/Deny buttons in
Telegram). The scaffold ships its example gate **commented out** — a fresh
config gates nothing — and, once enabled, that example covers only the bash
family, leaving `edit_file` ungated (a config choice, not a hardcoded
exemption). The full verdict contract and payload fields are in
[configuration.md](configuration.md#the-command-gate--hookssh).

**If you need hard isolation, run shell3 in a container, VM, or throwaway
user account.** The hook is a policy gate, not a security boundary.

## What the gate does and doesn't guarantee

- **Fails closed.** A script that exits nonzero, prints malformed JSON, or
  times out (10 s) blocks. A malformed `argv` (empty, or an empty element)
  blocks — never runs unwrapped.
- **Match the whole command.** Write patterns against the entire `command`
  string, so `echo hi; rm -rf /` and `x=$(rm -rf /)` still hit an `rm -rf`
  pattern — chaining can't hide a flagged fragment.
- **Headless sessions deny on ask.** Subagents and cron jobs have no human
  attached, so an ask verdict auto-denies with its `reason` (which flows back
  to the parent agent in the completion notice). Scripts see `headless` in
  the payload and can print a tailored block instead. Unanswered asks deny
  after the timeout (default 300 s). A block verdict never prompts.
- **Per-agent, no inheritance.** A subagent with no hook file runs ungated —
  the main agent's script never applies to it. Give every subagent its own
  script (even a strict three-line allowlist) if it must be constrained.
- **It's a guardrail, not a boundary.** A determined model can phrase a
  destructive command your regexes don't catch. Pair with real isolation for
  anything that must not escape.

## Output redaction — `tool-result.sh`

`hooks/tool-result.sh` (and `hooks/<name>.tool-result.sh`) runs after every
tool; print `{"output": "…"}` to replace what the model sees (e.g. redact
secrets). A failing redactor fails **closed**: the output is replaced by an
error notice, never passed through unredacted. Background jobs are out of
scope — the hook sees only the "started job…" pointer, not the streamed
output — so redact at the source if a background command can emit secrets.

## Reminder-envelope hardening

Completion notices (background jobs, subagent results) are injected into the
agent's context inside `<system-reminder>` blocks. The untrusted text they
carry is neutralized first: any embedded `<system-reminder` / 
`</system-reminder` sequence is `&lt;`-escaped (case-insensitively), so tool
output can't close the host's envelope and forge system text. The notice
header also frames the content as task *output* — data, not instructions.
Structural, always on, not configurable.

## Secrets

Secrets live in a plain-text `.env` beside `shell3.yaml`, referenced from
YAML as `env:KEY`:

- **Never commit `.env`.** The shipped `.gitignore` excludes it.
- **Never read or display credential files** — this applies to you and to the
  agent (the system prompt says so; the dashboard and `send_media_telegram`
  refuse `.env` and its dotenv siblings — `.env.local`, `.env.production`, … —
  outright).
- **Scripts read secrets at point of use.** The scaffold's `scripting` skill
  teaches the pattern: a wrapper script under `~/.shell3/lib/bin/` reads the
  one key it needs from `.env` (`grep '^KEY=' ~/.shell3/.env | cut -d= -f2-`)
  inside its own process, so the secret never appears in the conversation, a
  command string, or the agent's environment. Enforce the perimeter with the
  gate example's `.env` deny (block commands whose text touches `.env`) and
  a `tool-result.sh` redactor as backstop. On a multi-user box the usual
  caveat applies: a process's environment and arguments are readable by
  same-user processes, so treat secrets as readable by anything that user
  runs.

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
