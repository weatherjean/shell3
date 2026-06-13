# Remove Telegram, cron via `--cron` flag — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove every trace of Telegram from shell3, keeping the core guts (Dispatch, cron scheduler, transport, media), and replace telegram's role as the cron host with a `--cron` flag on the interactive TUI.

**Architecture:** Two phases. Phase A is an *ordered* deletion — each task removes consumers before producers so `make build` stays green and is committed at every step. Phase B adds the only new code: a `Spec.Cron` flag, a `Session.CronJobs()` accessor, `Notice` rendering in the TUI wake loop, scheduler arming in `RunInteractive`, and a `--cron` root flag.

**Tech Stack:** Go, cobra (CLI), robfig/cron (via `internal/cron`), Lua config (`internal/luacfg`).

**Branch:** `remove-telegram` (already created).

---

## Reference: the telegram footprint (verification grep)

The completeness check used throughout Phase A:

```bash
grep -rli telegram --include="*.go" --include="*.lua" --include="*.tmpl" . | grep -v docs/
```

Files touched (from initial survey):
- **Whole-package deletes:** `internal/telegram/` (incl. `web/`), `internal/scaffold/defaults/telegram/`
- **cmd/shell3:** `telegram.go`, `telegram_reload_test.go`, `main.go`, `boot.go`, `boot_test.go`, `dbpath.go`, `dbpath_test.go`
- **internal/luacfg:** `luacfg.go`, `register.go`, `telegram_test.go`
- **internal/agentsetup:** `agentsetup.go`, `agentsetup_test.go`
- **pkg/shell3:** `runtime.go`, `reload.go`, `shell3.go`, `telegram_config_test.go`, `dashboard_race_test.go`, `reload_test.go`, `example_test.go`, `runtime` accessor tests
- **internal/scaffold:** `scaffold.go`, `scaffold_test.go`, `defaults/base/lib/skills/browser.md`
- **internal/chat:** `media.go`, `session.go` (comments only), `media_bytes_test.go`
- **docs:** `docs/cookbook/lib/browser.md`, `CHANGELOG.md`

---

## Phase A — Delete Telegram (ordered, build stays green)

### Task A1: Remove the telegram command + boot path (top-level consumers)

This removes the only importers of `internal/telegram`, `internal/telegram/web`, and the telegram half of `internal/cron`, leaving those packages unused-but-valid (still compiles).

**Files:**
- Delete: `cmd/shell3/telegram.go`, `cmd/shell3/telegram_reload_test.go`
- Modify: `cmd/shell3/main.go` (remove `root.AddCommand(newTelegramCommand())` at line 53)
- Modify: `cmd/shell3/boot.go` + `cmd/shell3/boot_test.go` (remove the `--telegram` boot path / telegram-dir creation)
- Modify: `cmd/shell3/dbpath.go` + `cmd/shell3/dbpath_test.go` (remove the single telegram reference)

- [ ] **Step 1: Delete the command files**

```bash
git rm cmd/shell3/telegram.go cmd/shell3/telegram_reload_test.go
```

- [ ] **Step 2: Remove the registration in main.go**

Delete line 53 `root.AddCommand(newTelegramCommand())` in `cmd/shell3/main.go`.

- [ ] **Step 3: Strip telegram from boot.go and dbpath.go**

Read `cmd/shell3/boot.go`, `cmd/shell3/boot_test.go`, `cmd/shell3/dbpath.go`, `cmd/shell3/dbpath_test.go`. Remove the `--telegram` flag, the telegram-directory bootstrap branch, and any telegram path constants. Keep the non-telegram boot behavior (global + project setup) intact. Update the affected tests to drop telegram assertions (do not delete unrelated test cases).

- [ ] **Step 4: Build + vet + test**

