<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="shell3: local orchestration and durable workflows" width="100%">
</p>

shell3 is a small Go harness for one attached agent and checked multi-agent
workflows. Its local interface is a line-oriented terminal conversation.
Telegram can expose the same runtime remotely.

The attached agent has two tools: `bash` and `bash_bg`. It edits files with
ordinary project commands through `bash`; the starter kit prefers
[`sd`](https://github.com/chmln/sd) and includes lazy editing guidance with a
narrow portable-`sed` fallback. Delegated work runs through checked
`*.wrk.lisp` workflows and typed external runners. Workers are leaf processes;
they cannot launch workflows.

## Start

Go is the only build dependency. `sd` is a recommended runtime companion for
the attached agent, not something shell3 installs automatically.

```sh
make build
./shell3 boot
export YOUR_API_KEY=...
./shell3
```

`boot` writes `~/.shell3/shell3.lisp` and refuses to overwrite an existing
file. Edit the generated model declaration, including the environment variable
name for its API key, then validate it:

```sh
./shell3 config check ~/.shell3/shell3.lisp
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

One strict, inert `shell3.lisp` contains the model, orchestrator prompt, memory,
skills, runner protocols, worker profiles, schedules, and optional Telegram
settings. Unknown, duplicate, misplaced, contradictory, and unresolved forms
are errors.

Skills expose only their name and description in the prompt. The agent loads a
relevant body with `shell3 config skill` when needed. `shell3 boot` is the
canonical annotated configuration example.

## Workflows

A `*.wrk.lisp` file declares a durable task graph. Nodes can invoke typed agent
runners, execute deterministic commands, repeat fresh-agent loops until an
external check passes, or wait for an event.

```sh
shell3 wrk check change.wrk.lisp
shell3 wrk compile --config shell3.lisp change.wrk.lisp
shell3 wrk run --config shell3.lisp change.wrk.lisp 'Implement the change.'
shell3 wrk status TASK/RUN
```

Runs keep immutable inputs, state, logs, and artifacts under
`.shell3_project/wrk/`. See [Workflow reference](docs/wrk.md).

## Persistent hosts

Use one persistent host per project when schedules or immediate workflow wakeups
are required:

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
- [Workflows](docs/wrk.md)
- [Operations](docs/operations.md)
- [Safety](docs/safety.md)
- [Internals](docs/internals.md)
- [Contributing](CONTRIBUTING.md)

shell3 is not an operating-system sandbox. Model-selected commands run with the
permissions of the shell3 process. Use a container, VM, or restricted account
when hard isolation matters.

Linux, macOS, and WSL are supported. Native Windows is not. Licensed under the
[MIT License](LICENSE).
