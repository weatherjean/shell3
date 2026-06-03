# agentsetup.Build: staged builder extraction — design

**Date:** 2026-06-03
**Status:** approved (brainstorm complete; feeds an implementation plan)
**Scope:** `internal/agentsetup/agentsetup.go` — the second of three deferred refactors
recorded in `docs/superpowers/notes/refactor-backlog.md`.

## Goal

`Build` is ~177 lines (`agentsetup.go:40–216`) that resolve paths, ensure dirs,
open the log, load `shell3.lua`, build clients, open the store, and assemble a
`chat.Config` — with an **incremental cleanup ladder**: each error path manually
re-closes the resources acquired so far (`logCloser`, then `lc`, then `store`).
Split `Build` into a package-private `builder` struct with stage methods, and
centralize teardown in a LIFO closer stack so the repeated manual close-ladders
disappear. **No externally-observable behavior change.**

This is a readability refactor. The other two backlog items, and
`ResolveConfigPath`/`fileExists` (already small and separate), are out of scope.

## Background / constraints

`Build` today acquires three closeable resources in order: the **log**
(`applog.Open`, line 61), the **Lua config** (`luacfg.Load`, line 73), and the
**store** (`store.Open`, line 129). The final `cleanup` closure (lines 208–214)
closes them in the order **store → lc → log**. The two early error paths close
the subset acquired so far in the same reverse order (line 75–76: log only;
lines 97–99: lc then log).

**Key correctness invariant:** acquisition order is log → lc → store, so a LIFO
teardown stack (close in reverse-acquisition order) reproduces *both* the final
`cleanup()` ordering *and* every error path's partial close **exactly**. The
cleanup stack is therefore behavior-preserving, not merely tidier.

Two resource opens are **non-fatal** and must stay so: `applog.Open` failure
falls back to `applog.Noop{}` + a stderr warning (lines 62–68); `store.Open`
failure logs a warning and leaves the store nil (lines 128–134). Neither aborts
`Build`.

`Build` is called by two production entry points: `cmd/shell3/run.go:62` and
`pkg/shell3/shell3.go:123`. Its signature `Build(Options) (chat.Config, func(),
error)` must not change.

**Existing tests** (`agentsetup_test.go`): `TestBuild_LoadsConfig` (happy path,
gates off so the store never opens) and `TestBuild_MissingConfig_Errors` (error
*before* the log opens). The post-log-open error path and the store-open/close
path are currently uncovered. The `writeMinimalConfig` helper writes a
`shell3.lua` + `.env` in a temp dir; the memory gate is enabled via the agent's
`tools = { memory = true }` table (`Build` opens the store when
`Gates.Memory || Gates.History`).

## Design

### The `builder` struct (package-private)

```go
// builder accumulates the state and open resources used to assemble a
// chat.Config across Build's stages. closers is a LIFO teardown stack:
// stages push a closer as they acquire a resource, and closeAll runs them in
// reverse-acquisition order — matching Build's original cleanup ordering.
type builder struct {
	opts Options

	g    paths.Global
	l    paths.Local
	proj paths.Project
	uuid string

	log applog.Logger
	lc  *luacfg.LoadedConfig
	st  *store.Store

	m            luacfg.Model
	client       chat.LLMClient
	rp           llm.RequestParams
	models       []chat.ModelInfo
	coreMemories []store.MemoryEntry

	closers []func() // LIFO teardown stack
}

func (b *builder) closeAll() {
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}
```

### Stage methods

Each stage mutates the builder; a returned error aborts `Build`.

- `resolvePaths() error` — `ResolveConfigPath`, `paths.NewGlobal/NewLocal`,
  `bootstrap.EnsureGlobal`, `bootstrap.EnsureProject` (sets `uuid`, `proj`).
  Acquires no closeable resource.
- `openLog()` — `applog.Open` with `logMaxBytes`/`logArchives`; on error, stderr
  warning + `applog.Noop{}` (non-fatal, no abort). Pushes the log closer.