Run: `make build && go vet ./... && go test ./cmd/... ./pkg/...`
Expected: PASS. `internal/telegram` and `internal/cron` are now unused by `cmd` but still compile.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "remove(telegram): delete telegram command + boot path"
```

### Task A2: Delete the telegram packages + scaffold template

**Files:**
- Delete: `internal/telegram/` (entire directory, including `web/`)
- Delete: `internal/scaffold/defaults/telegram/` (entire directory)
- Modify: `internal/scaffold/scaffold.go` + `internal/scaffold/scaffold_test.go` (remove telegram template embedding/handling)
- Modify: `internal/scaffold/defaults/base/lib/skills/browser.md` (drop telegram mention)

- [ ] **Step 1: Delete the directories**

```bash
git rm -r internal/telegram internal/scaffold/defaults/telegram
```

- [ ] **Step 2: Strip telegram from scaffold.go**

Read `internal/scaffold/scaffold.go`. Remove the `//go:embed` of the telegram template, any `telegram`-named template var, and the branch that writes the telegram `shell3.lua` during `boot --telegram`. Read `internal/scaffold/scaffold_test.go` and remove telegram assertions/cases. Edit `internal/scaffold/defaults/base/lib/skills/browser.md` to remove the telegram reference (reword to be transport-neutral).

- [ ] **Step 3: Build + vet + test**

Run: `make build && go vet ./... && go test ./internal/scaffold/... ./cmd/...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "remove(telegram): delete telegram + scaffold telegram template"
```

### Task A3: Remove telegram config from pkg/shell3

`pkg/shell3` builds `TelegramConfig`/`DashboardConfig` from agentsetup parts and exposes `Runtime.Telegram()`. Nothing in `cmd` references these now, so they can be removed self-contained.

**Files:**
- Modify: `pkg/shell3/runtime.go` (remove `TelegramConfig` type ~L20-27, `DashboardConfig` type ~L29-35, the `telegram` field ~L124, its population in the runtime constructor ~L188-232, and `Runtime.Telegram()` ~L232-233)
- Modify: `pkg/shell3/reload.go` (remove any telegram field copy in the reload path)
- Delete: `pkg/shell3/telegram_config_test.go`, `pkg/shell3/dashboard_race_test.go`
- Modify: `pkg/shell3/reload_test.go`, `pkg/shell3/example_test.go` (drop telegram references)

- [ ] **Step 1: Delete the dedicated telegram tests**

```bash
git rm pkg/shell3/telegram_config_test.go pkg/shell3/dashboard_race_test.go
```

- [ ] **Step 2: Remove telegram types + accessor from runtime.go and reload.go**

Read `pkg/shell3/runtime.go` and `pkg/shell3/reload.go`. Delete the `TelegramConfig` and `DashboardConfig` type declarations, the `telegram TelegramConfig` struct field, the code that reads `parts.Telegram()` to populate it, and the `Runtime.Telegram()` method. In `reload.go` remove any telegram field assignment. Keep `CronJob` and `Runtime.Cron()` — they stay.

- [ ] **Step 3: Drop telegram references in remaining pkg tests**

Read `pkg/shell3/reload_test.go` and `pkg/shell3/example_test.go`; remove telegram assertions/usages while leaving the rest of each test intact.

- [ ] **Step 4: Build + vet + test**

