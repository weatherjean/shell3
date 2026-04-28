---
name: executing-plans
description: Execute approved plans in small validated steps with clear progress updates
---

# Executing Plans Skill

Use this skill only when there is an actual written plan file in the project (for example `plan.md` or `docs/plan-*.md`) and it meets the **writing-plans** quality bar.

## Execution workflow

1. Preflight: locate the plan `.md` file and verify it has clear goal, ordered steps, scoped files, and validation commands.
2. If no adequate plan file exists, push back lightly and request/produce one before implementing.
3. Execute steps in order; do not silently change scope.
4. Before each edit, quickly confirm the step still matches code reality.
5. Make the smallest change that satisfies the step.
6. Validate incrementally during execution when helpful.
7. At the end, run full required project validation.
8. Summarize what changed, what was validated, and any follow-ups.

## Drift handling

If you discover a mismatch between plan and code:
- Minor mismatch: adapt step and continue, then report the adjustment.
- Major mismatch/risk increase: pause and ask user before proceeding.

## Rules

- Do not start implementation without a plan `.md` file that meets writing-plans standards.
- If asked to "just code" without such a plan for non-trivial work, push back lightly and create/ask for the plan first.
- Do not batch unrelated edits into one step.
- Do not skip validation when touching behavior.
- Prefer targeted file edits over broad rewrites.
- Keep user informed with brief progress checkpoints on longer tasks.

## Completion checklist

- Planned scope completed.
- No unrelated modifications introduced.
- Validation commands passed (or blockers clearly reported).
- Final summary references files changed and checks run.
