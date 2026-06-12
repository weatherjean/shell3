shell3 is a minimal Unix-composable coding agent (Go). We're doing iterative pre-release passes on the `feat/bash-first` branch. Bar: OPEN SOURCE, no backwards-compat, no two-ways-of-doing-the-same-thing, no dead code, no hackiness — minimalism and good taste. Design: the agent's only verbs are `bash` and `edit_file`; custom tools are declarative bash-command templates (`shell3.tool{command=...}`, no Lua handler); subagents are backgrounded subprocesses; skills are `.md` files; context is host-managed auto-compaction; the only safety surface is `shell3.wrap_bash`. "Lua is king": Lua holds policy, Go provides mechanism.

This is the **CODE-QUALITY & CORRECTNESS pass**. It follows the non-test cleanup (passes 1–2) and the test-quality pass (pass 3). The whole tree (231 Go files, ~33k lines, code + tests) was read in full. The verdict: the codebase is already unusually strong — doc comments explain *why*, tests are behavior-pinning characterization tests, concurrency is handled with explicit discipline (busy-gates, fail-closed `wrap_bash`, lock-snapshot-then-act), and OSS scaffolding is real (CI runs `gofmt` + `go vet` + `go test -race` on Linux **and** macOS + a build job; LICENSE/README/CONTRIBUTING/SECURITY/CHANGELOG/goreleaser all present; `go vet ./...` clean). This handoff closes the small number of genuine gaps that remain to reach "top notch."

## STATE (do not undo, do not re-flag)
- Working tree is dirty on `feat/bash-first`; `go build ./...`, `go vet ./...`, `go test ./...` are GREEN. Three `TestMain` `$HOME` redirects (pkg/shell3, internal/telegram, internal/telegram/web) keep real `~/.shell3/projects` from growing during a test run — keep any Runtime-building tests under that redirect.
- HEAD at authoring: `56f5b0f`. Line numbers below are anchors, not contracts — re-grep before editing.
- The architecture is sound and must NOT be refactored to chase a metric: the rendering-agnostic `internal/chat` core, the `pkg/shell3` public API (`doc.go` + `example_test.go`), the single-source `internal/agentsetup` assembly, the sink/pointer notification design, WAL-gating, atomic writes, and typed-error propagation across the public boundary all stay.

## MISSION
Land the six work items below, in order. P0 is a real data race and ships first. Each item lists exact files, the fix, and a verification gate. The suite must stay GREEN and `ls ~/.shell3/projects | wc -l` must not change across a test run. Every new test earns its place by pinning a real contract (same bar as pass 3); every fix is minimal and matches the surrounding idiom.

---

## P0 — Correctness: the dashboard races the running turn

**Severity: HIGH (data race). Ship first.**

### The bug
The Telegram dashboard reads live session state from `net/http` goroutines **concurrently with a running turn on the same `*shell3.Session`**, with no synchronization. The turn goroutine appends to the message slice while the HTTP goroutine copies it — concurrent slice-header read + append/realloc across goroutines is a data race.

Concrete path (History):
- `internal/telegram/web/server.go:107` `handleHistory` → `s.sess.History()`
- `pkg/shell3/shell3.go:1028` `History()` → `s.sess.Messages()`
- `internal/chat/session.go:22` `Messages()` does `copy(out, s.messages)` (read, no lock)
- raced against `internal/chat/session.go:138` `append()` → `s.messages = append(...)`, called from `internal/chat/turn.go` (`sess.append`) on the turn goroutine.

Same shape for status:
- `internal/telegram/web/server.go:163` `handleStatus` → `s.sess.Snapshot()`
- `pkg/shell3/shell3.go:972` `Snapshot()` reads `s.cfg.*` (Personality.Tools, Params, StatusLine, ModeLabel, …), which `SwitchAgent`/`SetParam`/`Clear`/`Reload`/`RegisterHostTool` mutate between turns on the bot goroutine.

The doc comments on `Snapshot`/`History`/`Prune` correctly say "call only between turns (reads unsynchronized state)." The dashboard is a **second concurrent reader** that violates that contract by construction. The wiring is in `cmd/shell3/telegram.go:100-113` (`go startDashboard(...)` runs the HTTP server while `b.Run(ctx)` runs turns on the same `sess`). The existing tests don't catch it because none drives a dashboard read *during* a turn.