Run: `make build && go vet ./... && go test ./pkg/...`
Expected: PASS. `agentsetup.Telegram()` is now unused but still compiles.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "remove(telegram): drop TelegramConfig/DashboardConfig from pkg/shell3"
```

### Task A4: Remove telegram from agentsetup + luacfg (producers, last)

**Files:**
- Modify: `internal/agentsetup/agentsetup.go` (remove `Parts.Telegram()` ~L81-82 and `ResolveTelegramConfigPath` ~L483-505)
- Modify: `internal/agentsetup/agentsetup_test.go` (drop telegram tests)
- Modify: `internal/luacfg/luacfg.go` (remove `TelegramConfig` type, `telegram` field, `Telegram()` accessor, and any telegram validation; keep all cron code)
- Modify: `internal/luacfg/register.go` (remove `luaTelegram` registration + `telegramKeys`; keep `luaCron`)
- Delete: `internal/luacfg/telegram_test.go`

- [ ] **Step 1: Delete the luacfg telegram test**

```bash
git rm internal/luacfg/telegram_test.go
```

- [ ] **Step 2: Remove telegram from agentsetup**

Read `internal/agentsetup/agentsetup.go`. Delete the `Telegram()` accessor and `ResolveTelegramConfigPath` (and its helper usages if now unused — check `fileExists` is still used elsewhere before removing). Read `agentsetup_test.go`; remove the `ResolveTelegramConfigPath` tests and any `Telegram()` assertions.

- [ ] **Step 3: Remove telegram from luacfg**

Read `internal/luacfg/luacfg.go` and `internal/luacfg/register.go`. Remove the `TelegramConfig` type, the `telegram` field on `LoadedConfig`, the `Telegram()` accessor, the `L.SetField(tbl, "telegram", ...)` registration, the `luaTelegram` function, and `telegramKeys`/`telegramDashboardKeys` maps. Leave every `cron` symbol (`luaCron`, `cronKeys`, `cronJobKeys`, `CronJob`, `Cron()`) untouched.

- [ ] **Step 4: Build + vet + full test**

Run: `make build && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 5: Verify no telegram references remain**

Run: `grep -rli telegram --include="*.go" --include="*.lua" --include="*.tmpl" . | grep -v docs/`
Expected: empty output (no matches). If anything prints, remove it and re-run Step 4.

- [ ] **Step 6: Commit**

```bash
git add -A && git commit -m "remove(telegram): drop telegram config from agentsetup + luacfg"
```

### Task A5: Comment + doc cleanups

**Files:**
- Modify: `internal/chat/media.go:35`, `internal/chat/session.go:18`, `internal/chat/media_bytes_test.go` (telegram-only comments → transport-neutral wording)
- Modify: `docs/cookbook/lib/browser.md`, `CHANGELOG.md`

- [ ] **Step 1: Reword the comments**

In `internal/chat/media.go` line ~35 ("e.g. a Telegram photo download") and `internal/chat/session.go` line ~18 ("the Telegram dashboard polling History()"), reword to drop the telegram-specific example (e.g. "a host that holds the bytes directly" / "a reader polling History()"). Check `internal/chat/media_bytes_test.go` for a telegram comment and reword. These are comments only — no behavior change.

- [ ] **Step 2: Update docs**

