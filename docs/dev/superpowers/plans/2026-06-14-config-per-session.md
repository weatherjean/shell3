# Config-per-session + FTS hygiene + retire the `status` affordance — Implementation Plan

> **For agentic workers:** Use superpowers:subagent-driven-development. TDD per task (failing test → watch fail → implement → watch pass). Gate every task on `make lint` (0 issues) and `make test` (race+coverage, green). Commit per task; end each commit body with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.

**Branch:** continue on `feat/bg-jobs-sqlite` (this is the CLI/prompt-anchoring follow-on).

**Goal (4 threads, decided with the user):**
1. **Remove `status` from the agent's prompts.** `status` is a Telegram-only host tool; the TUI agent never has it, yet `browser.md`/`self-evolve.md` tell the agent to "call the `status` tool" to find its config dir. Repoint them at the `## Environment` block.
2. **Exclude tool turns from FTS search.** `shell3 fts` returns `role='tool'` rows (bash echoes) that swamp real hits. Filter them out of *search* while keeping them in the table so `read-session` replay stays complete.
3. **(A) Inject the config path into `## Environment`** so the live agent (any front-end) can resolve its own config dir without a tool. This is also what makes (1) safe for Telegram, which shares those skills.
4. **(B) Record which config produced each session in the DB**, and use it. Many configs share one canonical DB; today `sessions` has no config column and `shell3 run --resume <id>` resolves config from the *invocation*, not the session — so resuming under a different cwd/default silently runs the wrong agent/model/tools. Same bug in `spawnRevive` (uses the reviver's config, not the dormant parent's). Fix: store `config_path` at `StartSession`, then resume + revive prefer it (explicit `--config` still wins), and surface it in `list-sessions`.

