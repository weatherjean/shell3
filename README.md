<p align="center">
  <img src="docs/assets/shell3-banner.svg" alt="๑ï shell3: local orchestration and durable workflows" width="100%">
</p>

<p align="center">
  <a href="https://github.com/weatherjean/shell3/actions/workflows/ci.yml"><img src="https://github.com/weatherjean/shell3/actions/workflows/ci.yml/badge.svg" alt="CI status"></a>
</p>

shell3 turns one terminal-based agent into a local orchestrator for ordinary
Unix work and durable multi-agent workflows. Use it when a single agent is
enough for the conversation, but larger jobs need an inspectable task graph,
separate external runners, deterministic checks, restart recovery, or a
schedule.

The attached agent has two core tools: `bash` and `bash_bg`. Delegated work runs
through checked `*.wrk.lisp` workflows and typed external runners. Workers are
leaf processes and cannot launch more workflows. Telegram is an optional
remote-control adapter to the same runtime, not a second agent system.

## Why shell3

Most agent CLIs are good at one interactive process. shell3 keeps that simple
path, then puts an explicit boundary around delegation:

- the attached model gets a deliberately small Unix tool surface;
- substantial work is declared as strict, reviewable workflow data;
- runner commands are typed argv protocols rather than interpolated shell
  templates;
- workflow inputs, state, logs, checks, and artifacts survive process restarts;
- writers run alone, safe readers may run concurrently, and workers cannot
  recursively delegate.

If one agent CLI already handles the whole job, keep using it directly. shell3
earns its extra machinery when you want orchestration policy and durable
execution to be visible outside the model's conversation. The Lisp files are
inert data: shell3 parses a fixed schema and does not evaluate them as code.

## Install

shell3 supports Linux, macOS, and WSL. Native Windows is not supported. Runtime
commands require Bash. Building from source requires the Go version declared in
`go.mod`.

### Installer

The installer selects the latest release for the current OS and architecture,
downloads its prebuilt archive, and verifies it against the release checksum
when `sha256sum` or `shasum` is available:

```sh
curl -fsSL https://raw.githubusercontent.com/weatherjean/shell3/main/install.sh | sh
```

It installs to `~/.local/bin` by default. Set `PREFIX` to choose another
directory or `VERSION=vX.Y.Z` to pin a release. The script prints a warning if
the selected directory is not on `PATH`.

### Manual or source installation

Download an archive and `checksums.txt` from the
[latest release](https://github.com/weatherjean/shell3/releases/latest), verify
the archive, and place the `shell3` binary somewhere on `PATH`.

To inspect and build the source instead:

```sh
git clone https://github.com/weatherjean/shell3.git
cd shell3
make build
mkdir -p "$HOME/.local/bin"
install -m 0755 shell3 "$HOME/.local/bin/shell3"
```

## Quickstart

You need an OpenAI-compatible model endpoint, its model ID, and an API key.
`boot` writes one annotated configuration file and refuses to overwrite an
existing one:

```sh
shell3 boot
${EDITOR:-vi} "$HOME/.shell3/shell3.lisp"
```

In the generated `model` block, set the model ID and the environment-variable
name that will contain its API key. Replace the placeholder `base-url`, or
remove that form to use the SDK's default endpoint. Then validate the complete
kit, export the named secret in the process environment, and start shell3:

```sh
shell3 config check "$HOME/.shell3/shell3.lisp"
export SHELL3_API_KEY='your API key'
shell3
```

The default work directory is `~/.shell3/workdir`. For project-local config,
state, and commands, use `shell3 boot --here`, edit `./shell3.lisp`, validate
it, and start with `shell3 --here`. One-shot requests use `-p`:

```sh
shell3 -p 'Run the tests and summarize any failures.'
```

Secrets are read only from the inherited process environment. Never put a
credential value in `shell3.lisp`.

To add Telegram, tell the agent: `Set up Telegram remote control for this
project.`

## What is durable

An interactive turn may do concise work directly with Bash. For larger work, a
`*.wrk.lisp` file can declare agent, retry loop, deterministic command, and
human-wait nodes. shell3 snapshots the resolved config and workflow, advances
one dependency wave at a time, and retains the evidence needed to resume or
inspect the run.

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

For example, this graph asks one configured runner to inspect, gives fresh
runner processes up to three implementation attempts, and lets `make test`
rather than the worker's own claim decide when the loop is done:

```lisp
(task "checked-change"
  (parallel 2)

  (agent inspect
    (using builder)
    (prompt "Inspect the code and write $TASK_ARTIFACTS/findings.md.")
    (accept (file "findings.md")))

  (loop implement
    (using builder)
    (after inspect)
    (access write)
    (max 3)
    (prompt "Implement the requested change using the findings.")
    (until (sh "make test")))

  (command verify
    (after implement)
    (run "make test")))
```

This example assumes `builder` is an agent profile declared in `shell3.lisp`.
shell3 does not install or authenticate external agent harnesses; runner
profiles bind their real, inspected command-line protocols. See
[Configuration](docs/configuration.md#runners-and-agents).

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
