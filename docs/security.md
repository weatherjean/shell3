# Security & data

shell3 runs shell commands chosen by a language model. This page covers the
threat model, the one safety hook, secrets, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell: **no approval prompt, no
built-in allowlist**. That's the point of a bash-first agent, it composes
with your whole system. Treat a session the way you'd treat running a script
someone else wrote.

The opt-in gate is a bash hook script per agent: `hooks/tool-call.sh` for the
main agent, `hooks/<name>.tool-call.sh` for subagent `<name>`. There is no
fallback between them; each agent is governed by exactly one script or none.
The script runs before **every** tool (`bash`, `bash_bg`, `edit_file`,
`read_media`, MCP tools as `mcp_<server>_<tool>`, host tools like
`send_media_telegram`) with the call as JSON on stdin, and prints a verdict: pass,
rewrite, runner-swap, or block — there is no ask verdict and no approval flow.
The full verdict contract and payload fields are in
[configuration.md](configuration.md#the-command-gate--hookssh).

**If you need hard isolation, run shell3 in a container, VM, or throwaway
user account.** The hook is a policy gate, not a security boundary.

## The armed scaffold gate

A fresh config ships with the gate armed, not as a commented-out example.
What `hooks/tool-call.sh` refuses, and why:

- **Credentials** (`.env`, `~/.ssh`, `~/.aws`, `~/.config/gh`, …): blocked
  for read and write, by every tool. A `lib/bin` script reads the one key it
  needs at point of use.
- **The gate itself** and `shell3.yaml`: readable, not writable. Otherwise
  "ask the operator to lift this" has an obvious shortcut.
- **System paths** (`/etc`, `/usr/bin`, `/System`, `~/Library`): writes
  blocked. `/usr/local` and `/opt` are allowed, since installing a tool is
  ordinary work.
- **Destruction**: `rm -rf /`, `mkfs`, fork bombs, and anything that stops
  shell3 itself.
- **Unread remote code**: `curl … | sh`, `base64 -d | sh` and friends.
- **Publishing**: `npm publish`, `gh release create`, force-pushes. Plain
  `git push` is allowed; it's normal work and recoverable.
- **Everything else runs.** A denylist, deliberately: an allowlist makes
  every new project a refusal until someone edits the file, which is how
  gates get turned off.

Every rule decides immediately — there is no ask verdict or approval prompt
to park a turn on — and each refusal tells the model to raise the block with
the operator rather than route around it. The script is short bash with the
reasoning in its comments; read it and tune it to your deployment.

## Network surface

shell3's outbound connections are Telegram's Bot API and the endpoints named
in your config: model `base_url`s and MCP servers. That is the whole list.
There is no telemetry, no crash reporting, and no update check. (The agent's
own shell commands can of course reach anything the gate lets them — that
includes any wrapper script you write for transcription, speech, or image
generation; see [cookbook/voice-images.md](cookbook/voice-images.md).)

## The Telegram boundary

shell3 has **no listening socket**: it long-polls Telegram outbound, so there
is no port to expose, no login to defend, and no tunnel to run. The whole
access model is two secrets:

- **The bot token** *is* the bot. Anyone holding it can read the chat's
  pending updates and send as your bot. It lives in `.env` like every other
  secret; revoke it with `/revoke` in [@BotFather](https://t.me/BotFather) if
  it leaks, and restart with the new one.
- **`telegram.chat_id`** is the only chat the bot answers. Updates from any
  other chat, inline-button presses included, are dropped before a turn
  starts, so a stranger who finds your bot gets nothing back. Point it at
  your own private chat: it accepts a group id, and then every member of
  that group holds the shell described below.

**Whoever controls that chat controls a shell** on the machine running
shell3. That includes anyone with access to your Telegram account, and any
device where it is still signed in: put a passcode on the app, keep two-step
verification on, and audit active sessions in Telegram's settings. There is
no privilege tier below "shell".

The remaining line of defence is the [command gate](#unsafe-by-default),
which the scaffold ships armed but is yours to tune.

**What travels through Telegram.** Every message, reply, transcript,
dash-view document, generated image and voice note goes through Telegram's
servers; a bot chat is not end-to-end encrypted, and cannot be. That includes
anything the agent quotes from your machine: file contents, command output, a
`/status` dump of the system prompt, a `/runs` replay of a stored session.
Treat the chat as a third-party log of everything the agent says.

There is no approval flow to fall back on: whoever holds that chat **is**
the operator — the `chat_id` allowlist is the whole access model.

## What the gate does and doesn't guarantee

- **Fails closed.** A script that exits nonzero, prints malformed JSON, or
  times out (10 s) blocks. A malformed `argv` (empty, or an empty element)
  blocks; it never runs unwrapped.
- **Match the whole command.** Write patterns against the entire `command`
  string, so `echo hi; rm -rf /` and `x=$(rm -rf /)` still hit an `rm -rf`
  pattern. Chaining can't hide a flagged fragment.
- **Headless sessions are flagged.** Subagents and cron jobs have no human
  attached; hooks see `headless: true` in the payload and can print a
  tailored block for them. A block's `reason` flows back to the agent (and,
  from a subagent, to its parent in the completion mail).
- **Per-agent, no inheritance.** A subagent with no hook file runs ungated;
  the main agent's script never applies to it. Give every subagent its own
  script (even a strict three-line allowlist) if it must be constrained.
- **It's a guardrail, not a boundary.** A determined model can phrase a
  destructive command your regexes don't catch. Pair with real isolation for
  anything that must not escape.

## Output redaction — `tool-result.sh`

`hooks/tool-result.sh` (and `hooks/<name>.tool-result.sh`) runs after every
tool; print `{"output": "…"}` to replace what the model sees, e.g. to redact
secrets. A failing redactor fails **closed**: the output is replaced by an
error notice, never passed through unredacted. Background jobs are out of
scope, since the hook sees only the "started job…" pointer and not the
streamed output; redact at the source if a background command can emit
secrets.

## Reminder-envelope hardening

Completion notices (background jobs, subagent results) are injected into the
agent's context inside `<system-reminder>` blocks. The untrusted text they
carry is neutralized first: any embedded `<system-reminder` /
`</system-reminder` sequence is `&lt;`-escaped (case-insensitively), so tool
output can't close the host's envelope and forge system text. The notice
header also frames the content as task output rather than instructions.
Structural, always on, not configurable.

## Secrets

Secrets live in a plain-text `.env` beside `shell3.yaml`, referenced from
YAML as `env:KEY`:

- **Never commit `.env`.** The shipped `.gitignore` excludes it.
- **Never read or display credential files.** This applies to you and to the
  agent: the system prompt says so, the scaffold's gate blocks commands whose
  text touches `.env`, and `send_media_telegram` refuses to send `.env` or a
  dotenv sibling (`.env.local`, `.env.production`, …) out of the chat.
- **Scripts read secrets at point of use.** The pattern, with the gate deny
  and `tool-result.sh` redaction as backstops, is in
  [configuration.md](configuration.md#scripts--secrets). On a multi-user box
  the usual caveat applies: a process's environment and arguments are
  readable by same-user processes.

## Where data lives, and how to remove it

- **Runtime state**: `.shell3_project/`, kept beside `shell3.yaml` (default
  install: `~/.shell3/.shell3_project/`) — one SQLite database
  (`shell3.db`: every conversation, the full-text index, the front-end
  thread indexes) plus background-job logs as plain files under
  `runs/<id>/jobs/`. Sessions older than 30 days are swept at startup
  ([`runs_keep_days`](configuration.md#the-runs-janitor--runs_keep_days)
  changes or disables that). The directory ignores itself (a self-contained `.gitignore`
  of `*`). Wipe every transcript with `rm -rf ~/.shell3/.shell3_project`;
  back up by copying the directory while shell3 is stopped.
- **The rest of `~/.shell3/`**: your config, `.env`, the app log, proxy logs,
  the `/quiet` override (`quiet_mode.json`), and `media/` (chat uploads —
  everything sent to the bot, plus anything your own wrapper scripts save
  there). Wipe everything with `rm -rf ~/.shell3`.

## Reporting vulnerabilities

Report privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories),
not a public issue.
