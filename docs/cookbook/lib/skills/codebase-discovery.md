---
name: codebase-discovery
description: Use for any non-obvious code navigation or when reading beyond one clearly-local file; discover relevant code fast while keeping context lean
---

# Codebase Discovery Skill

Use this skill at task start and whenever scope is unclear. Context is managed
for you (shell3 auto-prunes old tool outputs and auto-compacts long history),
but what you *pull in* is up to you — the leaner your reads, the more useful
context survives.

## Discovery workflow

1. Frame the question in one line (what decision or change is needed).
2. Start broad but cheap: identify likely packages/files via `rg`, `fd`,
   `go list` — searches over dumps.
3. Build a short relevance map:
   - entrypoints
   - touched modules
   - tests covering behavior
4. Read only the smallest slices needed (`sed -n 'START,ENDp'`, focused `rg -C`
   context) — not whole files.
5. Edit/run/verify using that map; re-fetch a slice later rather than hoarding
   it now.

## Read-lean rules

- Prefer `rg`/`sed -n` slices over `cat` of a whole file; dump a full file only
  when you are about to edit most of it.
- If relevance is uncertain, skip it now and re-fetch later if needed.
- Capture understanding as a one-line note in your reasoning, then move on —
  don't re-read what you already summarized.
- Avoid speculative deep-dives without evidence they affect the requested
  change.

## Good examples

- Good: `rg "CreateClient"` to find candidates, read 20–40 relevant lines,
  summarize, move on.
- Good: Read one handler file slice and one test file slice, confirm behavior,
  then implement.

## Bad examples

- Bad: Dump entire package files "just in case".
- Bad: Read 8–10 files before forming a relevance map.
- Bad: Re-read a file you already summarized instead of trusting the summary.

## Expected behavior

- Be explicit about why each file/command is relevant.
- Each key claim in a user-facing summary maps to concrete evidence (file path,
  command, or observed output), not only narrative.
