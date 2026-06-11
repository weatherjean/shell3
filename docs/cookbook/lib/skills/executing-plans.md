---
name: executing-plans
description: Execute approved plans with safe git workflow, scoped commits, and validation
---

# Executing Plans Skill

Use this skill after a plan has been written and approved. This skill includes the git workflow; do not use a separate git skill.

## Preflight: protect user work

Before editing, always run a git status check in the relevant repo:

```bash
git status --short --branch
```

Then:

1. If uncommitted changes exist, determine whether they are from this session and related to the current task.
2. If changes may be unrelated or user-owned, stop and ask what to do: keep mixed state, commit, stash, discard, or move to another branch.
3. Do not discard, overwrite, stage, or commit user work without explicit approval.
4. Always request or create/switch to a dedicated feature branch before implementation for any work that could be committed.
5. If changes already exist on `main`, stop and ask whether to move them to a feature branch before committing. Do not commit directly to `main` unless the user explicitly says to commit directly to `main`.

Branch naming: use a short descriptive branch name such as `feat/help-rendering` or `chore/update-skills`.

## Execution workflow

1. Confirm the approved plan still matches the code/config reality.
2. Execute steps in order; do not silently expand scope.
3. Make the smallest edit that satisfies each step.
4. Validate incrementally when useful.
5. Commit logical checkpoints as you go when the user has approved committing for this task.
6. At the end, run the plan's validation plus project-standard validation when code behavior changed.
7. Summarize files changed, validation run, commits made, and any follow-ups.

## Commit and merge workflow

- Keep commits scoped and reviewable while executing.
- Never include unrelated files.
- Commit messages should be concise and conventional when possible.
- When the user approves the final result, squash the task branch and merge to `main` only with explicit confirmation.
- Never push automatically.
- After merge or final approval, offer to push.

## Drift handling

If the plan and reality diverge:

- Minor mismatch: adapt narrowly and report the adjustment.
- Major mismatch, new risk, or scope increase: pause and ask before proceeding.

## Validation rules

- Do not skip validation for behavior changes.
- If validation cannot be run, explain why and provide the exact command the user should run.
- Before calling work complete, ensure no unrelated modifications were introduced.

## Completion checklist

- Planned scope completed.
- Working tree state reviewed.
- Relevant commits created if approved.
- Required validation passed or blockers reported.
- Final summary references changed files and checks run.
