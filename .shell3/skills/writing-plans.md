---
name: writing-plans
description: Turn requests into concise, executable implementation plans before coding
---

# Writing Plans Skill

Use this skill when work is non-trivial, cross-file, risky, or has multiple possible approaches.

## Plan goals

- Define exactly what will be changed and why.
- Keep scope minimal and reversible.
- Make execution and validation obvious.

## Workflow

1. Run the **brainstorming** skill first when requirements are vague, high-risk, or multi-path.
2. Restate the target outcome in 1-2 lines.
3. Identify constraints and risks (compatibility, data loss, auth/secrets, migrations).
4. Inspect only enough code to map impacted files.
5. Write a short step-by-step plan (typically 3-7 steps).
6. Include explicit validation commands.
7. If ambiguity remains, ask focused questions before coding.

## Plan quality bar

A good plan is:
- **Concrete**: names files/components, not vague areas.
- **Ordered**: implementation sequence is logical.
- **Testable**: each step can be verified.
- **Minimal**: avoids unrelated refactors.

## Rules

- Do not skip brainstorming when ambiguity is still material.
- Push back lightly if asked to execute without enough clarity to produce a plan that meets this skill's quality bar.
- Prefer clarifying questions and narrowed scope over speculative planning.

## Communication format

- **Goal**
- **Constraints/Risks**
- **Proposed Plan**
- **Validation**
- **Questions (if any)**
