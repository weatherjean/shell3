---
name: brainstorming
description: Refine vague requests into a concrete plan before implementation
---

# Brainstorming Skill

Use this skill **before writing code** when the request is ambiguous, broad, or high-risk.

## Goals

- Clarify what the user actually wants.
- Explore alternatives and tradeoffs.
- Produce a small, concrete plan the user can approve.

## Workflow

1. Restate the request in plain language.
2. Ask only the minimum high-value questions needed to remove ambiguity.
3. Propose 2-3 options (default + alternatives) with pros/cons.
4. Recommend one option.
5. Ask for explicit go-ahead before implementation.

## Rules

- Do not jump to coding while key requirements are unclear.
- Keep questions grouped and concise.
- Surface risks early (data loss, migrations, auth/secrets, backward compatibility).
- Prefer incremental, reversible changes.

## Output format

- **Understanding**
- **Open Questions**
- **Options**
- **Recommended Approach**
- **Implementation Plan (small steps)**
