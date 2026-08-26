# Security & data

shell3 runs shell commands chosen by a language model. This page covers the
threat model, the one safety hook, secrets, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell: **no approval prompt, no
built-in allowlist**. That's the point of a bash-first agent, it composes
with your whole system. Treat a session the way you'd treat running a script
someone else wrote.

The opt-in gate is a bash function the kit declares: a `gate:` block naming
the agents it governs, bound to the function under it. There is no fallback
between agents; each is governed by exactly one function or none.
The script runs before **every** tool (`bash`, `bash_bg`, `edit_file`,
MCP tools as `mcp_<server>_<tool>`, host tools like
`send_media_telegram`) with the call as JSON on stdin, and prints a verdict: pass,
rewrite, runner-swap, or block — there is no ask verdict and no approval flow.
The full verdict contract and payload fields are in
[configuration.md](configuration.md#the-command-gate--gate).

**If you need hard isolation, run shell3 in a container, VM, or throwaway
user account.** The hook is a policy gate, not a security boundary.

## The armed scaffold gate

A fresh config ships with the gate armed, not as a commented-out example.
What the scaffold's `gate:` function refuses, and why:

- **Credentials** (`.env`, `~/.ssh`, `~/.aws`, `~/.config/gh`, …): blocked
  for read and write, by every tool. A `lib/bin` script reads the one key it
  needs at point of use.
- **The gate itself: not protected, deliberately.** The gate, the wiring and
  every agent's prompt live in the same `shell3.sh`, which the agent is
  expected to edit — that is the whole self-evolve loop — so a write rule on
  that path would block ordinary work, and a narrower one is theatre the
  agent can step over in two lines of Python. The refusal text still tells
  the agent not to edit the gate; that is advice, not enforcement. If you
  want the gate to hold, make the file unwritable at the OS level — see
  below.
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

Every gate decision that blocks, rewrites, or goes to the reviewer writes a
`gate …` WARN line to the app log (`~/.shell3/shell3.log`), with the tool,
the command and the reason truncated to 300 bytes each. Allowed calls are not
logged — the gate runs before every tool call, so passes would drown the
refusals. `grep gate ~/.shell3/shell3.log` is the answer to "has anything been
refused?".

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
generation; see the `using-llms` skill in
[internal/scaffold/defaults/base/skills/using-llms.md](../internal/scaffold/defaults/base/skills/using-llms.md).)

## The Telegram boundary

shell3's chat side has **no listening socket**: it long-polls Telegram
outbound, so there is no login to defend and no tunnel to run. The one
listener is the read-only web dash on `127.0.0.1` (below). The chat access
model is two secrets:

