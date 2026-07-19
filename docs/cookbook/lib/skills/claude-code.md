---
name: claude-code
description: Delegate coding/exploration/research tasks to Claude Code — a full coding agent on this machine. Use for writing/refactoring code, running tests, git work, codebase exploration, documentation research, or any multi-step programming task in a repo
---

# Claude Code Skill

`claude` (Claude Code) is a coding agent installed on this machine. You can
hand it a whole programming task and get back a concise result — it
reads/edits files, runs commands, and iterates on its own inside the
project directory.

**You are the orchestrator** (shell3 agent). Claude Code is your delegated
worker. The key to effective delegation is a precise, self-contained prompt
and understanding the subagent permission model.

---

## How to run

Always non-interactive (`-p`), always from the project directory:

```bash
cd /path/to/project && claude -p "prompt" --output-format text --permission-mode acceptEdits
```

### Sandbox: path permissions

By default, Claude Code can only read/write files inside the project
directory. If the task needs to read files outside it (e.g. `~/.shell3/`
skills or config), use `--add-dir`:

```bash
cd /path/to/project && claude -p "prompt" --add-dir ~/.shell3 \
  --output-format text --permission-mode acceptEdits
```

Multiple dirs: `--add-dir ~/.shell3 --add-dir ~/other`

**Dangerous alternative** (full filesystem access — avoid unless you trust
the prompt completely):
```
--dangerously-skip-permissions
```

### Permission modes

| Mode | When to use |
|------|------------|
| `acceptEdits` | Coding tasks — lets it edit files without prompting |
| *(omit)* | Read-only questions about a codebase (no flags needed) |
| `auto` | Full autonomy — approves all tool calls automatically |
| `bypassPermissions` | Maximum autonomy (use cautiously) |

Note: `-p` mode has no human to answer prompts. If a task stalls because
it needs permissions beyond what the mode grants, report that to the user
and let them decide.

---

## Subagent types Claude Code spawns internally

When you ask Claude Code to do research or exploration, it can internally
spawn subagents with different profiles. Know these so you can steer:

| Subagent | Model | Tools | Use for |
|----------|-------|-------|---------|
| **Explore** | Haiku (fast/cheap) | Read-only (no Write/Edit) | Codebase search, file discovery, doc crawling |
| **Plan** | Inherits main model | Read-only | Research before presenting a plan |
| **General-purpose** | Inherits main model | All tools | Multi-step tasks needing both research and action |

**Rule of thumb:** Ask Claude Code to "use an Explore subagent" for cheap
read-only research. For larger coding tasks, the General-purpose agent
handles the full cycle (plan → implement → test).

---

## Prompt crafting (critical)

A subagent starts with a **fresh, empty context** — it inherits nothing
from your conversation. The prompt is its entire briefing.

### Good prompt = everything in one message

```
In the shell3 codebase at ~/code/agents/shell3:

1. Find where bash_bg's argument schema is defined (look in internal/chat/ 
   or internal/llm/ for tool definitions)
2. Add an optional boolean parameter "force_wake" defaulting to false
3. Plumb it through to internal/shell3/jobs.go's finishCommand so that 
   when force_wake=true, even exit-0 jobs inject a Wake event
4. Update any related types or callers
5. Build: go build -o shell3 .
6. Return a summary of what you changed

Do not touch ~/.shell3/ — that's external config.
```

### Bad prompt (too vague)

```
Fix the bash_bg tool
```

### What the subagent DOES get automatically
- Working directory and CLAUDE.md from the project
- Git status snapshot (taken at session start)
- Any environment the project inherits

### What it does NOT get
- Your conversation history
- Files you've already read in this session
- Config or secrets from ~/.shell3/
- Any context you didn't put in the prompt

---

## Foreground vs Background (important!)

Tasks run via `bash_bg` are **background subagents**.

