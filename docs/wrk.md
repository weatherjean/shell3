# Wrk workflows

shell3 runs its Lisp-configured orchestrator through
a line-oriented terminal conversation. It prints an append-only, bounded
transcript and reads the next input; there is no alternate screen, viewport,
mouse handling, or terminal UI state. There is no legacy config loader. A
small embedded schedule clock invokes only durable wrkfiles; it never dispatches
hidden model turns. The attached model sees only `bash`, `bash_bg`, and
`edit_file`.

Assistant segments are rendered as terminal Markdown when each model round
finishes. Reasoning is represented by an animated rainbow `thinking` marker
rather than printed. Prompts, tools, results, and errors use a small palette
selected for the detected light or dark terminal background; redirected output
remains plain text. The activity marker occupies the line above the cursor and
is reclaimed when work finishes. Tool calls show their command or file path, and tool results
retain a bounded head and tail with an omission marker. Each bash result is
collapsed to one whitespace-normalized line of at most roughly 240 runes. The
local console accepts input only at its prompt; Escape cancels its active turn.
Telegram alone supports mid-turn steering. An in-process background command
completion writes one durable `main` filesystem-inbox notice; it never starts
a model turn or posts a second completion message. The unadvertised
`/test_output` diagnostic is answered by the host without a model turn and
prints a representative sample of the rendering pipeline for terminal checks.
Durable `main` inbox notices never start a turn or enter a prompt. Telegram
shows a host-owned `✉️` pending count and a bounded preview of the latest
notice; the local console shows the count. The user asks
the agent to load the `shell3-inbox` skill and use
`shell3 inbox list|read|archive` when ready.

Skills are `(skill NAME ...)` forms inside `shell3.lisp`. The orchestrator
indexes names and descriptions, then loads a selected body with `shell3 config
skill CONFIG NAME`; bodies are not paid for on unrelated turns. Stable explicit
preferences live in the top-level `(memory ...)` form. `shell3 boot` supplies
browser, web-search, harness-discovery, self-evolution, inbox, Telegram,
workflow-brainstorming, scheduling, and wrk-authoring skills in the one-file
starter kit. Memory must never contain credentials.

Telegram is optional and remains only a remote control surface:

```lisp
(telegram
  (token-env SHELL3_TELEGRAM_TOKEN)
  (home-chat 123456789)
  (allow-from 123456789)
  (max-concurrent-turns 4)
  (group-messages addressed))
```

The token value belongs in the process environment. `home-chat` is where
host-owned inbox alerts land. `allow-from` contains positive Telegram user
IDs, not chat IDs; it is required when the home chat is a group. Set
`group-messages` to `all` only when every allowlisted message in a group should
trigger shell3 rather than requiring an address or reply.

Create a separate directory for the experiment and write `shell3.lisp`:

```lisp
(shell3
  (version 1)

  (memory "Prefer concise completion reports.")

  (model primary
    (base-url "https://your-provider.example/v1")
    (api-key-env SHELL3_EXPERIMENT_API_KEY)
    (id "your-model-id")
    (reasoning medium)
    (max-tokens 16000)
    (context-window 128000))

  (orchestrator
    (model primary)
    (prompt """
You are the operator of shell3, a harness harness.
Use wrk workflows when delegation or verification loops add value.
"""))

  (skill wrk-authoring
    (description "Use when authoring or running a wrk workflow.")
    (instructions "Validate and compile before running."))

  (telegram
    (token-env SHELL3_TELEGRAM_TOKEN)
    (home-chat 123456789)
    (allow-from 123456789)))
```

`api-key-env` is a secret name, not a secret value. Export it from the
interactive shell profile or inject it through the service environment. An
absent or empty value fails closed. Validate the file, then start the console
from the project the agent should operate on:

```sh
export SHELL3_EXPERIMENT_API_KEY=...
shell3 config check /path/to/experiment/shell3.lisp
shell3 --config /path/to/experiment/shell3.lisp --workdir /path/to/project
shell3 telegram --config /path/to/experiment/shell3.lisp --workdir /path/to/project
```

For a non-interactive smoke test, pass `-p`:

```sh
shell3 --config /path/to/experiment/shell3.lisp \
  --workdir /path/to/project \
  -p 'Use bash to print the current directory, then tell me its basename.'
```

Runner agents used by wrkfiles are declared separately in the same root form.
Their command is typed argv data rather than shell interpolation:

```lisp
(runner codex
  (parameters (model string required))
  (command "codex" "exec")
  (arguments
    "--output-last-message" result-file
    "--cd" workdir
    "--model" model
    "-")
  (stderr log)
  (result (file result-file))
  (success (exit 0))
  (timeout "30m"))

(agent builder
  (using codex)
  (model "your-codex-model"))
```

Every runner receives its prompt on standard input, executes in the task root,
and writes `stdout.log`; these are protocol invariants rather than configurable
fields. `stderr` may be logged, merged, or discarded. `result` selects either
stdout or the `result-file` slot. Agent forms only bind a runner and its typed
parameters. Put task-specific instructions in each wrk node's `prompt`.

The runnable command set is `wrk check`, `wrk compile`, `wrk run`, `wrk beat`,
`wrk status`, `wrk signal`, and `wrk cancel`. A run advances until terminal or
waiting. `wrk signal` writes its event to the shared durable inbox; when the
local console is attached to the same project state, its bounded workflow
router reconciles and resumes the run within five seconds. Telegram and service
hosts also wake immediately when the advisory socket notification arrives.
Startup and periodic registry reconciliation recover signals accepted while no
adapter was alive. The headless `shell3 service` provides the same periodic
reconciliation when Telegram is not the persistent host.

If an immutable snapshot later fails integrity or strict parsing, its route is
quarantined with a `.invalid` suffix and one diagnostic is appended to the
rotating `.shell3_project/errors.jsonl`. Version 0 does not migrate or
repeatedly retry incompatible snapshots, and router diagnostics do not enter
the model inbox.

Foreground `wrk run` and manual `wrk beat` commands stream each node's
lifecycle and mirror the underlying runner or Bash command output as it is
produced. The durable `stdout.log`, `stderr.log`, `dispatch.err`, command, and
verification logs are still written. Automatic router beats do not print this
transcript. If `wrk run` has no request argument, piped input becomes the
request; an interactive terminal means an empty request and starts immediately.

Each run registers `wrk:<task>/<run-id>` under its notification state root.
This route points to the absolute run directory, so `--state` may place the
workflow state elsewhere. Set `--notify-state` to the attached project's
`.shell3_project` when using a non-default control root.

## Scheduled wrkfiles

A strict schedule declaration lives in `shell3.lisp` and names a wrkfile, not
an agent prompt or arbitrary executable:

```lisp
(schedule daily-report
  (cron "0 8 * * *")
  (timezone "Europe/Ljubljana")
  (run (wrkfile "workflows/daily-report.wrk.lisp"))
  (request "Produce the daily report.")
  (output "report.md")
  (timeout "30m")
  (overlap skip)
  (notify "main"))
```

The wrkfile path is relative to `shell3.lisp`; output is a clean relative path
beneath the run's artifacts directory. The timeout covers execution and time
spent waiting. `request` may be omitted for an empty request, `overlap` defaults
to `skip`, and `notify` defaults to `main`; the other fields are required.
`skip` prevents a new fire while that schedule has a running ledger row;
`allow` admits independent runs. Every actual invocation is indexed
in SQLite as `running`, `done`, or `failed`, and points to its immutable wrk run
directory and output file. A timeout or missing/non-regular output is a failed
run. Overlap skips are structured application-log events rather than fake runs.

Exactly one persistent process owns the project's schedule clock. Persist
`shell3 telegram` when remote control should stay online, or `shell3 service`
for a headless clock and workflow router. The process lock rejects a second
owner. Schedule declarations do not hot reload in version 1; restart the chosen
host after changing them or renaming a scheduled wrkfile's task. Use `shell3
schedule run NAME` for a manual proof and
`shell3 schedule history [NAME]` for JSONL inspection of the SQLite ledger.
An admitted run recovers after restart. Version 1 deliberately skips calendar
occurrences missed while no owner was running instead of replaying a catch-up
burst.

The wrkfile root is the task itself; there is no enclosing `wrk` or `version`
form. For example:

```lisp
(task "checked-change"
  (root ".")
  (parallel 1)

  (loop implement
    (using builder)
    (access write)
    (max 3)
    (prompt "Implement the request and verify it.")
    (until (sh "go test ./..."))))
```

Dispatched runner agents are leaf workers. Their prompt and environment tell
them to perform the assigned node directly, and `wrk run` rejects an accidental
nested workflow launch. The attached orchestrator remains the owner of graph
construction, dispatch, and verification. Every loop attempt launches a new
runner process automatically.
