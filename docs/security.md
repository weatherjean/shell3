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

- `shell3.bash_safety{allow=, deny=}` — a declarative glob allow/deny gate with a
  live human-approval (ask) flow for anything unlisted (TUI `y/N` prompt /
  Telegram inline buttons; headless subagents deny on ask). See the next section.
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

## Opt-in human-in-the-loop — `bash_safety`

`shell3.bash_safety` is the opt-in layer that puts a human between the model
and the shell. When enabled, it splits each command on shell operators and
matches segments against your `allow` and `deny` glob lists: denied commands
are hard-blocked, allowed commands run, and anything else asks for human
confirmation. `deny` wins over `allow`; the gate runs before `wrap_bash`.

**Known limitation:** the splitter is a cheap heuristic — it is NOT a shell
parser. It breaks on `&&`, `||`, `|`, `;`, newline, `&` (background), `$(`
(command substitution), backtick, and the redirection operators `>` `>>` `<`
`<<` `<<<` (so a redirect target can't ride along inside an allowlisted
segment — e.g. `cat x > /etc/passwd` puts `/etc/passwd` on its own segment,
which falls to `ask`). However it is NOT exhaustive: anything hidden inside
quotes, deeply nested constructs, and indirection like `eval`/`exec` can still
defeat `deny`. A `;` inside a quoted string is also still treated as a segment
boundary (errs toward more segments, which is fail-safe).

**`deny` is best-effort defense-in-depth, not a hard boundary.** The **`allow`
list is the real safety boundary**: everything not explicitly allowed lands in
`ask` (a `y/N` prompt in the TUI; an automatic deny where no asker is wired —
see below). Write `allow` conservatively — list specific commands you know are
safe rather than trying to be exhaustive. Do not rely on `deny` to catch every
dangerous form of a command.

**Allowlist the agent's own read commands.** The agent reads its skills and
inspects files with bash (`cat`, `rg`, `ls`, …). If you enable `bash_safety`,
the `allow` list must include those reads or the agent can't read its skills or
config — and where no prompt is wired it can't recover. An empty `allow` list
bricks the agent.

**Glob word-boundary note:** `*` is a greedy substring wildcard with no word
boundary: `ls*` also matches `lsof`/`lsattr`. Use a trailing space to express a
word boundary (e.g. `ls *` or `git status *`) where that matters.

**The TUI and Telegram can ask; headless subagents deny on ask.** The TUI shows
a `y/N` confirmation prompt; the Telegram host sends inline `Allow`/`Deny`
buttons. Headless `shell3` subagents have no attached human, so any command that
lands in the "ask" verdict is automatically denied, and the reason is reported
to the parent agent via the inbox. Configure a complete `allow` list for
commands your subagents legitimately need, since they cannot prompt.

An ask-verdict that is never answered (e.g. a Telegram prompt nobody taps) does
not block forever: it falls back to deny after `ask_timeout` (default 5 minutes;
set `ask_timeout = 0` to wait indefinitely).

See [configuration.md](configuration.md#opt-in-command-approval--bash_safety)
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
