# CLI

Run `shell3 <command> --help` for the complete flag list. The forms below show
the normal operator paths.

## Paths

With no path flags, shell3 uses:

```text
config   ~/.shell3/shell3.lisp
workdir  ~/.shell3/workdir
state    <workdir>/.shell3_project
```

`--here` selects `./shell3.lisp` and the current directory. Explicit mode
requires both `--config` and `--workdir`.

## Conversation

```sh
shell3
shell3 --here
shell3 --config /path/to/shell3.lisp --workdir /path/to/project
shell3 -p 'one headless request'
```

Bare shell3 is line-oriented and keeps ordinary terminal scrollback. `/help`
shows host commands, `/reload` validates and activates a complete new config
generation, and `/exit` closes the console. Escape cancels an active turn.

`-p` runs one request and waits for background commands started by that turn.
Positional prompts are rejected.

## Configuration

```sh
shell3 boot
shell3 boot /path/to/shell3.lisp
shell3 config check /path/to/shell3.lisp
shell3 config skill /path/to/shell3.lisp NAME
```

`boot` writes one complete kit and never overwrites. `config check` performs
strict parsing and resolution, including scheduled wrkfiles. `config skill`
validates the kit and prints one embedded skill body.

## Telegram

```sh
shell3 telegram
shell3 telegram --here
shell3 telegram --config /path/to/shell3.lisp --workdir /path/to/project
shell3 telegram --console
```

Telegram uses the kit's `telegram` declaration and reads its token from the
named environment variable. It resumes one persisted conversation per chat and
supports `/ask`, `/help`, `/stop`, `/superstop`, `/new`, and `/reload`.

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
shell3 inbox archive MESSAGE_ID[,MESSAGE_ID...]
```

`notify` persists before attempting a best-effort wake. A successful command
means the notice is durable; its JSON receipt separately reports wake delivery.

Inbox commands do not load a model or config. `list` returns bounded metadata
and previews. `read` returns a bounded UTF-8-safe chunk and records contiguous
read progress. `archive` rejects a batch unless every named notice is pending
and fully read.

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

`run` creates immutable durable state and advances until terminal or waiting.
Without a request argument it reads piped input; an interactive terminal means
an empty request. `beat` advances one runnable wave. `status` is read-only JSON.
`signal` uses the durable inbox. `cancel` records cancellation and interrupts an
active beat.

See [wrk.md](wrk.md) for the file format and state model.

## Schedules and service

```sh
shell3 service --config /path/to/shell3.lisp --workdir /path/to/project
shell3 schedule list --config /path/to/shell3.lisp --workdir /path/to/project
shell3 schedule run --config /path/to/shell3.lisp --workdir /path/to/project NAME
shell3 schedule history --workdir /path/to/project [NAME]
```

`service` is the headless persistent workflow router and schedule host. It does
not open a model session. `schedule list` emits resolved declarations as JSONL;
`run` admits one manual fire through the normal durable path; `history` emits
the SQLite ledger newest first.

Exactly one `service` or Telegram process may own a project's schedules.

## Completion and version

```sh
shell3 completion bash
shell3 completion zsh
shell3 --version
```
