---
name: git-workflow
description: Safe branch workflow with strict handling of unrelated local changes
---

# Git Workflow Skill

Use this skill for any change that may be committed.

## Branch strategy

- Work on a dedicated **feature branch**.
- Keep commits scoped to the task.
- Prefer a clean history intended for **squash merge into `main`**.

## Uncommitted-changes safety rule (mandatory)

Before editing:
1. Check working tree status.
2. If there are uncommitted changes unrelated to this session, **stop**.
3. Ask the user how to resolve first (commit, stash, discard, or separate branch).
4. Do not proceed until state is clean or user explicitly accepts the mixed state.

## Commit hygiene

- Commit only when user asks.
- Never include unrelated files in a commit.
- Summarize what changed and why.

## Push/merge hygiene

- Push only when user asks.
- Do not merge without user confirmation.
- For completion, propose: squash merge PR to `main`.
