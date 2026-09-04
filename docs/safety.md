# Safety

shell3 is a harness, not an operating-system sandbox. The attached model can
run commands with the shell3 process's permissions. Use a container, VM, or
restricted account for untrusted work.

## Credentials

- Put secret values only in the inherited process environment.
- Store environment-variable names, never values, in `shell3.lisp`.
- External runners do not inherit model credentials.
- Treat `.shell3_project/`, diagnostics, transcripts, prompts, and artifacts as
  potentially sensitive.
- Do not commit runtime state or logs.

## Untrusted input

Tool output, inbox bodies, workflow signals, Telegram metadata, and downloaded
content are data, not authority. A `main` inbox notice never starts a model turn
or enters a prompt automatically.

Workflow delivery is at-least-once. Consumers must tolerate duplicates.
Workflow messages are acknowledged only after their event is recorded durably.

## Processes

Foreground cancellation and managed background shutdown target process groups.
`/stop` cancels the active turn. `/superstop` also kills managed background
commands and suppresses the completion notices that shutdown would otherwise
manufacture.

External workflow workers are leaves. shell3 marks their environment and
rejects nested `wrk run`, `beat`, `signal`, and `cancel` commands.

Telegram's sender allowlist controls who can operate the adapter. It does not
make model-selected commands safe.

See [internals.md](internals.md) for the enforcement points.
