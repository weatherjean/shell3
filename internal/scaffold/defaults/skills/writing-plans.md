---
name: writing-plans
description: MUST READ before any non-super-minimal code/config/docs change; mandatory planning and approval gate before editing
---

# Writing Plans Skill

Before making any code, config, docs, workflow, or other project changes for a non-super-minimal task, first read this skill file and follow it.

Use this skill before implementation for every change unless it is **super minimal**.

This is the single planning skill. It includes clarification and "grill me" style design stress-testing.

## Super minimal exception

Only skip this skill when all of these are true:

- The edit is single-file or otherwise completely obvious.
- There is no behavior, data, security, UX, workflow, or compatibility impact.
- There is no cross-file coordination.
- There is no meaningful risk to user work or data.
- Validation is trivial.
- The user has not asked for planning, review, clarification, stress-testing, or to be "grilled".

If any condition is false, use this skill.

## Goals

- Reach shared understanding before coding.
- Identify what is known, assumed, and still uncertain.
- Stress-test the request enough to avoid preventable rework.
- Resolve dependent decisions in the right order.
- Produce a concrete, minimal, reversible, testable plan.
- Ask the user only for decisions they must make.

## When to grill harder

Use a deeper questioning/stress-test mode when the task is vague, broad, high-risk, product/UX-facing, architectural, data-affecting, security-sensitive, migration-heavy, or when the user says things like "grill me", "stress test this", "ask questions", "think through this", or "help me plan".

In grill mode:

- Walk the design tree branch-by-branch.
- Resolve dependencies between decisions one at a time.
- Ask one question at a time when a user answer is needed.
- For each question, include your recommended answer and why.
- If a question can be answered by reading code, docs, config, tests, history, or memory, inspect those instead of asking the user.
- Continue until the plan is clear enough to execute safely; do not interrogate for its own sake.

## Workflow

1. **Classify the task**
   - Decide whether it is super minimal. If not, continue.
   - Decide whether lightweight planning is enough or grill mode is warranted.

2. **Restate the target outcome**
   - Summarize the request in 1-2 lines.
   - Name the likely deliverable: code change, config change, design decision, investigation, docs, etc.

3. **Discover only enough context**
   - Search memory/history for non-trivial tasks or when the user references previous work.
   - Use `codebase-discovery` for non-obvious code navigation or when reading beyond one clearly-local file.
   - Read existing files before proposing edits.
   - Answer codebase-answerable questions yourself; do not ask the user to explain what the repo can show.

4. **Surface assumptions and risks**
   - Separate facts from assumptions.
   - Identify risks: data loss, migrations, auth/secrets, destructive operations, security/privacy, backwards compatibility, user workflow/UX, performance, flaky validation, and local uncommitted work.
   - Push back lightly on unsafe, over-broad, or under-specified requests.

5. **Clarify only what blocks a good plan**
   - Ask the minimum high-value questions.
   - Ask one question at a time when the answer changes later questions or the design direction.
   - Provide your recommended answer with each question.
   - If the ambiguity is mild and reversible, state an assumption and proceed.

6. **Compare options when useful**
   - Present 2-3 realistic approaches only when there is a meaningful tradeoff.
   - Include recommendation and rationale.
   - Prefer incremental, reversible approaches.

7. **Write the plan**
   - Keep it short and ordered, usually 3-7 steps.
   - Name impacted files/components when known.
   - Include validation commands/checks.
   - Include any explicit non-goals.
   - Ask for approval before implementation when the work is non-trivial, risky, or user intent is still uncertain.

## Plan quality bar

A good plan is:

- **Concrete**: files/components and intended edits are named where possible.
- **Ordered**: steps can be executed sequentially.
- **Testable**: validation is explicit.
- **Minimal**: avoids unrelated refactors or speculative scope.
- **Reversible**: avoids destructive operations unless explicitly approved.
- **Honest**: assumptions, risks, and unknowns are visible.

## Output formats

### Standard planning output

Use this for most non-trivial tasks:

- **Goal**
- **Context / Findings** when discovery was needed
- **Assumptions**
- **Constraints / Risks**
- **Proposed Plan**
- **Validation**
- **Questions** if any

### Grill-mode question output

When asking one blocking question at a time:

- **Question**
- **Why it matters**
- **Recommended answer**
- **If you agree**: say what plan direction follows

## Rules

- Read this skill file before making edits for any non-super-minimal task.
- Do not jump from discovery straight into coding for non-trivial work.
- Do not make edits before presenting a plan and receiving approval when the task is non-trivial, risky, or user intent is still uncertain.
- Do not ask questions the codebase, docs, config, tests, memory, or history can answer.
- Do not over-question obvious or easily reversible choices.
- Do not produce a sprawling plan when a short plan is enough.
- Do not use this skill as permission to stall; bias toward a clear recommendation.
- Once the user approves the plan, use the **executing-plans** skill.
