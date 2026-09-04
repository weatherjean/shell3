# Configuration

`shell3.lisp` is inert data. It contains exactly one versioned root form:

```lisp
(shell3
  (version 1)
  ...)
```

Unknown, duplicate, misplaced, contradictory, and unresolved forms fail
validation. Strings may use ordinary quotes or triple quotes. Symbols name
declarations and value slots; they are not evaluated.

```sh
shell3 boot
shell3 config check /path/to/shell3.lisp
shell3 config skill /path/to/shell3.lisp NAME
```

`boot` writes the annotated reference kit and never overwrites. `config check`
also validates scheduled wrkfiles. It does not resolve secrets, call a model,
or run commands.

## Root forms

| Form | Cardinality | Purpose |
|---|---:|---|
| `version` | exactly one | Format version; currently `1`. |
| `memory` | zero or one | Stable user-supplied context. |
| `define` | zero or more | String or integer constant for runner argv. |
| `model` | zero or more | OpenAI-compatible attached-model endpoint. |
| `orchestrator` | zero or one | Model and base prompt for attached sessions. |
| `skill` | zero or more | Lazily loaded guidance. |
| `runner` | zero or more | Typed external process protocol. |
| `agent` | zero or more | Bound runner profile used by wrk nodes. |
| `schedule` | zero or more | Calendar trigger for one wrkfile. |
| `telegram` | zero or one | Telegram adapter settings. |

Conversation commands require an `orchestrator`; workflow-only configuration
does not.

## Attached model

```lisp
(model primary
  (base-url "https://provider.example/v1")
  (api-key-env SHELL3_API_KEY)
  (id "model-id")
  (reasoning medium)
  (max-tokens 16000)
  (context-window 128000))

(orchestrator
  (model primary)
  (prompt "Coordinate the work."))
```

`api-key-env` and `id` are required. `base-url` is optional and otherwise uses
the SDK default. `reasoning` defaults to `medium`; accepted values are `none`,
`minimal`, `low`, `medium`, `high`, and `xhigh`. `max-tokens` defaults to
`16000`; the current adapter sends `xhigh` as `high`. A positive
`context-window` enables usage reminders, tool-output pruning, and automatic
compaction thresholds.

Secret fields name environment variables. Secret values never belong in Lisp.
They are resolved only when the corresponding runtime starts or reloads.

`memory` is appended to every attached-model prompt. A skill requires
`description` and `instructions`; only its name and description enter the base
prompt. `config skill` retrieves its body when needed.

## Runners and agents

```lisp
(define codex-bin "codex")

(runner codex
  (parameters
    (model string required)
    (profile string optional "automation"))
  (command codex-bin "exec")
  (arguments
    "--output-last-message" result-file
    "--cd" workdir
    "--model" model
    (optional profile "--profile" profile)
    "-")
  (stderr log)
  (result (file result-file))
  (success (exit 0))
  (timeout "30m"))

(agent builder
  (using codex)
  (model "model-id"))
```

`command` and `result` are required. `arguments` are exact argv entries, not a
shell template. Literals, constants, declared parameters, and these runtime
slots are accepted:

```text
agent-name  prompt-file  result-file  workdir
task-id     task-run     task-root    task-artifacts  task-attempt
```

Parameters are strings and are `required` or `optional`; an optional parameter
may have a default. `(optional NAME ARG...)` emits its argv only when `NAME` is
non-empty.

`stderr` is `log` (default), `merge`, or `discard`. `result` is `stdout` or
`(file result-file)`. The success exit defaults to `0`; the process timeout
defaults to 30 minutes. Each invocation retains its prompt and output logs.

Agent runners inherit the host environment except all declared attached-model
and Telegram secret variables. Any other runner credential must be injected by
the operator.

## Telegram

```lisp
(telegram
  (token-env SHELL3_TELEGRAM_TOKEN)
  (home-chat 123456789)
  (allow-from 123456789)
  (max-concurrent-turns 4)
  (group-messages addressed))
```

`token-env` and non-zero `home-chat` are required. `allow-from` contains
positive user IDs and is required when `home-chat` is a group. Concurrency
defaults to `4`. `home-chat` receives lifecycle and main-inbox alerts.

Group policy is `addressed` (default) or `all`. Addressed mode accepts `/ask`,
replies, and mentions delivered by Telegram; `all` accepts every delivered
message from an allowed sender. A group's title and description enter its
prompt as untrusted context.

Valid changes can reload between turns except `token-env`, `home-chat`, and
schedules, which require restarting the Telegram process.

Schedules are documented with their execution model in [Workflows](wrk.md).