**Why A and B are one value, two consumers:** both are `rt.ConfigPath()` (the host's resolved absolute config path). Record it once at session start; read it in the prompt (A) and from the DB (B).

**No standalone `shell3 config-path` CLI** (decided): redundant for self-ID (a bash subprocess re-resolves config independently of the host, so it's unreliable as "what am I running under"); the host already has the authoritative value.

**Tech:** Go, modernc.org/sqlite (WAL), cobra. No backward compat (dev branch; column added in place, old rows get `''` and fall back to current resolution).

---

## Task 1: FTS search excludes tool turns

**Files:** `internal/store/store.go` (`HistorySearchExpr`), `internal/store/*_test.go`.

- [ ] **Step 1 (failing test):** in the store test package, seed one session with a `role='user'` turn and a `role='tool'` turn that BOTH contain a distinctive term (e.g. "zebraterm") via `AppendHistory`. Call `HistorySearchExpr("zebraterm", "", 0, 0)` and assert the result contains the user hit and NOT the tool hit. Run → FAIL (tool row currently returned).
- [ ] **Step 2:** add `AND role <> 'tool'` to the `HistorySearchExpr` query's WHERE clause (alongside `history MATCH ?` and the project filter). `role` is an UNINDEXED FTS column, so this post-filters cleanly. Do not touch the `earlier`/chunk subquery. Run → PASS. Confirm `read-session` (which uses `SessionTurns`, not this query) is unaffected — it must still return tool turns.
- [ ] **Step 3:** `make lint` + `make test`. Commit: `feat(store): exclude tool turns from FTS search (keep them for replay)`

---

## Task 2: record `config_path` per session

**Files:** `internal/store/store.go` (migrate + `StartSession` + new `SessionConfigPath`), `internal/store/sessions.go` (`StartSessionWithParent`), `internal/store/*_test.go`; callers `pkg/shell3/shell3.go:411`, `internal/chat/tools.go:130`; `internal/chat/chat.go` (Config gains `ConfigPath`); wherever the runtime assembles `chat.Config` (populate `ConfigPath` from the resolved config path).

- [ ] **Step 1:** add column to the `sessions` CREATE in `migrate()` (edit in place): `config_path TEXT NOT NULL DEFAULT ''`.
- [ ] **Step 2 (failing test):** test that `StartSession(uuid, workdir, "/x/.shell3/shell3.lua")` then a new `SessionConfigPath(id)` returns `/x/.shell3/shell3.lua`; and that a session started with `""` returns `""`. Run → FAIL.
- [ ] **Step 3:** change `StartSession(projectUUID, workdir string)` → `StartSession(projectUUID, workdir, configPath string)` and `StartSessionWithParent(parent int64, projectUUID, workdir string)` → add `configPath string`; INSERT the column. Add `func (s *Store) SessionConfigPath(id int64) (string, error)` (SELECT config_path FROM sessions WHERE id=?; wrap errors `store: session config path %d`). Run → PASS.
- [ ] **Step 4:** thread the value through callers. Add `ConfigPath string` to `chat.Config` (doc it: the resolved absolute shell3.lua path for this session; "" if unknown). Populate it where the runtime builds `chat.Config` from its resolved config (the runtime knows `rt.ConfigPath()` / the resolved path used to build the config — find that assembly point and set `cfg.ConfigPath`). Update `pkg/shell3/shell3.go:411` `StartSession(cfg.ProjectRef, cfg.WorkDir, cfg.ConfigPath)` and the subagent spawn at `internal/chat/tools.go:130` `StartSessionWithParent(..., cfg.ConfigPath)` (a subagent records the same config it runs under). Keep `chat.Config`'s reload-copy helper (the function near chat.go:144 that lists preserved fields) consistent — add `ConfigPath` to the preserved set if appropriate.
- [ ] **Step 5:** `make lint` + `make test` (the delegation/resume e2e must stay green). Commit: `feat(store): record config_path per session (StartSession + SessionConfigPath)`

---

## Task 3: resume + revive use the session's recorded config

**Files:** `cmd/shell3/run.go` (resume peek), `pkg/shell3/transport.go` (`spawnRevive`), tests.

- [ ] **Step 1 (resume):** in `run.go`, when `f.resume != 0 && f.configPath == ""`, resolve the canonical DB (`canonicalDBPath("")`), open it read-only-ish via `store.Open`, call `SessionConfigPath(f.resume)`, and if non-empty set `spec.ConfigPath` to it before building the spec. Explicit `--config` always overrides (guard on `f.configPath == ""`). Close the store. Wrap errors `run: resolve resume config`. A missing/empty value → leave `spec.ConfigPath` empty (current default resolution). Add a CLI/unit test: seed a session with a known `config_path` at the canonical DB under a temp `$HOME`, build the flags with `--resume <id>` and no `--config`, and assert the resolved `spec.ConfigPath` equals the recorded one (extract via a small testable helper if `RunE` is hard to assert directly — prefer a `resolveResumeConfig(resumeID, flagConfig string) (string, error)` helper that the test calls and `RunE` uses).
- [ ] **Step 2 (revive):** in `spawnRevive`, replace `cfgPath = s.runtime.ConfigPath()` with: read the dormant parent's config via `s.cfg.Store.SessionConfigPath(parentID)`; if non-empty use it, else fall back to `s.runtime.ConfigPath()`. Pass as `--config`. This makes a revived parent run under ITS OWN config, not the reviver's.
- [ ] **Step 3:** `make lint` + `make test`. Commit: `feat(resume): resume + revive prefer the session's recorded config (explicit --config wins)`

---

## Task 4: surface config in `list-sessions`

**Files:** `internal/store/store.go` (`SessionMeta` + `ListSessionsPage` select), `cmd/shell3/sessions.go`, tests.

- [ ] **Step 1:** add `ConfigPath string` to `SessionMeta`; SELECT `config_path` in `ListSessionsPage` and scan it. (Leave the dashboard `ListSessions(limit)` path untouched — it may leave ConfigPath zero, like Status/ParentID.)
- [ ] **Step 2:** append the config path (basename of its dir, or the full path — pick the more useful; full path is fine) to the `list-sessions` output line. Update `sessions_test.go` to tolerate/верify the new column.
- [ ] **Step 3:** `make lint` + `make test`. Commit: `feat(cli): list-sessions shows each session's config_path`

---

## Task 5: inject config into `## Environment`; retire `status` from the shared skills

**Files:** `internal/agentsetup/agentsetup.go` (env section + Parts wiring + test), `internal/scaffold/defaults/base/lib/skills/browser.md`, `internal/scaffold/defaults/base/lib/skills/self-evolve.md`, `internal/scaffold/defaults/base/shell3.lua.tmpl` (self-evolve description), and the LIVE `~/.shell3/lib/skills/browser.md` + `self-evolve.md` (+ the one-line self-evolve description in `~/.shell3/shell3.lua`). Do NOT change the Telegram tmpl's host-tool list (status the tool still exists there for interactive use).

- [ ] **Step 1 (A — inject):** thread the resolved config path into `agentsetup` `Parts` (it's available from the same inputs that already give `p.dbPath`/`p.uuid`), and add an `## Environment` line, e.g.: `- config: `<config_path>` (your shell3.lua; its directory holds lib/ — edit via the self-evolve skill)`. Keep the existing `if p.dbPath == ""` guard semantics; if config path is "" just omit the one line (don't drop the section). Update the agentsetup test for the new line.
- [ ] **Step 2 (retire status):** 
  - `browser.md`: change "call the `status` tool if unsure of the path" → "the config dir is in your `## Environment` section" (so `<config-dir>` is resolvable).
  - `self-evolve.md` (line ~25): "Call the `status` tool. It prints the absolute path of the `shell3.lua` you edit" → "Your `shell3.lua` path is in the `## Environment` section."
  - `base/shell3.lua.tmpl` self-evolve description (line ~17): "reload + status tools" → "reload tool" (drop status).
  - Grep `internal/scaffold/defaults/base/` for any other agent-facing `status` mention and repoint/remove.
- [ ] **Step 3 (live sync):** copy the corrected `browser.md` + `self-evolve.md` into `~/.shell3/lib/skills/`; and make the SAME one-line self-evolve-description edit in `~/.shell3/shell3.lua` (targeted exact-string edit only — do not rewrite the user's customized config). Verify `grep -rn "status" ~/.shell3/lib/skills/ ~/.shell3/shell3.lua` shows no agent-facing "call the status tool" guidance (the Telegram host-tool mention, if present, is fine).
- [ ] **Step 4:** `make lint` + `make test`. Commit: `docs/prompts: surface config path in ## Environment; retire the status-tool affordance from shared skills`

---

## Task 6: end-to-end verification

**Files:** `test/` (mirror the existing e2e harness in `test/delegation_e2e_test.go`).

- [ ] **Step 1 (resume picks recorded config):** run a session under config A (a temp `shell3.lua` whose agent prints a distinctive marker), exit; then `shell3 run --resume <id>` WITHOUT `--config` from a different cwd; assert it ran under A (marker present / correct agent). Compare against the bug: with no recorded config it would fall to the default and differ.
- [ ] **Step 2 (FTS):** seed a tool turn + user turn with a shared term; assert `shell3 fts <term>` returns only the user turn.
- [ ] **Step 3 (revive uses parent config):** confirm the existing delegation revive e2e still passes and (if cheap) assert the revived process used the parent's recorded config_path.
- [ ] **Step 4:** `make lint` + `make test`. Commit: `test(e2e): resume/revive honor recorded config; fts excludes tool turns`

---

## Self-review checklist
- `status` gone from every TUI-facing prompt; `## Environment` now carries the config path (works for Telegram too).
- FTS search excludes `role='tool'`; `read-session` still complete.
- `sessions.config_path` recorded at StartSession; resume + revive prefer it; explicit `--config` overrides.
- `list-sessions` surfaces it.
- Live `~/.shell3` skills synced; user's `shell3.lua` touched only by a single-line description edit.
- TUI/runtime `ListSessions(limit)` dashboard path left intact.