`rt.SubagentList`, `rt.SubagentTranscript`, `rt.PastSessions`, `rt.SessionTurns` are NOT affected (they read disk / `database/sql`, both concurrency-safe). The only racy dashboard endpoints are `/api/history` (messages) and `/api/status` (cfg).

### The fix (two localized locks)

**(1) Guard `chat.Session.messages` with a `sync.RWMutex`.**
In `internal/chat/session.go`, add a `msgMu sync.RWMutex` field to `Session`. Take `msgMu.Lock()` in `append()` and `SetMessages()`; take `msgMu.RLock()` in `Messages()`. Leave the turn loop's direct `sess.messages` reads (`internal/chat/turn.go`, `compactInto`, `saveHistory`, etc.) as-is: they run on the turn goroutine, sequential with `append` on that same goroutine, so they don't self-race; the only cross-goroutine access is `append` (write) vs `Messages` (read), which the RWMutex now covers.
- Note the inbox already has its own `inboxMu` — keep them separate; do not merge.
- `internal/chat/tools.go` `PruneByID` mutates message `Content` in place on caller-provided slices (the `Messages()` copy from `pkg/shell3.Prune`); that copy is host-owned and Prune is between-turns, so no extra locking is needed there — but add a one-line comment on `Messages()` noting the returned slice is a snapshot safe to mutate.

**(2) Guard `pkg/shell3.Session` cfg reads with the existing `s.mu`.**
`Snapshot()` (`pkg/shell3/shell3.go:972`) and any other reader the dashboard reaches must read `s.cfg` under `s.mu`. The cfg *writers* — `ApplyActiveAgent` (via `SwitchAgent`), `SetParam`, `Clear`, `RegisterHostTool`, `applyDelegationContext` — must hold `s.mu` across the mutation (several already call `isBusy()`, which takes `s.mu`, then release before mutating; widen the hold to cover the write). The turn goroutine reads cfg unlocked in `turnConfig()` (`shell3.go:842`, after `Send` releases `s.mu`) — that is reader-vs-reader against the dashboard (safe) and never concurrent with a writer (busy-gate), so it stays unlocked.
- Keep the lock scope tight: copy the fields you need out of `s.cfg` under the lock, release, then build the `Snapshot`/`ToolInfo`/`ParamValue` slices. Don't hold `s.mu` across an LLM `ParamDescriber.ParamSpecs()` call.

### Acceptance gate
- A new `-race` regression test that FAILS before the fix and passes after. Put it in `pkg/shell3` (it can use the unexported `route`/`busy` plumbing) or `internal/telegram/web` (it has the signed-initData harness in `happy_path_test.go`). Shape: start a turn backed by `shell3.NewBlockingLLM()` (it blocks in `Stream` until ctx-cancel — see `pkg/shell3/testseam.go`), then from another goroutine hammer `sess.History()` + `sess.Snapshot()` (or hit `/api/history` + `/api/status`) in a loop while the turn is in flight; cancel and drain at the end. Must be clean under `go test -race`.
- `go test -race ./...` GREEN on Linux + macOS.
- Document the new guarantee: update the `Snapshot`/`History` doc comments to say reads are now safe to call concurrently with a turn (the dashboard relies on it), replacing the "between turns only" caveat for those two.

---

## P1 — Coverage: close the real blind spots

**Severity: MEDIUM. Four targeted additions; no production behavior changes (except the optional clamp guard noted).**

### 1a. openai adapter request-param mapping
`internal/adapter/openai/client.go:230-246` maps `RequestParams` → SDK params (the `xhigh→high` clamp at :233, temperature, `parallel_tool_calls`, `MaxCompletionTokens`). None of it is unit-tested — `internals_test.go` covers only `toMessages`/`toTools`/`bodyTap`. Add `client_params_test.go` using the `httptest` SSE-server pattern already in `stream_race_test.go`: construct a `Client`, `SetParams(...)`, run `Stream`, and assert the captured request body (via `tap.snapshot()` / the request the test server received) carries `reasoning_effort:"high"` when `xhigh` was requested, the temperature, the `parallel_tool_calls` flag, and `max_completion_tokens`. One table-driven test; cover the clamp and the "none" effort skip (`:230-231`).

