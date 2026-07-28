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
rewrite, runner-swap, block, or ask a human (Allow/Deny buttons in the chat). **The scaffold ships an armed gate**, not a commented-out example: a
fresh config already refuses credential reads, system-path writes, unread
remote code (`curl … | sh`), publishing, force-pushes, commands that would kill
shell3 itself, and edits to the gate scripts. Everything else runs untouched,
because a harness that mostly runs unattended has to be able to do the work it
is given. It never asks: with nobody reading the chat an ask is a denial that
first parks the turn, so every rule decides immediately. Read
`hooks/tool-call.sh` — it is a short bash script, and the reasoning is in its
comments. The full verdict contract and payload fields are in
[configuration.md](configuration.md#the-command-gate--hookssh).

**If you need hard isolation, run shell3 in a container, VM, or throwaway
user account.** The hook is a policy gate, not a security boundary.

## The Telegram boundary

shell3 has **no listening socket**: it long-polls Telegram outbound, so there
is no port to expose, no login to defend, and no tunnel to run. The whole
access model is two secrets:

- **The bot token** *is* the bot. Anyone holding it can read the chat's pending
  updates and send as your bot. It lives in `.env` like every other secret;
  revoke it with `/revoke` in [@BotFather](https://t.me/BotFather) if it leaks,
  and restart with the new one.
- **`telegram.chat_id`** is the only chat the bot answers. Updates from any
  other chat are dropped before a turn starts, so a stranger who finds your bot
  gets nothing back.

**Whoever controls that chat controls a shell** on the machine running shell3 —
the agent's first verb is `bash`. That includes anyone with access to your
Telegram account, and any device where it is still signed in: put a passcode on
the app, keep two-step verification on the account, and audit active sessions
in Telegram's own settings. There is no privilege tier below "shell".

The remaining line of defence is the [command gate](#unsafe-by-default) —
`hooks/tool-call.sh` — which the scaffold ships armed, but it is yours to tune:
read it and widen or tighten it for the work this deployment does.

**What travels through Telegram.** Every message, reply, transcript, dash-view
document, generated image and voice note goes through Telegram's servers — a
bot chat is not end-to-end encrypted, and cannot be. That includes anything the
agent quotes from your machine: file contents, command output, a `/status` dump
of the effective system prompt, a `/runs` replay of a stored session. Treat the
chat as a third-party log of everything the agent says.

Approvals inherit all of this: whoever holds that chat taps the Allow/Deny
buttons on a gate `ask`. No answer means denial — a cancelled turn, a send
failure, or the timeout all deny, and a headless caller (subagent, cron) denies
immediately.

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
  the payload and can print a tailored block instead. Unanswered asks deny:
  `ask_timeout` (default 300 s) bounds the wait, and a send failure or a
  cancelled turn denies immediately. A block verdict never prompts.
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
  agent (the system prompt says so, the scaffold's gate blocks commands whose
  text touches `.env`, and `send_media_telegram` refuses to send `.env` or a
  dotenv sibling — `.env.local`, `.env.production`, … — out of the chat).
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

- **Runtime state**: `.shell3_project/`, kept beside `shell3.yaml` (default
  install: `~/.shell3/.shell3_project/`) — conversation history as JSONL
  (`runs/<id>/messages.jsonl` + `meta.json`) and the Telegram message→session
  index (`telegram_threads.jsonl`). The directory ignores itself (a
  self-contained `.gitignore` of `*`). Wipe it — every transcript:
  `rm -rf ~/.shell3/.shell3_project`.
- **The rest of `~/.shell3/`**: your config, `.env`, the app log, proxy logs,
  the `/voice` override (`voice_mode.json`), and `media/` (everything sent to
  the bot, generated images, cached speech). Wipe everything:
  `rm -rf ~/.shell3`.

## Reporting vulnerabilities

Report privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories),
not a public issue.
