# Single Canonical DB — Design Spec

**Status:** approved (2026-06-13), executing
**Plan:** `docs/dev/superpowers/plans/2026-06-13-single-canonical-db.md`

## Problem

Subagent completion reports were silently lost ("never pinged back"). Root cause: the project SQLite DB **and** the per-session Unix socket are both re-derived from the process working directory (`<cwd>/.shell3/.ref` → `~/.shell3/projects/<uuid>/shell3.db`; `SockPath(cwd, id)`). The `bash_bg` tool lets the model choose a spawned child's launch directory (`workdir` param, or a `cd` in the command). When a child's CWD diverged from the parent's, it opened a **different** project DB where the parent session did not exist:

- `StartSessionWithParent(parentID)` and the completion `report` ran against the wrong DB.
- `Liveness(parentID)` → `sql.ErrNoRows` → treated as "dormant".
- `ClaimRevive(parentID)` → 0 rows affected → `won=false, err=nil` → code path "a winner will deliver" returns, but **there is no winner**.
- Report black-holed. No SQL error surfaced (all liveness/inbox writes are `_ =`-swallowed).

Confirmed live: a child `bash_bg`-spawned with `workdir: ~/.shell3/projects/<uuid>/` created a parallel project, reported into it, and the real parent (correctly `live`) never heard back.

## Decision

Move to a **single canonical database** per home at `~/.shell3/data/shell3.db`, shared by every front-end (CLI, TUI, telegram). Conversation history stays logically per-project via a `project_uuid` column. Orchestration routing (parent pointer, liveness, inbox, revive) then needs only the canonical DB path + a globally-unique session id — **CWD drops out of the routing path entirely**. A wrong CWD can at worst mislabel a child's own history rows; it can no longer black-hole a report.

Rejected alternatives:
- **In parent memory only** — cannot satisfy durable delegation (a dormant/exited parent has no memory to deliver into). That is exactly the revive feature just built.
- **Separate orchestration DB + per-project history DBs** — works, but needs a cross-file foreign key (global session id referenced by a separate history file), which SQLite can't enforce. The single-DB-with-`project_uuid`-column collapses that.
- **`--cwd` flag to force shared pwd** — enforces the fragile invariant instead of removing the dependency; would be dead code under the single-DB model. Not pursued.

## Requirements

1. One DB file at `~/.shell3/data/shell3.db` (sidecars `-wal`/`-shm` alongside). `data/` is gitignored.
2. `sessions` gains `project_uuid TEXT` and `workdir TEXT`. Every session-start records both.
3. The `history` FTS5 table gains `project_uuid UNINDEXED`, stamped at insert via subselect on the session — so per-project full-text search is a single-table filter (no FTS5 external-content join).
4. `.shell3/.ref` → UUID survives only as a **namespacing key**, never a path component. The per-project directory, `meta.json`, and `FindByCWD`/`ReadMeta` CWD-recovery machinery are **deleted** (clean break, no migration).
5. `shell3 fts "<query>" [--project-id <uuid>] [--page N]` — read-only paginated full-text search. `--project-id` optional (omit → all projects). Becomes the agent's primary history interface (the `history` skill drives it instead of hand-written `sqlite3 … MATCH …`).
6. `shell3 list-projects [--page N]` — paginated `DISTINCT project_uuid` listing with workdir, session count, last activity. Replaces the deleted `ls ~/.shell3/projects/` discovery; enables cross-project resume from the TUI/telegram.
7. Cross-project resume: TUI `--resume <id>` and `shell3 run --resume <id>` now resolve any session because all front-ends share the one DB. No change to resume itself.

## Non-goals (explicit follow-ups, not this change)

- The **crash-strand** bug: a parent killed with `kill -9` stays `status='live'` with a stale sock; `pid` is never liveness-checked, so `ClaimRevive` (which only fires on `'dormant'`) can never reclaim it and reports strand. Predates this work; fix separately by reclaiming a `'live'` row whose `pid` is dead.
- Logging the swallowed `_ =` errors on `SetLiveness`/`AppendInbox`/`ClaimRevive` to `applog` so transport failures stop being invisible.

## Acceptance

- `go build ./... && go vet ./... && go test ./...` all green.
- E2E proof: a child launched from a directory **different** from the parent (no shared pwd, no `--cwd`) still reports to and revives the parent (`test/delegation_e2e_test.go::TestDelegation_DivergentChildCwdStillReachesParent`).
- `shell3 fts` and `shell3 list-projects` return correct, project-scoped, paginated results (store-level unit tests).
