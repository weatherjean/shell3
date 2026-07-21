---
name: pi
description: Delegate coding/exploration tasks to pi — a minimal coding agent on this machine. Use for writing/refactoring code, running tests, or multi-step programming tasks in a repo
---

# pi Skill

`pi` is a deliberately minimal coding agent installed on this machine
(tools: read, bash, edit, write). Hand it a whole
programming task and get back a concise result. You are the orchestrator;
pi is your delegated worker with a **fresh, empty context** — the prompt is
its entire briefing, so make it self-contained (paths, steps,
done-criteria).

## How to run

Print mode (`-p`), from the project directory:

```bash
cd /path/to/project && pi -p "prompt"
```

- Response prints to stdout, then it exits. `--mode json` for event lines.
- Piped stdin is context: `cat README.md | pi -p "summarize this"`.
- Model: `--model provider/id` (e.g. `--model openai/gpt-5.2`), or
  `--provider <name> --model <pattern>`; a `:high` suffix raises the
  thinking level.

### Permissions — read before delegating

pi has **no sandbox and no approval prompts by design**: its bash tool
runs with your user's full rights, immediately. Only hand it prompts you
fully trust, keep tasks scoped to the project directory, and use the
`--tools` allowlist to drop what a task doesn't need:

```bash
pi -p --tools read "how does auth work here?"        # read-only research
pi -p --tools read,edit,write "refactor X"           # edits but no commands
```

If a task stalls on a tool you filtered away, report that to the user
rather than silently widening.

## Foreground vs background

A foreground `bash` call blocks your whole turn (and is capped at 120s) —
run anything non-trivial via `bash_bg`:

```json
bash_bg {
  command: "cd /path/to/project && pi -p 'task'"
}
```

Completion wakes you with the result. For a run whose finish needs no
immediate action, add `quiet: true` to queue clean exits for your next
turn instead (failures still wake you).

## Follow-ups

```bash
pi -c -p "now also add tests"            # continue the most recent session
pi --session <id> -p "…"                 # continue a specific session
```

Continuing keeps the session's accumulated context, so the worker doesn't
rediscover the codebase.

## Reporting

- Relay the final summary, not the transcript; pass failures on honestly.
- Never point it at the shell3 config dir (`~/.shell3/`) — edit that
  yourself via the self-evolve skill.
