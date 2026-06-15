# Security & data

shell3 runs shell commands chosen by a language model. Be clear-eyed about what
that means, and set it up accordingly. This page covers the threat model,
the one safety hook, how secrets are handled, and how to remove shell3's data.

## Unsafe by default

shell3 gives the model a full, unrestricted shell. There is **no approval
prompt** before a command runs and no built-in allowlist. This is a deliberate
design choice — the appeal of a bash-first agent is that it composes with your
whole system — but it means you should treat a shell3 session the way you'd treat
running a script someone else wrote.

The single safety surface is the `shell3.wrap_bash(fn)` hook. It sees every
`bash` and `bash_bg` command (and therefore every subagent, since those run via
`bash_bg`) and can allow it, rewrite it, block it, or route it through a
sandbox. It is allow/block/rewrite only — there is no interactive approval flow.
See [configuration.md](configuration.md#gating-the-shell--wrap_bash) for the
mechanics and [cookbook/sandbox.md](cookbook/sandbox.md) for container, SSH, and
`firejail` recipes.

A malformed `wrap_bash` argv table fails **closed**: the command is blocked, not
run unwrapped. Custom command-template tools (`shell3.tool{ command=... }`)
bypass `wrap_bash` by design — their command is your trusted author template, not
model input — so bake any sandboxing into the tool's own command string.

**If you need hard isolation, run shell3 inside a container, a VM, or a
throwaway user account.** `wrap_bash` is a policy hook, not a security boundary on
its own.

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

shell3 keeps two kinds of state in two different places.

**Project-local state** lives in each project's `.shell3/` directory: the project
UUID, subagent transcripts, and proxy logs. To remove it:

```sh
rm -rf .shell3            # this project's local state
```

**Shared state** — conversation history, sessions, and background jobs — does
*not* live in per-project files. It's all in one shared database at
`~/.shell3/data/shell3.db` (WAL mode), with each row tagged by the project's UUID
(`cat .shell3/.ref`). Deleting a project's `.shell3/` directory leaves those rows
in place. To wipe all history across every project:

```sh
rm -rf ~/.shell3/data/shell3.db   # wipes ALL history, sessions, and jobs
```

## Reporting vulnerabilities

Please report security issues privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories)
rather than a public issue.
