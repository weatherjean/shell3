-- cookbook: drop into ~/.shell3/lib/skills/ then require("lib.skills.codebase-discovery")
return shell3.skill({
  name        = "codebase-discovery",
  description = "Discover relevant code fast via broad search then aggressive context pruning.",
  body        = [[
---
name: codebase-discovery
description: Use for any non-obvious code navigation or when reading beyond one clearly-local file; discover relevant code fast and prune context aggressively
---

# Codebase Discovery Skill

Use this skill at task start and whenever scope is unclear.

## Discovery workflow

1. Start broad: identify likely packages/files via `rg`, `fd`, `go list`, and targeted reads.
2. Build a short relevance map:
   - entrypoints
   - touched modules
   - tests covering behavior
3. Read only the smallest slices needed (`sed -n`, focused `rg` context).

## Aggressive pruning policy

- After each file read or large tool result, default to pruning immediately.
- Keep output only if it is actively needed for the next step (edit, run, or verify).
- If relevance is uncertain, prune now and re-fetch later if needed.
- If a result is large and only partly useful, extract what matters, then prune the full output.
- Avoid dumping full large files unless strictly necessary.
- Keep active context limited to files/outputs directly related to the task.
- Prefer short summaries over raw output once understanding is captured.
- **Hard rule:** before any user-facing summary/progress update, prune all successful large tool outputs that are not required for the immediate next action.
- **Hard rule:** at the end of each discovery subtask, run an explicit context-hygiene sweep (prune stale large outputs, keep only active edit/test evidence).
- **Threshold rule:** treat successful outputs larger than ~4KB as prune-first unless they are immediately needed.
- **Traceability rule:** after pruning large outputs, retain or include minimal references for reported actions (commands run, files inspected/edited, tests executed, and relevant `tool_call_id`s).
- **Summary evidence rule:** each key claim in a user-facing summary must map to concrete evidence (file path, command, or tool result ID), not only narrative.

## Relevance rules

Keep information if it is:
- directly edited,
- directly executed/tested,
- or required to verify correctness.

Drop/deprioritize anything else.

## Workflow (repeat until done)

1. Frame the question in one line (what decision or change is needed).
2. Locate likely files quickly (`rg`, `fd`, `go list`), then read narrow slices.
3. Capture a tiny relevance map (entrypoint, implementation, tests).
4. After each read/result, decide immediately: keep briefly or prune now.
5. Edit/run/verify using only retained relevant context.
6. Prune stale outputs before moving to the next subtask.
7. Before any user-facing summary/update, perform a final context-hygiene sweep and prune all non-essential large successful outputs.

## Good examples

- Good: Use `rg "CreateClient"` to find candidates, read 20–40 relevant lines, summarize findings, prune full search output.
- Good: Read one handler file and one test file, confirm behavior, then prune both outputs before implementing.
- Good: Keep only outputs needed for an imminent edit or test run; prune everything else.

## Bad examples

- Bad: Dump entire package files "just in case" and keep them in context.
- Bad: Read 8–10 files before forming a relevance map.
- Bad: Keep large outputs when relevance is uncertain instead of pruning and re-fetching later.
- Bad: Continue exploring speculative paths without evidence they affect the requested change.

## Expected behavior

- Be explicit about why each file/command is relevant.
- Continuously trim context as confidence increases.
- Avoid speculative deep-dives without evidence they affect the task.
]],
})
