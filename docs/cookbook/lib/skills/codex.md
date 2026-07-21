---
name: codex
description: Delegate coding/exploration tasks to the OpenAI Codex CLI — a full coding agent on this machine. Use for writing/refactoring code, running tests, or multi-step programming tasks in a repo
---

# Codex CLI Skill

`codex` (OpenAI Codex CLI) is a coding agent installed on this machine.
Hand it a whole programming task and get back a concise result — it
reads/edits files, runs commands, and iterates on its own inside the
project directory. You are the orchestrator; Codex is your delegated
worker with a **fresh, empty context** — the prompt is its entire
briefing, so make it self-contained (paths, steps, done-criteria).

## How to run

Always the non-interactive subcommand, from the project directory:

```bash
cd /path/to/project && codex exec --sandbox workspace-write "prompt"
```

- Final agent message goes to **stdout**; progress streams to stderr.
  `-o <path>` writes the final message to a file; `--json` emits JSON lines.
- Piped stdin becomes context: `cat notes.md | codex exec "instruction"`.
- `--cd <path>` sets the working directory instead of `cd`.
- Outside a git repo it refuses to run; `--skip-git-repo-check` overrides.
- The repo's `AGENTS.md` is its standing project brief.

### Sandbox levels (this is the permission model)

| Flag | Meaning |
|---|---|
| *(default)* | Read-only — codebase questions, research |
| `--sandbox workspace-write` | Edits + commands inside the project — normal coding tasks |
| `--sandbox danger-full-access` | Whole filesystem — avoid unless you fully trust the prompt |

There is no human to approve anything in `exec` mode: pick the narrowest
sandbox that lets the task finish. If a task stalls on access it lacks,
report that to the user rather than escalating on your own.

## Foreground vs background

A foreground `bash` call blocks your whole turn (and is capped at 120s) —
run anything non-trivial via `bash_bg`:

```json
bash_bg {
  command: "cd /path/to/project && codex exec --sandbox workspace-write 'task'"
}
```

Completion wakes you with the result. For a run whose finish needs no
immediate action, add `quiet: true` to queue clean exits for your next
turn instead (failures still wake you).

## Follow-ups

```bash
codex exec resume --last "now also add tests"
codex exec resume <SESSION_ID> "…"
```

Resuming keeps the session's accumulated context, so the worker doesn't
rediscover the codebase.

## Reporting

- Relay the final summary, not the transcript; pass failures on honestly.
- Never point it at the shell3 config dir (`~/.shell3/`) — edit that
  yourself via the self-evolve skill.