### 1b. Telegram host wiring (cmd/shell3/telegram.go)
The `SetReloader` closure (`cmd/shell3/telegram.go:119-147`) is real logic — it reloads, re-decorates the session, stops the old cron scheduler, arms a new one, and re-points the dashboard cron source — with zero coverage. Extract the reload-coordination body into a small testable function (e.g. `func reloadAndRearm(rt, b, sched, srv) (*cron.Scheduler, shell3.ReloadResult, error)`) in a `//go:build unix` file, and unit-test it with the existing telegram fakes (`fake_test.go`, `runtime_fake_test.go`) + a fake reloader: assert that a reload with a new cron job arms exactly one scheduler and that `b.RedecorateSession()` re-registers the host tools (the bot's `hasTool("send_media_telegram")` is true again after). Keep `newTelegramCommand` a thin wiring shell over the extracted function.

### 1c. Interactive TUI loop smoke test
`internal/tui/interactive.go` `RunInteractive` and `internal/patchapp/loop.go` (`Run`/`tickerLoop`/`winchLoop`) are untested — `interactive_test.go` even documents that the monolithic setup can't be driven hermetically. Add one PTY-driven smoke test under a build tag (e.g. `//go:build unix && ptytest`, run in CI as a separate opt-in step) using `github.com/creack/pty`: allocate a pty, run `tui.RunInteractive` against a fakellm-backed `Spec` (inject via a test seam), write a prompt + Enter, assert the streamed reply lands in the pty output, then send ctrl-C ctrl-C to quit cleanly. The goal is a single end-to-end "the loop wires up, streams, and exits" guard — not exhaustive key coverage (that lives in `patchapp` already).
- If the test seam needed to inject a fakellm into `RunInteractive` is too invasive, prefer the cheaper alternative: a `tui`-package test that calls the extracted helpers in sequence against a `fakeApp`, which already exists for the render sink — but the PTY test is the higher-value one if the seam is clean.

### 1d. Fuzz the tricky pure functions
Three pure functions are exactly where a fuzzer earns its keep — non-trivial string algorithms with a history of edge-case bugs:
- `FuzzReplace` over `internal/edittool/replace.go` `Replace` (the 9-replacer cascade). Invariant to assert: never panics; when it returns nil error, the result is `oldString`-free at the replaced site OR equals a valid application; round-trip sanity on the exact-match path.
- `FuzzApplyInline` over `internal/patchmd/patchmd.go` `applyInline` (the code-span/link tokenizer that already had an ANSI-leak bug — see `TestInline_CodeAndLink_NoLeak`). Invariant: `stripANSI(applyInline(s))` never contains raw SGR body bytes (`38;2;`, `[0m`, …) and never panics; idempotent on plain text.
- `FuzzParseDotEnvValue` over `internal/luacfg/dotenv.go` `parseDotEnvValue`/`stripInlineComment`. Invariant: never panics; a value with no unquoted `#` and no surrounding quote pair returns unchanged.

CI doesn't need to run long fuzz campaigns — `go test -run=Fuzz -fuzztime=10s` per target in a non-blocking job, plus committing any crashers found as seed corpus, is the right bar.

### Acceptance gate
- New tests GREEN under `-race`. Fuzz targets run clean for ≥30s locally before commit; commit seed corpus for any crash discovered.

---

## P2 — Tooling: deeper lint + coverage visibility

**Severity: MEDIUM. Pure infra; no Go behavior change.**

### 2a. Add golangci-lint
CI runs `gofmt` + `go vet` (`.github/workflows/ci.yml`) — good but shallow. The 8 `//nolint` directives already in the tree (patchapp, openai, ref) imply `golangci-lint` was anticipated. Commit a `.golangci.yml` enabling at least: `staticcheck`, `govet`, `errcheck`, `ineffassign`, `unused`, `unparam`, `gocritic`, `misspell`, `bodyclose`. Add a `golangci-lint run` step to the `lint` job (use the `golangci/golangci-lint-action`). Update the Makefile `lint:` target to invoke it too (after the existing gofmt/vet). Fix or `//nolint`-with-reason whatever it surfaces — expect a short list given `vet` is already clean; do NOT mass-suppress.
- Keep the config minimal and opinionated; don't enable the noisy formatters that fight `gofmt`.

### 2b. Coverage in CI
Add `-coverprofile=cover.out -covermode=atomic` to the test step and upload/print the summary (`go tool cover -func=cover.out | tail -1`). A badge or a soft floor is optional, but given the test culture, making the number visible is a top-notch signal. Do NOT gate the build on a hard coverage % initially — surface it, then ratchet.

### Acceptance gate
- `make lint` runs gofmt + vet + golangci-lint and is GREEN. CI lint job updated. Coverage prints in the test job.

---

## P3 — Robustness & docs (lower priority, do after P0–P2)

**Severity: LOW. Defense-in-depth + honesty in docs.**

### 3a. Hard-enforce the subagent depth limit
Depth limit 1 is currently *prompt-enforced*: `pkg/shell3/delegation.go` templates `--no-subagents` into the spawn command, and `cmd/shell3/run.go` maps that flag to `Spec.NoSubagents` → `SessionOpts.DisableSubagents`, which suppresses the delegation context (`pkg/shell3/delegation.go:37`). But nothing forces a model-authored `bash_bg` command to include `--no-subagents`; a non-compliant child could delegate (depth > 1). For an "unsafe by default" tool this is consistent, but cheap defense-in-depth is worth it: have the parent export an env var (e.g. `SHELL3_NO_SUBAGENTS=1`) into the spawned child's environment, and have `pkg/shell3.Session.applyDelegationContext` treat that env var as equivalent to `opts.DisableSubagents` — so a child started under it can never inject a delegation context regardless of its args. Document it as a hard backstop, not the primary mechanism.
- Add a test: a session whose env has `SHELL3_NO_SUBAGENTS=1` gets no `## Delegation` section even with subagents configured (mirror `TestApplyDelegationContext_SuppressedNoSubagents` in `pkg/shell3/delegation_test.go`).

### 3b. Document custom-tool secret exposure
`internal/luacfg/customtool.go` `ResolveCustomCall` exports declared secrets into the child process environment (`internal/chat/handler_bash.go` `runBashCapture` / `internal/bgjobs`). On Linux, same-user processes can read another process's env via `/proc/<pid>/environ`. This is acceptable for a local single-user tool and is the natural cost of the bash-template design — but it should be stated. Add a short note to `SECURITY.md` (and the `shell3.tool{secrets=...}` section of the cookbook) so users with multi-user hosts understand the boundary.

### 3c. Make "Unix-only" explicit in the README
Large parts of the tree are `//go:build unix` (bgjobs, cron, telegram, the TUI loop, modelproxy, parts of patchtui). The library/CLI is a Unix tool by design. Add one line to `README.md` (requirements/install section) stating Linux + macOS support and that Windows is not a target, so the build constraints aren't a surprise to contributors.

### Acceptance gate
- 3a test GREEN; SECURITY.md + README updated; no behavior regression.

---

## ORDERING & METHODOLOGY
1. **Baseline.** `go build ./...`, `go vet ./...`, `go test -race ./...` GREEN; record `ls ~/.shell3/projects | wc -l`.
2. **P0 first**, on its own commit: the two locks + the `-race` regression test. This is the only item that changes runtime behavior under concurrency — verify with `go test -race ./pkg/shell3/... ./internal/telegram/...` and the new regression test specifically.
3. **P1** as separate commits per sub-item (1a–1d) so each addition is reviewable in isolation.
4. **P2** as one infra commit (golangci config + CI + Makefile + any fixes it forces).
5. **P3** last, as one commit (or three small ones).
6. After every commit: `go build ./...`, `go vet ./...`, `go test -race ./...` GREEN, and the project-dir count unchanged.

## DEFINITION OF DONE
- The dashboard can be polled during a live turn with zero data races (`-race` regression test proves it).
- openai param mapping, telegram reload wiring, and the interactive loop each have at least one contract-pinning test; the three tricky pure functions have fuzz targets with committed seed corpus.
- `make lint` runs golangci-lint and is clean; CI surfaces coverage.
- Subagent depth limit has a hard env backstop; SECURITY.md documents secret-in-env; README states Unix-only.
- Suite GREEN on Linux + macOS, project-dir count stable, no dead code or second-way-of-doing-things introduced.

## WHAT NOT TO DO
- Don't add a mutex to every `chat.Session.messages` access "for safety" — the turn goroutine is single-threaded by contract; only the cross-goroutine append-vs-read pair needs guarding (see P0). Over-locking would invite a lock-order deadlock with `inboxMu`/`s.mu` and slow the turn loop.
- Don't refactor the architecture, rename the public API, or "simplify" the sink/delegation/compaction designs — they are deliberate and load-bearing.
- Don't mass-`//nolint` golangci findings; fix them or annotate each with a one-line reason.
- Don't gate CI on a hard coverage percentage on the first pass — surface it, then ratchet later.
