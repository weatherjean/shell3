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

**What shell3 defends:**

- **Tool guards.** Every tool call passes through the `on_tool_call` guard
  chain declared in `shell3.lua` before execution. Guards are Lua functions
  that can allow, block (the model sees a denial and must change approach),
  or cancel the whole turn. Denials and the guard verdicts are typed
  internally — the model cannot spoof a result that looks approved.
- **Secret isolation.** Provider keys and tool tokens live in a plain `.env`
  file beside `shell3.lua`, read only by the config loader via
  `shell3.env.secret()`. The agent is instructed never to read credential
  files, and the scaffolded config ships a `no_env_edit` guard that blocks
  edits to `.env`. There is no encryption at rest — protect the file like
  any `~/.netrc`-class credential file and keep it out of version control.
- **Process containment.** Model-run commands execute in their own process
  group with a default 10s timeout (model-extendable to a 600s cap); on
  timeout or cancel the whole group receives SIGTERM, so runaway
  grandchildren don't outlive the turn. Output is size-capped before it
  reaches the model.
- **Headless restraint.** In headless/pipeline mode the interactive-shell
  tool is removed from the schema entirely and a system reminder tells the
  model no human is present; host policies can additionally block
  destructive calls via the guard chain.
- **Auditability.** `--out` streams a lossless JSONL audit log of every
  turn event — assistant tokens, tool calls with raw arguments, tool
  results, and terminal status — for downstream review. Failing requests
  dump the last HTTP traffic to `.shell3/last_error.json` locally.

**Residual risk you accept by using it:** a model with `bash` access can do
anything your user account can do, within the limits of the guards you
configure. Run shell3 in a sandbox, container, or throwaway user if you need
hard isolation; guards are a steering wheel, not a sandbox.

## Supported versions

Security fixes target the latest release.