- **The bot token** *is* the bot. Anyone holding it can read the chat's
  pending updates and send as your bot. It lives in `.env` like every other
  secret; revoke it with `/revoke` in [@BotFather](https://t.me/BotFather) if
  it leaks, and restart with the new one.
- **`telegram.allow_from`** is who the bot obeys — the whole access model.
  It lists Telegram user ids, and only those people can drive the agent,
  in any chat. A message from anyone else is dropped on the update loop:
  before a room is created, before an attachment is saved, before a token is
  spent. A stranger who finds your bot, or who adds it to a group, gets
  nothing back. Unset, it falls back to the owner of `telegram.chat_id`,
  which is the single-DM case (a DM's chat id is its user id).

  `telegram.chat_id` is NOT an access rule. It names the HOME chat — where
  cron results and ownerless completions land — and nothing else. A group
  chat id there with an empty `allow_from` is refused at startup: the owner
  fallback would resolve to nobody.

**Everyone you list holds a shell** on the machine running shell3 — the same
shell you have, bounded only by the command gate. There is no privilege tier
below "shell" and no read-only role. Listing a coworker so they can ask the
agent for a deploy also lets them ask it for your SSH keys. That includes
anyone with access to those people's Telegram accounts, and any device where
one is still signed in: put a passcode on the app, keep two-step verification
on, and audit active sessions in Telegram's settings.

**Groups.** In a group the bot answers only what is addressed to it — an
`@mention` or a reply to one of its own messages — and only from an
allowlisted sender. Everything else is discarded without entering any
conversation. Two consequences worth knowing:

- With privacy mode ON (the default), `/ask <message>` and replies to the
  bot's own messages are the way in, and Telegram itself filters everything
  else — the bot is never sent the rest of the room. This is the smaller
  surface, and the recommended setup.
- Turning **privacy mode off** in @BotFather (or promoting the bot to admin)
  additionally enables plain @mentions, at a price: the group's messages then
  reach the shell3 process, which drops the ones that are not for it. They
  are not stored, not logged, and never enter a prompt — but the enforcement
  becomes shell3's rather than Telegram's, and a bug in that gate is a bug in
  your access control. Attachments are fetched only after a message clears
  both gates, so a stranger's photo is never downloaded.
- A group **admin** can edit the group description, and by default that
  description is injected into that room's prompt as standing context (it is
  delimited and labelled as member-written, not as an instruction from you).
  An admin need not be on `allow_from`. Set `use_description: false` for that
  chat under `telegram.chats:` if the room's admins are not people you would
  hand a prompt to.
- Reading that description requires the bot to see group info, which in
  practice means promoting it to admin in the group. Weigh that as its own
  decision: an admin bot also receives every message in the room (the
  privacy-mode filter no longer applies to it), so shell3's own gate becomes
  the filter, exactly as with privacy mode off. The `context:` files under
  `telegram.chats:` give a room standing context with no rights at all, and
  are the safer choice when the brief does not need to be editable from
  Telegram.

The remaining line of defence is the [command gate](#unsafe-by-default),
which the scaffold ships armed but is yours to tune.

**What travels through Telegram.** Every message, reply, transcript,
dash-view document, generated image and voice note goes through Telegram's
servers; a bot chat is not end-to-end encrypted, and cannot be. That includes
anything the agent quotes from your machine: file contents, command output,
a stored run's transcript. Treat the chat as a third-party log of everything
the agent says.

There is no approval flow to fall back on: whoever is on `allow_from` **is**
an operator.

## The web dash

The dash (`dash_port`, default 7333) binds `127.0.0.1` only and is strictly
read-only: status, the live conversation, jobs and their output logs, cron
schedules and detail, stored run replays, and a browser for the config
directory. Every request needs a `?t=` token minted by `/dash` — 32 random
bytes, ~1h lifetime, memory-only (a restart invalidates all of them),
compared in constant time; anything else is a bare 403. Everything model- or
tool-derived is HTML-escaped before it reaches a page. Exposing the dash
beyond loopback is a tunnel's job (`/dash help exposing` sets one up —
tailscale, cloudflared, or ngrok); the listener itself never binds wider, and
the token check stays in front either way.

A token in a URL is a bearer secret, and it now grants more than transcripts:
a live link lets its holder read your whole configuration tree through the
files browser — the kit (`shell3.sh`), skills, and any wrapper
scripts under `lib/`, plus the tail of every background job's output. The one
thing it can never surface is a credential: `.env` and its dotenv siblings
(`.env.*`) are listed but reported redacted,
and their bytes are never read off disk to build the page. Binary and
oversized files are flagged rather than dumped. Treat a `/dash` link the way
you would treat read access to `~/.shell3` itself, and let it expire.

## The gate is a speed bump, not a boundary

Say this plainly, because the previous version of this page did not: **the
agent can rewrite its own gate in two lines of Python.** The rules match shell
text, and `python3 -c "open('shell3.sh','w').write('')"` contains no `rm`, no
redirect, and no `sed -i`. It sails through.

That is not a bug to patch — every pattern list has this shape. What the gate
buys is the honest mistake, which is what actually happens when nobody is
watching. It is worth having for that and should be judged by that.

If you need the gate to hold against an agent that means to get around it, the
answer is filesystem-level, not textual:

- run shell3 as a **dedicated user** that does not own the config, or
- set the **immutable flag** — `chflags uchg <file>` (macOS), `chattr +i`
  (Linux) — so clearing it needs a password, or
- run the whole thing in a **container or VM**, which is the only answer that
  bounds what a compromised agent reaches.

A short gate is also a better gate. One that refuses ordinary work does not
teach an agent where the boundary is; it teaches it that the whole subject is
forbidden, and the operator eventually switches it off entirely.

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
- **`review` is a false-positive reducer, not a second boundary.** The
  `{"review": true}` soft deny sends flagged commands to a one-word LLM
  guardian (deny on anything but a clean APPROVE, comments stripped, fail
  closed on error or timeout, a three-strike breaker on consecutive
  denials). It lets the gate keep broad, lazy patterns without refusing
  ordinary work — but a reviewer that sees only the command string can be
  fooled by one crafted to look benign. Keep the irreversible rules
  (credentials, gate edits, machine destruction) on `block`, where no model
  gets a vote.
- **It's a guardrail, not a boundary.** A determined model can phrase a
  destructive command your regexes don't catch. Pair with real isolation for
  anything that must not escape.

## Output redaction — `note:`

A kit's `note:` block binds a function that runs after every tool for the
agents it names; print `{"output": "…"}` to replace what the model sees, e.g.
to redact secrets. A failing redactor fails **closed**: the output is replaced by an
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

Secrets live in a plain-text `.env` beside the kit (`shell3.sh`), referenced
from the wiring as `env:KEY`:

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

- **Runtime state**: `.shell3_project/`, kept beside the config (default
  install: `~/.shell3/.shell3_project/`) — one SQLite database
  (`shell3.db`: every conversation, the full-text index, each front-end's
  current-conversation marker) plus background-job logs as plain files under
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
