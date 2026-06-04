# Remove memory feature + environment prompt block

**Date:** 2026-06-04
**Status:** Approved

## Goal

Remove two engine features entirely, with all logic taken out of Go (no
replacement, no backwards compatibility):

1. **Memory** — the `memory_upsert` / `memory_list` / `memory_search` tools, the
   `memories` SQLite table, the `## Core memories` prompt block, and the
   `memory` tool gate + `core_memories` agent key.
2. **Environment block** — the `## Environment` prompt block (Workdir / Model /
   Time). Deleted with **no replacement**; the model does not need the date.

## Non-goals / what stays

- The `store` package stays: **sessions, history, and history FTS5 search** are
  untouched. Only the `memories` table and its functions are removed.
- No time/date injection of any kind is added back. Out of scope.
- No backwards compatibility. Only two configs exist (the embedded scaffold
  default and the author's `~/.shell3/shell3.lua`); both are updated in this
  change. Removed agent/gate keys (`environment`, `core_memories`, `memory`)
  will hard-error at load via `checkKeys` for any config still using them — this
  is acceptable and intended.

## Data decision

The existing `memories` table is **dropped**, not orphaned. Store
initialization runs `DROP TABLE IF EXISTS memories` (idempotent) and no longer
creates it. No data needs preserving (test-only usage).

## Changes by layer

| Layer | File | Change |
| --- | --- | --- |
| Store | `internal/store/store.go` | Remove `memories` FTS5 table creation + `migrateMemoriesAddCore`; add `DROP TABLE IF EXISTS memories`; remove `MemoryEntry`, `MemoryUpsert`, `MemoryQuery`, `MemorySearchExpr`, `scanMemoryRows`; fix package/Store doc comments. History + sessions stay. |
| Store tests | `internal/store/store_test.go` | Remove memory tests. |
| Tool defs | `internal/luacfg/tooldefs.go` | Remove `memoryUpsertTool`/`memoryListTool`/`memorySearchTool` + the `if g.Memory {...}` block. |
| Gates / keys | `internal/luacfg/register.go`, `luacfg.go` | Drop `memory` from `toolGateKeys`; drop `environment`+`core_memories` from `agentKeys`; remove the `Memory` gate field and the `Environment`/`CoreMemories` `Agent` fields and their `optBool` assignments. |
| Persona | `internal/luacfg/persona.go` | Delete `## Environment` and `## Core memories` blocks. Delete the now-unused `RuntimeData` struct; simplify `BuildPersona` to render prompt + skills block only. |
| Chat | `internal/chat/chat.go`, `handler_store.go` | Unregister the 3 memory `StoreHandler`s; remove `storeMemoryUpsert/List/Search` + `renderMemoryEntries`. History handlers stay. |
| Agent setup | `internal/agentsetup/agentsetup.go` | `openStore` condition becomes `a.Gates.History` only; remove `coreMemories` field + load; drop `time.Now()`/CWD plumbing into `BuildPersona`. |
| TUI | `internal/tui/render.go` | Drop `memory_*` from the tool-color helper (keep `history_*`); rename `isMemoryHistoryTool` → `isHistoryTool`. |
| Configs | `internal/scaffold/defaults/shell3.lua`, `~/.shell3/shell3.lua` | Remove `memory = true` gates, `environment = true`, `core_memories = true`. |
| Docs | `internal/docs/shell3.md` | Remove memory-tool, `core_memories`, and `environment` sections. |

## Verification

- `go build ./...`, `go vet ./...`
- `go test ./...` (store memory tests removed)
- Residual-reference sweep for memory/environment/core_memories symbols
- `shell3 doctor` against the live home config
