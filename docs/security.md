# Security & data

shell3 runs shell commands chosen by a language model. This page covers the
threat model, the one safety hook, secrets, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell: **no approval prompt, no
built-in allowlist**. That's the point of a bash-first agent, it composes with
your whole system. Treat a session the way you'd treat running a script
someone else wrote.

The opt-in gate is a bash hook script per agent: `hooks/tool-call.sh` for the
main agent, `hooks/<name>.tool-call.sh` for subagent `<name>`. There is no
fallback between them; each agent is governed by exactly one script or none.
The script runs before **every** tool (`bash`, `bash_bg`, `edit_file`,
`read_media`, MCP tools as `mcp_<server>_<tool>`, host tools like
`image_generate`) with the call as JSON on stdin, and prints a verdict: pass,
rewrite, runner-swap, block, or ask a human (an Allow/Deny modal in the
browser). The full verdict contract and payload fields are in
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

It never asks. shell3 mostly runs unattended, where an unanswered ask parks
the turn and then denies anyway, so every rule decides immediately, and each
refusal tells the model to raise the block with the operator rather than
route around it. The script is short bash with the reasoning in its comments;
read it and tune it to your deployment.

## Network surface

shell3's inbound surface is one listener, `web.addr`, and it is
[authenticated](#the-web-interface). Outbound, it connects to the endpoints
named in your config: model `base_url`s, `media:` models, MCP servers, and —
once push is on — the browser vendor's push service. That is the whole list.
No telemetry, no crash reporting, no update check. (The agent's own shell
commands can of course reach anything the gate lets them.)

## The web interface

`shell3 serve` requires a password. It refuses to start without one, and
`shell3 health` fails on a config that lacks it, because what sits behind this
login is not a document store: `/api/chat` is the agent, and the agent's first
verb is `bash` on the machine serving the page. Also behind it — every stored
transcript (`/api/runs/{id}`, `/api/jobs/{id}`), the config-file and media
readers, the effective system prompt (`/api/status`), firing a scheduled job by
hand (`/api/cron/{name}/run`), the approval answers, and reloading the config.

**Every route is gated.** The only things reachable without a session are
`POST /api/login` and the static files that draw the login screen — the app
bundle, which is this repository's published front-end and carries no secrets.
That is enforced structurally rather than by vigilance: routes are declared in
one table that records whether each is public, and a test walks that table
asserting every private route refuses an unauthenticated request. An endpoint
added without deciding its status defaults to private — fail-closed — and the
test holds it there.

### What protects the password

- **Length.** 16 characters minimum, enforced by `shell3 boot`, warned about by
  `serve`. Longer is better; `boot` offers to generate one.
- **A second factor**, if you set `web.totp_secret`: the password alone then is
  not a session. Codes are single-use inside their window.
- **Escalating delay** on failed attempts — not a lockout, which would let
  anyone who can reach the login route hold it closed against you.
- **An audit trail.** Every attempt, successful or not, goes to the app log with
  its IP and user-agent.
- **A notification on every successful login**, in the bell and over web push.
  Assume this is how you would learn that someone else is in.

### What it does not protect against

An attacker who authenticates *is* you, as far as the agent is concerned. There
is no privilege tier below "shell". The remaining line of defence is the
[command gate](#unsafe-by-default) — `hooks/tool-call.sh` — which the scaffold
ships armed, but it is yours to tune — read it and widen or tighten it for the
work this deployment does. If this interface is exposed, that script is the
difference between a session and an unconditional shell.

Sessions are server-side: a random token whose hash is stored in
`<config>/.shell3_project/web_sessions.json` (mode `0600`), 7-day sliding
expiry. Signing out revokes that session rather than only clearing the cookie.
**Changing `SHELL3_WEB_PASSWORD` invalidates every session** — the honest
response to "I think someone got in" — and deleting the file does the same.

**Keep auth in front anyway.** A password in the app is not the industry answer
to exposing a tool like this; an identity-aware proxy is — Cloudflare Access,
Tailscale, Authelia, a private network — and it is what gives you SSO and
hardware 2FA. This feature means a leaked URL is no longer a public shell. It
does not mean the interface wants to face the internet naked.

**Exposure is yours, and shell3 does none of it.** It binds `web.addr` and
stops there; how the interface becomes reachable is a decision you make
([deploying.md](deploying.md)). Whatever you choose, the moment it answers
from outside the box the password is the boundary. A non-loopback bind over
plain http is the bad case and warns at start: the password and the session
cookie cross the network in clear, so terminate TLS in front of it.

Approvals inherit all of this: whoever holds a logged-in page answers the
Allow/Deny modals. No browser attached means nobody answers, and an ask denies
at its timeout.

Push endpoints are gated like everything else, so registering a subscription,
reading the public VAPID key, or firing a test notification all require a
session.

## Push notifications — what is stored

Turning on push in the notification bell puts two files in the state directory
(`<config>/.shell3_project/`, alongside the runs):

- `web_push_keys.json` — the install's VAPID keypair, generated on first start
  and written mode `0600`. The **private key signs every push from this
  install**; anyone holding it can send notifications your subscribed browsers
  will trust. It is not a config file to copy between machines or commit.
- `web_push_subs.json` — one entry per subscribed browser: the push service's
  endpoint URL and that browser's `p256dh`/`auth` keys. Those are capabilities
  to deliver notifications to that device, so the file is a small tracking
  surface as well as a delivery list.

Contents travel to a third party by design: delivery goes through the browser
vendor's push service (Google, Mozilla, Apple), which sees the encrypted
payload and the timing of every notification. Notification bodies are the bell
text, truncated to 300 characters — job labels, notifier posts, cron results —
so anything the notifier says out loud leaves the box. Payloads are encrypted
to the subscription's keys, but treat the fact and rhythm of notifications as
visible to the push service.

Revoking: turn the toggle off (unsubscribes the browser and drops the server's
copy), or delete `web_push_subs.json` to forget every browser. Deleting
`web_push_keys.json` rotates the identity — a fresh keypair is generated on
the next start and every existing subscription stops working. Subscriptions a
push service reports as gone (`404`/`410`) are pruned automatically.

The service worker at `/sw.js` caches nothing — it exists only to show
notifications and focus an open tab — so there is no stale-asset store to
clear after an upgrade.

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
  the browser modal gives up after 2 minutes, an ask with no browser attached
  denies immediately, and `ask_timeout` (default 300 s) is the outer bound. A block verdict never prompts.
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
- **Never read or display credential files.** This applies to you and to the
  agent: the system prompt says so, the scaffold's gate blocks commands whose
  text touches `.env`, and the interface's Files view lists `.env` and its
  dotenv siblings (`.env.local`, `.env.production`, …) without ever opening
  them.
- **Scripts read secrets at point of use.** The pattern, with the gate deny
  and `tool-result.sh` redaction as backstops, is in
  [configuration.md](configuration.md#scripts--secrets). On a multi-user box
  the usual caveat applies: a process's environment and arguments are
  readable by same-user processes.

## Where data lives, and how to remove it

shell3 is file-native — no database.

- **Runtime state**: `.shell3_project/`, kept beside `shell3.yaml` (default
  install: `~/.shell3/.shell3_project/`) — conversation history as JSONL
  (`runs/<id>/messages.jsonl` + `meta.json`), the browser thread→session
  index (`web_threads.jsonl`), the hashed login sessions
  (`web_sessions.json`, `0600`), and, once push is used, `web_push_keys.json`
  (the VAPID private key, `0600`) and `web_push_subs.json` (subscribed
  browsers). The directory ignores itself (a self-contained `.gitignore` of
  `*`). Wipe it — every transcript and login session:
  `rm -rf ~/.shell3/.shell3_project`.
- **The rest of `~/.shell3/`**: your config, `.env`, the app log, proxy logs,
  and `media/` (dictated recordings, uploads, generated images,
  cached speech). Wipe everything: `rm -rf ~/.shell3`.

## Reporting vulnerabilities

Report privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories),
not a public issue.