- `loadConfig() error` — `luacfg.Load(configPath, filepath.Dir(configPath))`.
  Pushes `lc.Close`.
- `resolveModel() error` — look up `lc.Model(lc.Agent.ModelName)` (errors abort
  with "agent references unknown model"), `buildClient` for the initial client,
  build the `models` slice. No closer.
- `openStore()` — if `lc.Agent.Gates.Memory || lc.Agent.Gates.History`,
  `store.Open(proj.DB)` (non-fatal warning on failure); load core memories
  (`MemoryQuery(true, 0)`). Pushes `st.Close` when the store opened. No abort.
- `assemble() chat.Config` — `buildPrompt`, `customDefs`/`toolDefs`, `persona`,
  `customNames`, `toolNames`, and the `chat.Config` literal. Cannot error.

### `Build` orchestrator

```go
func Build(opts Options) (chat.Config, func(), error) {
	b := &builder{opts: opts}
	noop := func() {}
	if err := b.resolvePaths(); err != nil {
		return chat.Config{}, noop, err // nothing acquired yet
	}
	b.openLog() // non-fatal; may push the log closer
	if err := b.loadConfig(); err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	if err := b.resolveModel(); err != nil {
		b.closeAll()
		return chat.Config{}, noop, err
	}
	b.openStore() // non-fatal; may push the store closer
	return b.assemble(), b.closeAll, nil
}
```

### Closures

- `buildClient` becomes a small free function
  `buildClient(md luacfg.Model) (chat.LLMClient, llm.RequestParams)` — it needs
  no builder state. Used by `resolveModel` and captured by `switchModel`.
- `switchModel`, `buildPrompt`, and the `ToolGuard` `int`-adapter closure are
  created inside `assemble()` capturing builder state (`b.lc`, `b.m`, `b.opts`,
  `b.coreMemories`) and stored into `chat.Config` — identical semantics to today.

### File placement

Everything stays in `agentsetup.go`. The package is one small file; the builder
keeps related logic cohesive. No new file.

## Testing

**Phase A — characterize first (added before the extraction, must pass against
current code).** Two tests for the currently-uncovered observable paths:

1. `TestBuild_MalformedConfig_Errors` — write a *present but syntactically
   invalid* `shell3.lua` (plus `.env`) in a temp dir; `Build` must return a
   non-nil error. This exercises the post-log-open error/cleanup path (log
   opened, then `luacfg.Load` fails) that no test covers today.
2. `TestBuild_WithStore_CleanupSafe` — write a config whose agent has
   `tools = { memory = true }` so `store.Open` actually runs; assert `Build`
   succeeds, `cfg` is populated, and calling `cleanup()` is panic-free (and may
   be called once safely). This covers the store closer the happy-path test
   skips.

Cleanup *ordering* is not observable through `Build`'s public API, so it is held
by `go test -race`, the existing tests, and code review — not by an assertion.

**Phase B — extract, gated green.** Introduce the `builder` + stage methods;
the four tests (two existing + two new) plus the full gate (`go build`,
`go vet`, `go test -race`, `staticcheck`, `gofmt -l`, `deadcode`) must stay
clean. The extraction preserves logic verbatim; the LIFO closer stack reproduces
the exact cleanup ordering.

## Out of scope

- The other two backlog refactors (`RunTurn` is already done; `patchapp.App`
  remains).
- `ResolveConfigPath` and `fileExists` (already small, separate, tested
  indirectly) — untouched.
- Any behavior change, including the non-fatal log/store fallbacks, which are
  preserved exactly.
- Dependency injection of resources for testability — would change the public
  surface; not pursued.

## Success criteria

- `Build` shrinks to a short orchestrator (~15 lines) delegating to named
  stages; the cleanup ladder is replaced by one LIFO `closeAll`.
- The two new characterization tests exist and pass before and after the
  extraction; all four agentsetup tests pass.
- Full gate clean; no externally-observable behavior change; `Build`'s signature
  and the two call sites are untouched.
