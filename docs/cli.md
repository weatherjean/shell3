# CLI

Run `shell3 <command> --help` for all flags. This page covers the normal paths.

## Paths

Conversation, Telegram, service, and schedule `list`/`run` share these modes:

```text
config   ~/.shell3/shell3.lisp
workdir  ~/.shell3/workdir
state    <workdir>/.shell3_project
```

`--here` selects `./shell3.lisp` and the current directory. Otherwise
`--config` and `--workdir` must be supplied together.

Wrk commands are project-local. `check`, `compile`, and `run` default to
`./shell3.lisp`; `run` stores state under the task root. `beat`, `status`,
`signal`, and `cancel` default to `./.shell3_project/wrk`. Use `--config` or
`--state` to override those paths.

Inbox, notify, and schedule-history commands default to the global workdir and
also accept `--here` or `--workdir`. Inbox and notify additionally accept a
direct `.shell3_project` path through `--state`.

## Conversation

```sh
shell3
shell3 --here
shell3 --config /path/to/shell3.lisp --workdir /path/to/project
shell3 -p 'one headless request'
```

Bare shell3 is line-oriented and keeps terminal scrollback. `/help` shows host
commands, `/reload` validates and activates a complete config generation, and
`/exit` closes the console. Escape cancels an active turn.

`-p` runs one request and waits for background commands started by that turn.
Positional prompts are rejected.

## Configuration

```sh
shell3 boot
shell3 boot /path/to/shell3.lisp
shell3 config check /path/to/shell3.lisp
shell3 config skill /path/to/shell3.lisp NAME
```

`boot` writes one complete kit and never overwrites. `config check` strictly
parses and resolves the kit and scheduled wrkfiles without resolving secrets or
running commands. `config skill` validates the kit and prints one skill body.

See [configuration.md](configuration.md) for the Lisp forms.

## Telegram

```sh
shell3 telegram
shell3 telegram --here
shell3 telegram --config /path/to/shell3.lisp --workdir /path/to/project
shell3 telegram --console
```

Telegram reads the token named by its configuration and resumes one persisted
conversation per chat. It supports `/ask`, `/help`, `/stop`, `/superstop`,
`/new`, and `/reload`.

`--console` runs the Telegram routing contract over standard input and output.
It needs no Telegram credentials, but model requests still use the configured
endpoint.

## Inbox and notifications

```sh
shell3 notify --to main 'message'
shell3 notify --to wrk:TASK/RUN --event ready < body.txt

shell3 inbox list
shell3 inbox read MESSAGE_ID
shell3 inbox read MESSAGE_ID --offset 8192
shell3 inbox archive MESSAGE_ID [MESSAGE_ID...]
```

`notify` persists before a best-effort wake. Success means the notice is
durable; the JSON receipt reports wake delivery separately.

Inbox commands load no model or config. `list` returns bounded metadata and
previews. `read` returns a bounded UTF-8-safe chunk and records contiguous read
progress. `archive` requires every named notice to be pending and fully read.

## Workflows

```sh
shell3 wrk check FILE.wrk.lisp
shell3 wrk compile --config shell3.lisp FILE.wrk.lisp
shell3 wrk run --config shell3.lisp FILE.wrk.lisp 'request'
shell3 wrk beat TASK/RUN
shell3 wrk status TASK/RUN
shell3 wrk signal TASK/RUN EVENT 'message'
shell3 wrk cancel TASK/RUN
```

`run` creates immutable state and advances until terminal or waiting. Without a
request argument it reads piped input; an interactive terminal means an empty
request. `beat` advances one runnable wave. `status` emits read-only JSON.
`signal` uses the durable inbox. `cancel` records cancellation and interrupts
an active beat.

See [wrk.md](wrk.md) for the file format and state model.

## Schedules and service

```sh
shell3 service --config /path/to/shell3.lisp --workdir /path/to/project
shell3 schedule list --config /path/to/shell3.lisp --workdir /path/to/project
shell3 schedule run --config /path/to/shell3.lisp --workdir /path/to/project NAME
shell3 schedule history --workdir /path/to/project [NAME]
```

`service` is the headless workflow router and schedule host; it opens no model
session. `schedule list` emits resolved declarations as JSONL, `run` admits one
manual fire through the normal durable path, and `history` emits the SQLite
ledger newest first.

Exactly one `service` or Telegram process may own a project's schedules.

## Completion and version

```sh
shell3 completion bash
shell3 completion zsh
shell3 --version
```
