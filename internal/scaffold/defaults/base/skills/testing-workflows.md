---
name: testing-workflows
description: Use when testing a tool, an agent, or a workflow you just built or changed — what a passing tool test does and does not prove, and how to run a real turn test instead.
---

# Testing a workflow

**`shell3 tool run` and `shell3 tool test` prove plumbing, never behaviour.**

They bind arguments, run the shell function, and check what it printed. That
is worth having — it catches a typo'd param, a broken SQL statement, a missing
`chmod +x`. It is not evidence that the workflow works, because it never asks
the question the workflow exists to answer.

The bug an isolation test cannot see is the one where the work is in the wrong
layer. A tool that drafts prose, scores a lead, or picks a winner passes every
isolation test it has — while doing a job that belonged to a turn.

## A turn test dispatches a real agent

    shell3 ask --agent <name> -p "<the real task, phrased the way a user would>"

Stdout is the reply and nothing else, so it is safe to read or pipe.
Diagnostics go to stderr; a failed or empty run exits non-zero.

Then read what actually happened:

- **Which tools did it reach for, and in what order?** A workflow that works
  for the wrong reason usually shows up here first.
- **What did it decide, and did it say why?** If the decision arrived with no
  reasoning, a tool probably made it.
- **What did it not do?** A step silently skipped is the failure mode a green
  tool test hides best.

When you need the exact evidence later, the history skill can send the stored
run replay with the system prompt each turn was rendered with.

## Which test to write

| you changed | test |
|---|---|
| a tool's arguments, query, or file writes | `shell3 tool test` — plumbing is exactly what changed |
| what a tool *returns* to the agent | a turn test — the agent is the consumer |
| an agent's prompt, or which tools it has | a turn test; there is nothing else to test |
| a cron job | `/run <job>` on the live bot, then read the report it produced |

## Do not conclude from a green test alone

Say what you ran and what you saw. "The tool test passes" and "the workflow
works" are different claims, and only the second one is what was asked.
