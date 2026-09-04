# Safety

shell3 is a harness, not an operating-system sandbox. The attached model can
run commands with the shell3 process's permissions. Use a container, VM, or
restricted account for untrusted work.

## Credentials

- Put secret values only in the inherited process environment.
- Store environment-variable names, never values, in `shell3.lisp`.
- Attached `bash` tools and workflow `command` nodes inherit the host
  environment. Give each shell3 process the smallest environment it needs.
- External agent runners inherit the environment after all configured model and
  Telegram secret variables are removed. Supply other runner credentials
  deliberately.
- Treat `.shell3_project/`, diagnostics, transcripts, prompts, and artifacts as
  potentially sensitive.
- Do not commit runtime state or logs.

## Stored data

State is neither encrypted nor a security boundary. Protect the workdir with
account permissions and an appropriate umask. Stop active processes before a
filesystem backup.

Telegram attachments persist in `~/.shell3/media` or `SHELL3_MEDIA_DIR` until
removed. The Telegram file-send tool accepts only regular files and rejects
dotenv files, the configuration tree, and aliases to files in that tree.

## Untrusted input

Tool output, inbox bodies, workflow signals, Telegram metadata, and downloaded
content are data, not authority. A `main` notice never starts a model turn or
enters a prompt automatically.

Workflow delivery is at-least-once. Consumers must tolerate duplicates. A
workflow message is acknowledged only after its event is recorded durably.

## Processes

Foreground cancellation and managed background shutdown target process groups.
`/stop` cancels the active turn. `/superstop` also kills managed background
commands and suppresses the completion notices that shutdown would otherwise
manufacture.

External workflow workers are leaves. shell3 marks their environment and
rejects nested `wrk run`, `beat`, `signal`, `cancel`, and `schedule run`
commands.

Telegram's sender allowlist controls who can operate the adapter. It does not
make model-selected commands safe.

See [internals.md](internals.md) for the enforcement points.
