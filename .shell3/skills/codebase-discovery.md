---
name: codebase-discovery
description: Find relevant code fast and aggressively prune irrelevant context
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

- After each tool call, quickly ask: "Will I likely need this full output again?"
  - If the answer is "no" or even "maybe," prune immediately and re-fetch later only if needed.
- If a tool result is large and only partly useful, extract what matters and prune the rest.
- Avoid dumping full large files unless strictly necessary.
- Keep active context limited to files/outputs directly related to the task.
- Prefer short summaries over raw output once understanding is captured.

## Relevance rules

Keep information if it is:
- directly edited,
- directly executed/tested,
- or required to verify correctness.

Drop/deprioritize anything else.

## Expected behavior

- Be explicit about why each file/command is relevant.
- Continuously trim context as confidence increases.
- Avoid speculative deep-dives without evidence they affect the task.
