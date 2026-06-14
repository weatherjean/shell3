# Move cron config into `shell3.telegram{}`

**Date:** 2026-06-14

## Problem

`shell3.cron{}` is a top-level config call, but the scheduler it feeds is
consumed only by the Telegram host (`cmd/shell3/telegram.go`). A top-level call
implies cron works in any front-end; it does not. The base (non-Telegram)
scaffold even ships a commented cron sample that could never fire on its own.

## Goal

Nest cron under the Telegram block so the coupling is honest and discoverable:

```lua
shell3.telegram({
  token = ..., chat_id = ..., agent = "code", workdir = ...,
  dashboard = { ... },
  cron = {
    { name="daily", schedule="@daily", agent="explorer", prompt="..." },
  },
})
```

- **Flat list**: `cron` is directly the list of job tables (no `jobs=` wrapper).
- **Clean break**: the top-level `shell3.cron` global is removed; calling it
  raises `attempt to call a nil value`.

## Code changes

1. **`internal/luacfg/register.go`**
   - Remove `L.SetField(tbl, "cron", ...)` and the `luaCron` function.
   - Add `cron` to `telegramKeys`.
   - In `luaTelegram`, if `opts.cron` is a table, iterate it as a job list using
     the existing per-job parsing (the `cronJobKeys` check, name defaulting,
     `notify` default-true) and append to `c.cron`.
2. **`internal/luacfg/luacfg.go`** — unchanged. The `cron []CronJob` field,
   `Cron()` accessor, and the finalize-time validation loop stay; only the
   population source moves.
3. **`pkg/shell3`, `internal/agentsetup`** — unchanged; `Cron()` still reads the
   same slice.
4. **Tests** — `internal/luacfg/cron_test.go` and `telegram_test.go` rewritten to
   the nested form; add a test asserting top-level `shell3.cron` errors.

## Doc/scaffold changes

5. `internal/scaffold/defaults/telegram/shell3.lua.tmpl` — move the disarmed cron
   sample inside the `shell3.telegram{}` block as a `cron = { ... }` key.
6. `internal/scaffold/defaults/base/shell3.lua.tmpl` — fold the commented cron
   sample into the commented `shell3.telegram{}` block above it.
7. `internal/scaffold/defaults/base/lib/skills/scheduling-jobs.md` — update to the
   nested form.
8. `CHANGELOG.md` — note the breaking config change.

## Out of scope

`CronJob`, the scheduler, `/run`, hot-reload re-arm, and the dashboard cron
source are unchanged — they consume `Cron()`, whose shape and population timing
(finalize after full file load) are preserved.
