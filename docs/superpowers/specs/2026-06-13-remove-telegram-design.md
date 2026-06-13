# Remove Telegram, cron via `--cron` flag

**Date:** 2026-06-13
**Branch:** `remove-telegram`

## Goal

Completely remove Telegram from shell3 while keeping the core "guts" we'll wire a
web interface onto later. Telegram was the only long-running host that armed the
cron scheduler; we replace that single entrypoint with a `--cron` flag on the
interactive TUI host. After the change the build, vet, tests, and gofmt are all
green.

## Background (current state)

- `internal/telegram/` is a self-contained package (bot, send/reload/status
  tools, render, and a `web/` Mini-App dashboard). Its only importer is
  `cmd/shell3/telegram.go` (+ one test).
- Cron jobs are declared in Lua (`shell3.cron{}`), parsed by `internal/luacfg`
  into `[]CronJob`, exposed via `Runtime.Cron()`. The `internal/cron.Scheduler`
  arms one robfig/cron entry per job and calls `Dispatcher.Dispatch(...)` on tick.
  `internal/cron` does **not** import telegram — but the only place it is armed
  is `cmd/shell3/telegram.go`. So cron already never ticks under the plain TUI.
- `pkg/shell3.Session.Dispatch` is already telegram-free: it execs a `shell3`
  subprocess for the job's agent/prompt/workdir, reads the transcript's final
  text, and surfaces it via `deliverDispatchResult` → `rt.emit(HostEvent{Kind:
  Notice})`. That `Notice` is a generic host event the TUI already renders.
- Every interactive session already starts its socket transport and registers as
  a live parent (`runtime.go` `startTransport`), so the TUI is a valid host for
  subagent reports.

## Design

### What gets DELETED (telegram surface)

- `internal/telegram/` — entire package, including `web/` and its static assets.
- `internal/scaffold/defaults/telegram/` and the `shell3.telegram{}` block in the
  scaffold `shell3.lua.tmpl`; remove telegram handling in `scaffold.go`.
- `cmd/shell3/telegram.go` and `cmd/shell3/telegram_reload_test.go`; remove its
  command registration in `main.go`.
- `internal/luacfg`: `TelegramConfig` type, `luaTelegram` registration in
  `register.go`, related validation in `luacfg.go`, and `telegram_test.go`.
- `pkg/shell3`: `TelegramConfig` + `DashboardConfig` types, `Runtime.Telegram()`,
  `telegram_config_test.go`, `dashboard_race_test.go`, and telegram fields in
  `reload.go`/`runtime.go`.
- `internal/agentsetup`: `Parts.Telegram()` accessor, `ResolveTelegramConfigPath`,
  and telegram references in `agentsetup_test.go`.
- `cmd/shell3/boot.go`: the `--telegram` boot path and telegram-dir resolver;
  `cmd/shell3/dbpath.go` telegram reference.
- Comment-only cleanups: `internal/chat/media.go`, `internal/chat/session.go`,
  the `browser.md` skill (`internal/scaffold/defaults/base/lib/skills/browser.md`
  and `docs/cookbook/lib/browser.md`), `CHANGELOG.md`.

### What STAYS (the guts — untouched behavior)

- `internal/cron` (scheduler) — kept as-is.
- `pkg/shell3.Session.Dispatch` / `DispatchOpts` / `deliverDispatchResult`.
- The socket / inbox / revive subagent transport, media support, `internal/bgjobs`,
  `internal/store`, history/FTS, and all CLIs (`fts`/`list-projects`/
  `list-sessions`/`jobs`).

### What gets ADDED (the only new code)

A `--cron` boolean flag on `shell3 run`:

- **Off (default):** nothing armed. The TUI, `once`, and subagents never tick
  cron. This preserves "cron doesn't run when the TUI runs" by default.
- **On:** after the interactive `Session` + `Runtime` are built (its transport is
  already live), arm the scheduler and start it; stop it on shutdown.
  - `*shell3.Session` already satisfies `cron.Dispatcher`
    (`Dispatch(agent, prompt string, opts DispatchOpts) (string, error)`).
  - Jobs come from `rt.Cron()` (Lua `shell3.cron{}`). No SQLite, no CRUD, no
    migration.
  - `Runtime.Close` already joins in-flight dispatched jobs; the flag path calls
    `scheduler.Stop()` before/around teardown.
- Isolate the arming behind a small helper (e.g. `armCron(session, rt)
  (*cron.Scheduler, error)`) so the future web host calls one function instead of
  duplicating the wiring.
- A fired job's result surfaces through the existing host `Notice` event, rendered
  inline in the TUI — no new reporting path.

## Out of scope (YAGNI)

- No `shell3 cron run` subcommand — the `--cron` flag is the on/off switch.
- No manual `/cron run <name>` TUI command in v1 (the telegram host's `/run` is
  dropped with telegram).
- No move of cron jobs out of Lua; no SQLite cron table.
- No new web interface — only the seam (`armCron`) that one will reuse.

## Verification

All must be green after the change:

- `make build` — won't compile until every telegram reference is gone (a
  completeness check in itself).
- `go vet ./...`
- `go test ./...` — confirms the kept guts (cron scheduler, dispatch, transport,
  media, store) still pass, and the new `--cron` wiring works.
- `gofmt -l` reports no files.

Plus a manual smoke check: `shell3 run` (no cron armed) and `shell3 run --cron`
against a config with a `shell3.cron{}` job arms the scheduler and surfaces a
Notice on fire.