Edit `docs/cookbook/lib/browser.md` to remove the telegram reference. Add a `CHANGELOG.md` entry under an Unreleased/next section noting telegram removal and the new `--cron` flag (write the cron line now; it's delivered in Phase B).

- [ ] **Step 3: Build + full test + gofmt**

Run: `make build && go test ./... && gofmt -l .`
Expected: tests PASS, `gofmt -l .` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add -A && git commit -m "remove(telegram): reword telegram-only comments + docs"
```

---

## Phase B — Add the `--cron` flag (TDD)

### Task B1: `Spec.Cron` field + `Session.CronJobs()` accessor

**Files:**
- Modify: `pkg/shell3/shell3.go` (add `Cron bool` to `Spec`, thread it through `Start` if needed — it is only read by the host, so threading may be unnecessary; the field is the carrier)
- Create: accessor in `pkg/shell3/runtime.go` or `pkg/shell3/shell3.go`: `func (s *Session) CronJobs() []CronJob`
- Test: `pkg/shell3/cron_accessor_test.go`

- [ ] **Step 1: Write the failing test**

```go
package shell3

import "testing"

func TestSessionCronJobsNilWithoutRuntime(t *testing.T) {
	var s Session
	if jobs := s.CronJobs(); jobs != nil {
		t.Fatalf("expected nil cron jobs for a session with no runtime, got %v", jobs)
	}
}
```

- [ ] **Step 2: Run it, verify it fails to compile**

Run: `go test ./pkg/shell3/ -run TestSessionCronJobs -v`
Expected: FAIL — `s.CronJobs undefined`.

- [ ] **Step 3: Implement the accessor + Spec field**

In `pkg/shell3/shell3.go`, add to `Spec`:

```go
	// Cron, when true, tells an interactive host to arm the cron scheduler from
	// the loaded shell3.cron{} jobs for the lifetime of the session. Off by
	// default: the TUI/once/subagents never tick cron unless explicitly enabled.
	Cron bool
```

Add the accessor (in `runtime.go`, near `Runtime.Cron()`):

```go
// CronJobs returns the shell3.cron{} jobs loaded for this session's runtime
// (nil if the session has no runtime). Hosts use it to arm a cron.Scheduler
// with the Session itself as the Dispatcher.
func (s *Session) CronJobs() []CronJob {
	if s.runtime == nil {
		return nil
	}
	return s.runtime.Cron()
}
```

- [ ] **Step 4: Run the test, verify it passes**

Run: `go test ./pkg/shell3/ -run TestSessionCronJobs -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cron): Spec.Cron flag + Session.CronJobs() accessor"
```

### Task B2: Render `Notice` host events in the TUI wake loop

A fired cron job emits `HostEvent{Kind: Notice, Text: "[cron:name] …"}` on the same bus the wake loop reads. Today `consumeWakesWith` drops non-`Wake` events. Render `Notice` as a dim line, starting no turn.

**Files:**
- Modify: `internal/tui/interactive.go` (`consumeWakesWith`, ~L322-347)
- Test: `internal/tui/interactive_test.go` (or the existing wake test file — find where `consumeWakes` is tested)

- [ ] **Step 1: Write the failing test**

Find the existing wake-loop test (grep `consumeWakes` under `internal/tui`). Add a test that pushes a `Notice` event onto the fake session's wake bus and asserts the app printed its text and started no turn. Sketch:

```go
func TestConsumeWakesRendersNotice(t *testing.T) {
	// fake session whose WakeEvents() yields one Notice for this session, then closes.
	// fake app records PrintLine calls.
	// run consumeWakes(ctx, sess, app, &wg); wg.Wait()
	// assert app recorded a line containing "[cron:nightly] done" and launched no turn.
}
```

Match the construction style of the existing wake test (same fakes).

- [ ] **Step 2: Run it, verify it fails**

Run: `go test ./internal/tui/ -run TestConsumeWakesRendersNotice -v`
Expected: FAIL — Notice text not printed (currently dropped).

- [ ] **Step 3: Implement Notice handling**

In `consumeWakesWith`, replace the filter that `continue`s on non-Wake with a switch that also handles `Notice`:

```go
			if ev.Session != name {
				continue
			}
			switch ev.Kind {
			case shell3.Notice:
				// A host/cron dispatch result: show it verbatim (dim), start no turn.
				app.PrintLine(patchtui.Dim + ev.Text + patchtui.Reset)
				continue
			case shell3.Wake:
				// fall through to the wake/turn logic below
			default:
				continue
			}
```

Keep the existing Wake-driven `runTurn(...)` block after this for the `Wake` case.

- [ ] **Step 4: Run the test, verify it passes**

Run: `go test ./internal/tui/ -run TestConsumeWakesRendersNotice -v && go test ./internal/tui/...`
Expected: PASS (new test + all existing TUI tests).

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cron): render Notice host events in the TUI wake loop"
```

### Task B3: Arm cron in `RunInteractive` + wire the `--cron` root flag

**Files:**
- Modify: `internal/tui/interactive.go` (`RunInteractive`: arm scheduler when `spec.Cron`)
- Modify: `cmd/shell3/main.go` (add `--cron` flag on the root command → `spec.Cron`)

- [ ] **Step 1: Arm the scheduler in RunInteractive**

