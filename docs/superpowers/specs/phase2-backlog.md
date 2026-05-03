# Phase 2+ Backlog

Items deferred from Phase 1 structural refactor. Tackle after Phase 1 ships and tests are green.

---

## Phase 2: Infrastructure

### Structured Logger Interface
- No logging layer exists; errors go to stderr ad-hoc or `.shell3/last_error.json`
- Define `Logger` interface with `Debug(msg, fields...)` and `Error(msg, err, fields...)`
- No-op implementation for tests, stderr implementation for prod
- Replace JSON error dump in `turn.go` with logger call
- Thread logger through `TurnConfig` and `ToolConfig`

### Magic Constants Centralization
All hardcoded values should move to `internal/defaults/` or be made configurable:

| Constant | Location | Value |
|----------|----------|-------|
| `minPruneBytes` | `chat/tools.go:18` | 500 |
| `hookTimeout` | `hooks/hooks.go:17` | 20s |
| `hookTTYTimeout` | `hooks/hooks.go:18` | 5m |
| `ChunkSize` | `store/store.go:13` | 25 |
| Spinner interval | `patchapp/inputbox.go:21` | 500ms |
| Reserved render lines | `patchapp/render.go` | 4 |

### `looksLikeError()` Replacement
- `chat/tools.go:90` — fragile string-prefix heuristic
- Replace with explicit error return from `ToolHandler.Execute()` (error vs result distinction is now in the interface)

---

## Phase 3: Hardening

### Hook Failure Visibility
- Fire-and-forget hook dispatch silently drops errors
- Add log call on hook failure (requires Phase 2 logger)
- Consider whether partial hook execution needs rollback semantics

### Store Session End Error Handling
- `EndSession()` errors deferred with `_ =` — lost history on failure
- At minimum: log the error (requires Phase 2 logger)

### Input Validation for Tool Parameters
- Tool args arrive as `json.RawMessage`, no schema validation before dispatch
- Validate against `ToolDefinition.Parameters` JSON schema before calling `Execute()`

### Codex/OpenAI Adapter Coverage
- `adapters/codex`: 9.3% coverage — 227-line SSE state machine untested
- `adapters/openai`: 11.1% coverage — HTTP client with custom RoundTripper untested
- Extract `StreamParser` interface to enable testing without HTTP

### patchwidgets Coverage
- 24.2% coverage — lowest non-adapter package
- Interactive prompt handling and TTY detection need test harness

---

## Notes

- Phase 2 logger must land before Phase 3 hook/store error items (dependency)
- Adapter rework is lowest priority — adapters work, just untested
- Each phase should be its own implementation plan + PR
