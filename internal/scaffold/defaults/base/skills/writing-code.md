---
name: writing-code
description: Use before any substantial implementation work in a repo — new features, bug fixes, refactors. Enforces test-first, red-green-refactor discipline over inline bash + edit_file.
---

# Write the failing test first

Adapted from Hermes Agent's `test-driven-development` skill (MIT, itself
credited "adapted from obra/superpowers"). shell3 has no dedicated coding
agent wired in — you do implementation work directly with `bash` and
`edit_file` — so this is the discipline that keeps that reliable.

**The rule:** no production code without a failing test first. If you
didn't watch a test fail, you don't know it tests the right thing — a test
that passes on its first run proves nothing.

## When

Always: new features, bug fixes, refactors, behavior changes. Skip only for
throwaway prototypes or generated/config files — and say so, don't decide
it silently. "Just this once" is the rationalization to watch for, not a
real exception.

For anything hard to reverse or multi-step, run this alongside the
`planning` skill — plan what you're building, then build it test-first.

## The cycle

**RED** — write one minimal test for one behavior. Real code, not mocks,
unless truly unavoidable. Name it for the behavior, not the implementation.

Run it with `bash` and confirm it fails, and fails for the right reason
(missing feature, not a typo). A test that passes immediately is testing
something that already exists — fix the test, not the code.

**GREEN** — write the smallest code that passes. Hardcoding a return value
or duplicating a line is fine here; you're not done, you're just green.
Don't add anything the test didn't ask for.

Run the one test, then the full suite, with `bash`. Both must pass, output
pristine. A short suite is one foreground `bash` call; anything that risks
the 120s cap belongs in `bash_bg` instead — start it, keep working, and
come back to the result rather than let a slow suite eat the turn.

**REFACTOR** — only once green. Remove duplication, improve names, extract
helpers. Keep re-running tests as you go; if one goes red, undo and take a
smaller step. Don't add behavior here.

Repeat per behavior. Don't write a pile of tests up front and then a pile
of implementation to match (horizontal slicing) — one RED→GREEN pair at a
time, each one teaching you the next test.

## Never claim it without running it

"It should work" is not evidence. Before telling the user something works
or is fixed: you ran the test with `bash`, you saw it fail before the fix
and pass after, and you ran the full suite, not just the new test. If you
can't point at that run, you don't get to make the claim.

## Red flags — stop and restart with TDD

Code before test. Test passes on the first run. Can't explain why a test
failed. "I already tested it manually." "Keep the code as reference while
I write tests around it" — you'll adapt it, which is testing after with
extra steps. Hit one of these: delete the code, start the cycle over.

## License

Hermes Agent's `test-driven-development` skill is MIT-licensed and traces
to obra/superpowers. shell3 is MIT (see `LICENSE`); this file is a cut-down,
rewritten adaptation for shell3's bash-first tools, kept under the same
license with attribution here.
