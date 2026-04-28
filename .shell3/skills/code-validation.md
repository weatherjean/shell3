---
name: code-validation
description: Verify correctness before completion with repo-standard checks
---

# Code Validation Skill

Run validation before declaring work complete.

## Required validation steps

1. Format changed code as needed.
2. Run project-standard checks:

```bash
go vet ./... && go test ./...
```

## Validation rules

- If checks fail, do not claim completion.
- Report failing command + key error output.
- Fix and re-run until green, or clearly state blockers.

## Scope guidance

- For quick iteration, package-level tests are okay during development.
- Before final handoff, run full required checks.

## Communication

Always report:
- commands run,
- result (pass/fail),
- any skipped checks and why.