| | Foreground (direct `claude -p`) | Background (`bash_bg` wrapping `claude -p`) |
|---|---|---|
| Blocks your chat? | Yes, until done (and foreground bash is capped at 120s — longer runs get killed) | No, you keep working |
| Permission prompts | Auto-accepted (by mode flag) | **Auto-denied** — tool calls needing approval silently fail |
| Wake on completion | N/A (blocking) | Clean exit → queued silently; Nonzero exit → wakes you |
| Best for | Quick tasks, tasks needing permissions | Long tasks, parallel work |

**Critical failure mode:** If a background task needs a permission you
didn't grant via the mode flag, it **silently fails** that tool call and
continues without it. The subagent doesn't tell you. The result looks
plausible but is incomplete.

**Fix:** Set `--permission-mode auto` (or `bypassPermissions`) for
background tasks that might need elevated permissions. For read-only
research, no special flag is needed.

### The force_wake parameter

`bash_bg` accepts an optional `force_wake` boolean. Use it to get notified
immediately on clean (exit 0) completions:

| `force_wake` | Exit 0 (clean) | Nonzero exit |
|---|---|---|
| `false` (default) | Queued silently — arrives on your next turn | Wakes you immediately |
| `true` | Wakes you immediately | Wakes you immediately |

```json
bash_bg {
  command: "cd ~/code/agents/shell3 && claude -p 'task' --output-format text --permission-mode acceptEdits",
  force_wake: true
}
```

Defaults to `false` to keep the chat quiet during routine long tasks. Set
to `true` when you need to react as soon as the task finishes, regardless
of success/failure.

---

## Parallel fan-out

For independent investigations, spawn multiple Claude Code instances
concurrently — one `bash_bg` call each (never `&`/`wait` in foreground
`bash`: a foreground call blocks your turn and is capped at 120s):

```json
bash_bg { command: "cd ~/code/agents/shell3 && claude -p 'Research authentication module...'" }
bash_bg { command: "cd ~/code/agents/shell3 && claude -p 'Research database layer...'" }
bash_bg { command: "cd ~/code/agents/shell3 && claude -p 'Research API routes...'" }
```

Each runs in its own context window with no cross-talk. Only summaries
return. Works best when the tasks don't depend on each other.

---

## Follow-ups with --continue

```bash
cd ~/code/agents/shell3 && claude --continue -p "now also add tests for the force_wake parameter"
```

This continues the **most recent Claude Code session** in that directory,
preserving its accumulated context. Useful for iterative refinement without
making the subagent rediscover everything.

---

## When NOT to delegate

Delegation trades a clean context window against a cold start. Don't
delegate when:

- The task needs frequent back-and-forth with the user
- Multiple phases share heavy context you'd have to re-explain
- It's a quick targeted change (< 10 seconds for you to type)
- Latency matters — a subagent spends ~15-30s gathering context

---

## Custom subagent definitions (advanced)

Claude Code reads custom subagent definitions from `.claude/agents/*.md`.
When you need a reusable specialist agent (e.g. a code reviewer, a security
auditor, a docs writer), define it there. The frontmatter supports:

```yaml
---
name: code-reviewer
description: Reviews code for quality and best practices
tools: Read, Glob, Grep          # allowlist — omit = all tools
disallowedTools: [Edit, Write]   # denylist
model: sonnet                    # or haiku, opus, inherit (default)
permissionMode: acceptEdits
maxTurns: 15                    # ceiling on agentic turns
isolation: worktree             # run in a temp git worktree
background: false               # always foreground by default
skills: [skill-name]            # preload skill content
memory: project                 # persistent directory across sessions
---
```

You can reference these in delegation prompts: "Use the code-reviewer
subagent on the auth module." Or `@-mention` the agent name in Claude Code
interactive sessions.

---

## Reporting results

- Relay Claude Code's **final summary**, not its full transcript.
  Cap long output to the last 40-50 lines unless the user asks for details.
- If it reports failures (tests red, blocked on something), pass that on
  honestly — don't retry silently.
- If it mentions interesting sub-findings, include those concisely.
- **Never run it on `~/.shell3/` itself** — edit that yourself via the
  self-evolve skill.
