---
name: planning
description: Use before any plan-first action (see "Free vs. plan-first" in your prompt) — anything hard to reverse, outward-facing, or multi-step. Recon, then a written plan the user approves before you touch anything.
---

# Recon, then a plan the user approves

## Hard gate

Do NOT execute, send, delete, or edit anything the plan covers until the user
has said yes to the written plan. "Too simple to need a plan" is where the
damage happens — a plan for a small change is three sentences, but it exists
and it gets a yes first.

## 1. Recon

Investigate read-only until you could defend every step of the plan:

- Look at what actually exists: the files, the state, the service, the
  history (`rg` over past runs if this came up before).
- Check `memory.md` and `projects.md` for decisions and pitfalls already
  recorded.
- Note what you could NOT verify — unknowns belong in the plan, not under it.
- If the request itself is ambiguous, ask one question at a time in chat
  before writing anything; prefer multiple choice.

## 2. Write the plan

One file per plan: `plans/YYYY-MM-DD-<topic>.md` in the config directory
(create `plans/` if missing). Keep it short and concrete — no "TBD", no step
that could be read two ways:

```markdown
# <Topic>

**Status:** awaiting approval

## Goal
What will be true when this is done.

## Recon
What you found: relevant state, constraints, unknowns you couldn't resolve.

## Steps
1. Numbered, concrete, in order. Name exact files/commands/recipients.

## Risk & rollback
What could go wrong; what's irreversible; how to undo the rest.

## Out of scope
What you are deliberately not doing.
```

If there are genuinely different ways to go, list 2–3 options with
trade-offs, lead with your recommendation, and let the user pick.

**Grill mode.** When the task is vague, broad, or high-stakes — or the user
says "grill me" / "stress test this" — go deeper before writing: walk the
decisions one question at a time, each with your recommended answer and why,
and answer yourself anything the machine can answer (files, history, state)
instead of asking. Stop when the plan is safe to execute, not before, and
don't interrogate for its own sake.

## 3. Approval

Tell the user the plan is at its path and summarize it in a few lines in
chat (they can also open it in the dashboard file explorer). Then wait.

- Explicit yes → set **Status: approved**, execute, and report with evidence.
- Requested changes → revise the same file, re-share, wait again.
- Silence is not approval. Never start "while you wait".

## 4. Afterwards

- Update the status footer: `done <date>` (or `abandoned` — say why).
- Record durable outcomes in `projects.md` / `memory.md` as usual; the plan
  file stays as the audit trail.