In `internal/tui/interactive.go`, add the import `"github.com/weatherjean/shell3/internal/cron"`. After `sess, err := shell3.Start(ctx, spec)` succeeds and before the main loop, insert:

```go
	// When started with --cron, arm the scheduler from the loaded shell3.cron{}
	// jobs with this session as the Dispatcher. A fired job execs a subagent and
	// reports its result back as a Notice (rendered by consumeWakesWith). A bad
	// schedule is surfaced as a notice and disables cron rather than killing the
	// interactive session.
	if spec.Cron {
		if jobs := sess.CronJobs(); len(jobs) > 0 {
			sched, cerr := cron.New(sess, jobs)
			if cerr != nil {
				app.PrintLine(patchtui.Red + "[cron disabled: " + cerr.Error() + "]" + patchtui.Reset)
			} else {
				sched.Start()
				defer sched.Stop()
				app.PrintLine(patchtui.Dim + fmt.Sprintf("[cron: %d job(s) scheduled]", len(jobs)) + patchtui.Reset)
			}
		}
	}
```

Place this AFTER `app = patchapp.New(...)` (so `app.PrintLine` is valid) and after `defer sess.Close()` so `sched.Stop()` (LIFO) runs before the session closes — mirroring telegram.go's `defer sched.Stop()` / `defer rt.Close()` ordering. `*shell3.Session` satisfies `cron.Dispatcher` via its `Dispatch` method.

- [ ] **Step 2: Add the `--cron` flag in main.go**

In `cmd/shell3/main.go`, alongside `rootResume`/`rootConfigPath`/`rootAgent`, add `var rootCron bool`, register:

```go
	root.Flags().BoolVar(&rootCron, "cron", false, "Arm the scheduler from shell3.cron{} jobs for this interactive session.")
```

and set `Cron: rootCron` in the `shell3.Spec` literal inside `root.RunE`.

- [ ] **Step 3: Build + vet + full test**

Run: `make build && go vet ./... && go test ./...`
Expected: PASS.

- [ ] **Step 4: Manual smoke check**

Run: `go run ./cmd/shell3 --help | grep -A1 cron` → shows the `--cron` flag.
(Optional, if a cron-bearing config is available: `shell3 --cron` prints `[cron: N job(s) scheduled]`; plain `shell3` does not.)

- [ ] **Step 5: Commit**

```bash
git add -A && git commit -m "feat(cron): --cron flag arms the scheduler on the interactive TUI"
```

---

## Final Verification (do this yourself, not in a subagent)

- [ ] `make build` — PASS
- [ ] `go vet ./...` — clean
- [ ] `go test ./...` — PASS
- [ ] `gofmt -l .` — prints nothing
- [ ] `grep -rli telegram --include="*.go" --include="*.lua" --include="*.tmpl" . | grep -v docs/` — empty
- [ ] `go run ./cmd/shell3 --help` lists `--cron` and no `telegram` subcommand
- [ ] Review the full diff: `git diff main...remove-telegram --stat`

---

## Self-review notes

- **Spec coverage:** deletes (telegram pkg/web/scaffold/cmd/config/comments) → A1–A5; keep guts (cron, Dispatch, transport, media) → untouched; new `--cron` on/off → B1–B3; report path (Notice) → B2; verification → Final.
- **Type consistency:** `Session.CronJobs() []CronJob` (B1) is consumed in B3; `cron.New(disp Dispatcher, jobs []shell3.CronJob)` matches `(sess, jobs)`; `*Session` satisfies `cron.Dispatcher` via existing `Dispatch`. `HostEventKind` constants `Notice`/`Wake` used in B2 exist in `pkg/shell3/runtime.go`.
- **Ordering guarantee:** A1 removes consumers of `internal/telegram`/`web`/`cron` first; A2 deletes the now-orphaned packages; A3 then A4 remove producers (pkg→agentsetup→luacfg) so each task ends compilable.
