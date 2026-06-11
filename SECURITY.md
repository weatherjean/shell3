# Security

## Reporting a vulnerability

Please report vulnerabilities privately via
[GitHub Security Advisories](https://github.com/weatherjean/shell3/security/advisories/new)
rather than opening a public issue. You should get a response within a few
days.

## Threat model

shell3 executes shell commands chosen by a language model. That is its
purpose, not a vulnerability — but it shapes what shell3 does and doesn't
defend against.

**What shell3 assumes:** the user trusts their own `shell3.lua` (it is code
and runs with the user's privileges) and the configured model endpoint. A
malicious config or provider is outside the threat model — treat both like
software you install.

**Unsafe by default.** shell3 ships with **no** tool-call guard engine and no
human-in-the-loop approval flow — `bash` and `bash_bg` run with full,
unrestricted shell access. This is a deliberate design choice; the defenses
below are the ones that remain, plus a single opt-in bash hook you must wire up
yourself.

**What shell3 defends:**

- **`wrap_bash` — the one bash hook.** `shell3.wrap_bash(fn)` is the single
  place every `bash`/`bash_bg` command passes through before execution. `fn(cmd)`
  returns the command to run (optionally rewritten) or `nil`/`false` + a reason
  to block — pure allow / block / rewrite, no human prompt. The default scaffold
  ships a loud no-op (`function(cmd) return cmd end`); locking the shell down is
  your responsibility. There is no `ask`/approval verdict anymore.
- **Secret isolation.** Provider keys and tool tokens live in a plain `.env`
  file beside `shell3.lua`, read only by the config loader via
  `shell3.env.secret()`. The agent is instructed never to read credential
  files; you can block edits/reads of `.env` yourself in `wrap_bash`. There is
  no encryption at rest — protect the file like any `~/.netrc`-class credential
  file and keep it out of version control.
- **Process containment.** Model-run commands execute in their own process
  group with a default 10s timeout (model-extendable to a 600s cap); on
  timeout or cancel the whole group receives SIGTERM, so runaway
  grandchildren don't outlive the turn. Output is size-capped before it
  reaches the model.
- **Headless restraint.** In headless/pipeline mode the interactive-shell
  tool is removed from the schema entirely and a system reminder tells the
  model no human is present; host policies can additionally block
  destructive calls via `wrap_bash`.
- **Auditability.** `--out` streams a lossless JSONL audit log of every
  turn event — assistant tokens, tool calls with raw arguments, tool
  results, and terminal status — for downstream review. Failing requests
  dump the last HTTP traffic to `.shell3/last_error.json` locally.

**Residual risk you accept by using it:** a model with `bash` access can do
anything your user account can do, within the limits of whatever `wrap_bash`
hook you configure (none, by default). Run shell3 in a sandbox, container, or
throwaway user if you need hard isolation; `wrap_bash` is a steering wheel, not
a sandbox.

## Supported versions

Security fixes target the latest release.
