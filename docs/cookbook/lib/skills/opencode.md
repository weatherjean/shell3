---
name: opencode
description: Delegate coding/exploration tasks to opencode — a full coding agent on this machine. Use for writing/refactoring code, running tests, or multi-step programming tasks in a repo
---

# opencode Skill

`opencode` is a coding agent installed on this machine. Hand it a whole
programming task and get back a concise result — it reads/edits files, runs
commands, and iterates on its own inside the project directory. You are the
orchestrator; opencode is your delegated worker with a **fresh, empty
context** — the prompt is its entire briefing, so make it self-contained
(paths, steps, done-criteria).

## How to run

The non-interactive subcommand (never bare `opencode` — that opens a TUI),
from the project directory:

```bash
cd /path/to/project && opencode run "prompt"
```

- Result prints to stdout, then it exits. `--format json` for raw events.
- `-m provider/model` picks the model; `--agent <name>` picks a defined
  agent profile; `-f <path>` attaches files to the message.

### Permissions — read before delegating

`run` is non-interactive: an action the project's opencode permission
config would ask about **cannot prompt**, so a scoped task may stall or
fail on access instead. Passing `--auto` auto-approves every permission
not explicitly denied — dangerous: the worker can then edit and execute
anything your user can reach. Scope with agent profiles instead:

```bash
opencode run --agent plan "how does auth work here?"   # built-in read-only agent
```

If a task stalls on access, report that to the user rather than
silently widening (no `--auto` without an explicit OK).

## Foreground vs background

A foreground `bash` call blocks your whole turn (and is capped at 120s) —
run anything non-trivial via `bash_bg`:

```json
bash_bg {
  command: "cd /path/to/project && opencode run 'task'",
  force_wake: true
}
```

`force_wake: true` wakes you the moment it finishes even on success;
default false queues clean completions for your next turn.

## Follow-ups

```bash
opencode run -c "now also add tests"          # continue the last session
opencode run -s <session-id> "…"              # continue a specific session
```

Continuing keeps the session's accumulated context, so the worker doesn't
rediscover the codebase.

## Reporting

- Relay the final summary, not the transcript; pass failures on honestly.
- Never point it at the shell3 config dir (`~/.shell3/`) — edit that
  yourself via the self-evolve skill.
