shell3 is a minimal Unix-composable coding agent (Go). We're doing iterative pre-release passes on the feat/bash-first branch. Bar: OPEN SOURCE, no backwards-compat, no two-ways-of-doing-the-same-thing, no dead code, no hackiness — minimalism and good taste. Design: the agent's only verbs are `bash` and `edit_file`; custom tools are declarative bash-command templates (shell3.tool{command=...}, no Lua handler); subagents are backgrounded subprocesses; skills are .md files; context is host-managed auto-compaction; the only safety surface is shell3.wrap_bash. "Lua is king": Lua holds policy, Go provides mechanism.

Passes 1 and 2 cleaned the NON-test Go. This is PASS 3: the TEST surface. The test surface is ~1:1 with code (132 files, ~16.1k lines vs ~16.8k code lines) and has NOT been reviewed for quality. Make it the same quality as the code surface: every test earns its place by pinning a real behavioral contract.

STATE (do not undo, do not re-flag):
- Working tree is dirty on feat/bash-first (passes 1+2, uncommitted). `go build ./...`, `go vet ./...`, `go test ./...` are all GREEN. `ls ~/.shell3/projects | wc -l` is 344 and MUST stay 344 across a test run (a pass-1 TestMain $HOME redirect in pkg/shell3, internal/telegram, internal/telegram/web enforces this — keep any Runtime-building tests under that redirect).
- Pass 1 removed: telegram inline-callback/approval code; agentsetup.Build + dead Options fields; the luacfg.ResolvedCall duplicate (ResolveCustomCall returns chat.ResolvedTool); comment cruft; some swallowed errors; bg_done/agent_done magic strings (now sink.KindBgDone/KindAgentDone); the test home-dir leak.
- Pass 2 removed/fixed: compactInto's dead `out` return + freed loop; telegram Bot.dashURL dead field+param (NewBot is now 4 args); patchapp.dispatchSlash's unused bool return; inlined luacfg.lockVM (deleted dispatch.go); documented store.go prev/next QueryRow best-effort discard; auth.go double FormatInt.
- Confirmed FALSE-POSITIVES from pass 2 — do NOT resurrect: turn.go nil-deref & lastPromptTokens "bugs", openai SetExtra, convert.go luaToGo (already unexported), AudioFormat, ParamSetter/ParamDescriber merge (kept separate).

MISSION: Read ALL Go — code AND tests — and judge each TEST file/function: GOOD (keep) or BAD/OLD (cut or rewrite). Produce a deduplicated, severity-ranked verdict. Deliverable is a PLAN; do NOT edit until I approve. After approval, deletions/rewrites must keep the suite green and the project count at 344.

What makes a test BAD/OLD (hunt these — HIGH unless noted):
1. Orphaned — tests a feature/branch/type removed in build or passes 1–2 (approval flow, compactInto byte-count output, dashURL, a dispatchSlash bool, removed Lua tools/skill tool). HIGH.
2. Tautological/vacuous — no meaningful assertion, asserts a constant, only checks "doesn't panic", or asserts the fake it just configured. HIGH.
3. Change-detector — couples to internals (exact private field values, mock call-order, exact log/printf strings) instead of an observable contract; breaks on harmless refactors. MEDIUM/HIGH.
4. Duplicate coverage — multiple tests on the identical path with no added boundary; collapse to one. MEDIUM.
5. Misnamed/misleading — name claims X, asserts Y; or a "Test…" that's a scratch driver. MEDIUM.
6. Brittle/non-deterministic — real sleeps, wall-clock, network, ordering assumptions, real $HOME/cwd writes outside a temp dir or the TestMain redirect, goroutine races. HIGH.
7. Over-mocked — verifies the mock, not the unit under test. MEDIUM.
8. Disabled/rotting — t.Skip with no live reason, commented-out bodies, dead test helpers, helpers duplicated across files that should be shared. MEDIUM.
9. Oversized/unfocused — giant file/table testing many unrelated things; note for split. LOW/MEDIUM. (Eyeball internal/tui 1341 lines/2 files, internal/bgjobs 482/1, pkg/shell3 2618/18, internal/chat 2800/31.)

What makes a test GOOD (keep — the quality bar): behavior/contract focused; clear arrange-act-assert; tests the public surface or a documented invariant with a "why" (e.g. the pass-2 TestRunTurn_MidLoopCtxCancel_PairsAllToolCalls pins the OpenAI tool_call/result pairing invariant); deterministic; fakes only at real seams (fakellm, the telegram fakeClient); fails for exactly one reason.

Also flag SEPARATELY (as ADDITIONS, not cuts) any critical code path with NO test — "same quality as code surface" cuts both ways. Keep this a short, high-confidence list; don't pad.

METHODOLOGY (same rigor as passes 1–2):
1. Baseline: run `go build ./...`, `go vet ./...`, `go test ./...`, and record `ls ~/.shell3/projects | wc -l` (expect 344).
2. Dispatch parallel read-only Explore agents by package group (split below). Give each the rubric above; require for every finding: file:line · GOOD/BAD verdict · category (1–9) · severity · concrete action (delete / rewrite-to-assert-X / merge-with-Y) · one-line justification. Each agent MUST read the code under test, not just the test, before calling a test orphaned or tautological.
3. Hand-verify every HIGH verdict yourself before reporting — agents misread line numbers and call live tests dead (passes 1–2 each caught false positives this way). Confirm the target still exists / the path is really uncovered.
4. Produce a deduplicated, severity-ranked report: KEEP-AS-IS (the bar), CUT (orphaned/vacuous), REWRITE (change-detector/misnamed), MERGE (dup), DE-FLAKE (brittle), and a short ADD list (coverage gaps). Present the plan and WAIT for go-ahead before editing.

Suggested agent groups (by test volume):
- A: internal/chat (31 files / 2800)
- B: pkg/shell3 (18 / 2618) + test/ integration (5 / 670)
- C: internal/luacfg (22 / 1677) + internal/agentsetup (2 / 674)
- D: internal/tui (2 / 1341) + internal/patchapp|patchtui|patchmd (12 / 1685)
- E: internal/telegram + web (16 / 851) + cmd/shell3 (2 / 324) + internal/bootstrap (1 / 352) + internal/scaffold (1 / 308)
- F: internal/adapter/openai (5 / 646) + internal/bgjobs (1 / 482) + internal/edittool (2 / 495) + internal/store (3 / 270) + internal/llm{,/fakellm} + ref/sink/applog/modelproxy/paths/cron (long tail)

INVARIANTS: build/vet/test stay green after every edit; project count stays 344 (no test writes real $HOME); don't weaken coverage of a real contract to silence a "bad test" complaint — rewrite it to assert the contract instead of deleting blindly.
