# Phase 2+ Backlog

Items deferred from Phase 1 structural refactor. Tackle after Phase 1 ships and tests are green.

---

## Phase 2: Infrastructure

### ~~Structured Logger Interface~~ ✓ done
### ~~Magic Constants Centralization~~ ✓ done
### ~~`looksLikeError()` / prune threshold~~ ✓ done — threshold and heuristic removed entirely; AI can prune any result

---

## Phase 3: Hardening

### Hook Failure Visibility
- Fire-and-forget hook dispatch silently drops errors
- Add log call on hook failure (logger now available via `TurnConfig.Log`)

### Store Session End Error Handling
- `EndSession()` errors deferred with `_ =` — lost history on failure
- At minimum: log the error

### Input Validation for Tool Parameters
- Tool args arrive as `json.RawMessage`, no schema validation before dispatch
- Validate against `ToolDefinition.Parameters` JSON schema before calling `Execute()`

### Codex/OpenAI Adapter Coverage
- `adapters/codex`: low coverage — 227-line SSE state machine untested
- `adapters/openai`: low coverage — HTTP client with custom RoundTripper untested
- Extract `StreamParser` interface to enable testing without HTTP

### patchwidgets Coverage
- Lowest non-adapter package coverage
- Interactive prompt handling and TTY detection need test harness

---

## Notes

- Adapter rework is lowest priority — adapters work, just untested
- Each phase should be its own implementation plan + PR
