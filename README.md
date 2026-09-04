<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="shell3: local orchestration and durable workflows" width="100%">
</p>

shell3 is a Go harness for one attached agent and checked multi-agent
workflows. Its primary interface is a line-oriented terminal conversation;
Telegram is an optional adapter to the same runtime.

The attached agent has two core tools: `bash` and `bash_bg`. Delegated work runs
through checked `*.wrk.lisp` workflows and typed external runners. Workers are
leaf processes and cannot launch workflows.

## Start

Building requires the Go version declared in `go.mod`. Runtime commands and
workflows require a Unix environment with Bash.

```sh
make build
./shell3 boot
```

`boot` writes `~/.shell3/shell3.lisp` and refuses to overwrite it. Edit the
generated endpoint and model ID. The starter kit names its API-key variable
`SHELL3_API_KEY`:

```sh
./shell3 config check ~/.shell3/shell3.lisp
export SHELL3_API_KEY=...
./shell3
```

The default workdir is `~/.shell3/workdir`. To use a project-local kit and
state, run `shell3 --here`. Explicit paths must be supplied together:

```sh
shell3 --config /path/to/shell3.lisp --workdir /path/to/project
shell3 -p 'Run the tests and summarize any failures.'
```

Secrets are read only from the inherited process environment. Do not put
credential values in `shell3.lisp`.

## Configuration

One strict, inert `shell3.lisp` contains models, the orchestrator prompt,
memory, skills, runner protocols, agent profiles, schedules, and optional
Telegram settings. Invalid or unresolved forms are errors.

Skills expose only their name and description in the prompt; the agent loads a
relevant body with `shell3 config skill`. See [Configuration](docs/configuration.md).

## Workflows

A `*.wrk.lisp` file declares a durable task graph. Nodes can invoke typed agent
runners, execute deterministic commands, repeat fresh-agent loops until an
external check passes, or wait for an event.

```sh
shell3 wrk check --config /path/to/shell3.lisp change.wrk.lisp
shell3 wrk compile --config /path/to/shell3.lisp change.wrk.lisp
shell3 wrk run --config /path/to/shell3.lisp change.wrk.lisp 'Implement the change.'
shell3 wrk status TASK/RUN
```

Runs keep immutable inputs, state, logs, and artifacts under
`.shell3_project/wrk/`. See [Workflow reference](docs/wrk.md).

## Persistent hosts

Use one persistent host per project for schedules and immediate workflow
wakeups:

```sh
shell3 telegram
# or
shell3 service --config /path/to/shell3.lisp --workdir /path/to/project
```

Never run both for the same project. Telegram adds one model tool, `telegram`,
which sends a local file to the current chat. `service` opens no model session.
Keep the selected foreground process alive with the host service manager.

Inbox notices and background-command completions are durable and at-least-once.
Notices addressed to `main` remain passive until the user asks the agent to
inspect them.

## Reference

- [CLI](docs/cli.md)
- [Configuration](docs/configuration.md)
- [Workflows](docs/wrk.md)
- [Operations](docs/operations.md)
- [Safety](docs/safety.md)
- [Internals](docs/internals.md)
- [Contributing](CONTRIBUTING.md)

shell3 is not an operating-system sandbox. Model-selected commands run with the
process's permissions. Use a container, VM, or restricted account when hard
isolation matters.

Linux, macOS, and WSL are supported. Native Windows is not. Licensed under the
[MIT License](LICENSE).
