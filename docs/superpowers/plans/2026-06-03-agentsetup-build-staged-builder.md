# agentsetup.Build Staged-Builder Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Split `agentsetup.Build` (~177 lines) into a package-private `builder` struct with stage methods and a LIFO closer stack, replacing the manual per-error cleanup ladder, with no externally-observable behavior change.

**Architecture:** Two phases. **Phase A** (Tasks 1–2) adds two characterization tests for the currently-uncovered observable paths (post-log-open error; store-open + cleanup). **Phase B** (Task 3) extracts the builder and rewrites `Build` as a short orchestrator. Task 4 runs the full gate and finishes the branch.

**Tech Stack:** Go 1.26, module `github.com/weatherjean/shell3`. Tests are standard `testing` with temp dirs. Quality tools installed: `go vet`, `staticcheck`, `gofmt`, `deadcode` (under `/Users/weatherjean/go/bin`).

---

## Context for the executor (read first)

You have **zero prior context**. Key facts:

1. **Branch.** Work on `refactor/agentsetup-build` (already created; the design spec is committed there). Do NOT switch or create branches.

2. **The design spec** is `docs/superpowers/specs/2026-06-03-agentsetup-build-staged-builder-design.md`. Read it. The chosen shape is a private `builder` struct + stage methods + a LIFO `closers` stack.

3. **THE GATE.** After every task, all of these must be clean before you commit:
   ```bash
   go build ./...
   go vet ./...
   go test -race ./...                       # all ok, no FAIL/panic
   staticcheck ./...                          # empty
   gofmt -l $(git ls-files '*.go')            # empty
   deadcode -test ./...                       # empty
   ```
   The tree is clean at baseline; any new output is yours.

4. **Characterization tests pass on FIRST write.** Phase A tests document *current* behavior, so they must PASS against the unmodified `Build`. If one fails on first run, your understanding (or the test) is wrong — investigate; do NOT change `Build` to make it pass.

5. **`Build` is in `internal/agentsetup/agentsetup.go`** (function spans lines 40–216 at baseline). Its signature `Build(Options) (chat.Config, func(), error)` MUST NOT change — it is called by `cmd/shell3/run.go:62` and `pkg/shell3/shell3.go:123`.

6. **Behavior to preserve exactly:** acquisition order is **log → lc → store**, and teardown must run **store → lc → log** (reverse). Both `applog.Open` and `store.Open` failures are **non-fatal** (log → `applog.Noop{}` + stderr warning; store → warning, nil store) and must stay non-fatal.

7. **Commit style.** End each commit message with:
   ```
   Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
   ```

---

## Task 1: Phase A — characterize the post-log-open error path

Add a test that a *present but syntactically invalid* `shell3.lua` makes `Build`
return an error. This exercises the error path that runs *after* the log is
opened (log open → `luacfg.Load` fails), which no test covers today.

**Files:**
- Modify: `internal/agentsetup/agentsetup_test.go`

- [ ] **Step 1: Append the test**

Add to the END of `internal/agentsetup/agentsetup_test.go` (imports `os`,
`path/filepath`, `testing`, and the `agentsetup` package are already present):

```go
// TestBuild_MalformedConfig_Errors characterizes the post-log-open error path:
// a present but syntactically invalid shell3.lua resolves (so the log opens),
// then luacfg.Load fails — Build must surface the error.
func TestBuild_MalformedConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "shell3.lua"), []byte("this is ((( not valid lua\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/agentsetup/ -run TestBuild_MalformedConfig_Errors -v`
Expected: PASS. (Confirmed behavior: `luacfg.Load` returns a parse error for invalid Lua.)

- [ ] **Step 3: Gate + commit**

Run the full GATE (Context #3), then:

```bash
git add internal/agentsetup/agentsetup_test.go
git commit -m "$(cat <<'EOF'
test(agentsetup): characterize malformed-config error path

A present-but-invalid shell3.lua opens the log then fails to load; Build must
return the error. Pins the post-log-open cleanup path ahead of staging Build.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Phase A — characterize the store-open + cleanup path

Add a test that a config with the **memory gate on** actually opens the store
(so `cfg.Store != nil`) and that `cleanup()` runs without panicking. The
existing happy-path test has gates off, so the store closer is never exercised.

**Files:**
- Modify: `internal/agentsetup/agentsetup_test.go`

- [ ] **Step 1: Append a config helper + the test**

Add to the END of `internal/agentsetup/agentsetup_test.go`:

```go
// writeConfigWithMemory writes a shell3.lua whose agent enables the memory
// tool, so Build opens the store (Gates.Memory || Gates.History). Mirrors
// writeMinimalConfig but flips tools = { memory = true }.
func writeConfigWithMemory(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.agent({ name = "tester", model = "main", prompt = "you are a tester", tools = { memory = true } })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBuild_WithStore_CleanupSafe characterizes the store-open path: with the
// memory gate on, Build opens the store (cfg.Store != nil) and the returned
// cleanup closes it without panicking. Covers the store closer the gates-off
// happy-path test skips.
func TestBuild_WithStore_CleanupSafe(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeConfigWithMemory(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.Store == nil {
		t.Fatal("expected store to be opened with the memory gate on")
	}
	cleanup() // closes store + lua + log; must not panic
}
```

- [ ] **Step 2: Run the test — expect PASS**

Run: `go test ./internal/agentsetup/ -run TestBuild_WithStore_CleanupSafe -v`
Expected: PASS. (If `cfg.Store` is nil, the memory gate or store open isn't
working as assumed — investigate; do not change `Build`.)

- [ ] **Step 3: Run the whole agentsetup suite — expect PASS**

Run: `go test ./internal/agentsetup/ -v`
Expected: all four tests PASS (`TestBuild_MissingConfig_Errors`,
`TestBuild_LoadsConfig`, `TestBuild_MalformedConfig_Errors`,
`TestBuild_WithStore_CleanupSafe`).

- [ ] **Step 4: Gate + commit**

Run the full GATE, then:

```bash
git add internal/agentsetup/agentsetup_test.go
git commit -m "$(cat <<'EOF'
test(agentsetup): characterize store-open + cleanup path

With the memory gate on, Build opens the store and cleanup closes it. Covers
the store closer the gates-off happy-path test never exercises.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Phase B — extract the `builder` and stage methods

Replace the `Build` function with a package-private `builder` struct, stage
methods, a LIFO `closeAll`, a free `buildClient`, and a short `Build`
orchestrator. Pure structural move; logic is preserved verbatim.

**Files:**
- Modify: `internal/agentsetup/agentsetup.go`

- [ ] **Step 1: Remove the now-unused `io` import**

In the import block of `internal/agentsetup/agentsetup.go`, delete the line:

```go
	"io"
```

(The original only used `io` for `io.NopCloser(nil)` in `openLog`'s error path; the new builder pushes no closer on log-open failure, so `io` is no longer referenced. Leaving it causes an "imported and not used" build error.)

- [ ] **Step 2: Replace the `Build` function with the builder**

Replace the ENTIRE current `Build` function — from the doc comment line
`// Build assembles the full chat.Config. ...` through its closing `}` at the
end (the block immediately above `// ResolveConfigPath returns ...`) — with the
following. Do NOT touch the `Options` struct above it, or `ResolveConfigPath` /
`fileExists` below it.

```go
// builder accumulates the state and open resources used to assemble a
// chat.Config across Build's stages. closers is a LIFO teardown stack: stages
// push a closer as they acquire a resource, and closeAll runs them in
// reverse-acquisition order — matching Build's original cleanup ordering
// (store → lc → log).
type builder struct {
	opts Options

	configPath string
	g          paths.Global
	l          paths.Local
	proj       paths.Project
	uuid       string

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

// Build assembles the full chat.Config. The returned cleanup closes the store,
// the Lua state, and the log; callers MUST invoke it.
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

// closeAll runs the teardown stack in reverse-acquisition order.
func (b *builder) closeAll() {
	for i := len(b.closers) - 1; i >= 0; i-- {
		b.closers[i]()
	}
}

// resolvePaths resolves the config path, builds the global/local/project path
// sets, and ensures the global and project directories exist.
func (b *builder) resolvePaths() error {
	configPath, err := ResolveConfigPath(b.opts.ConfigPath, b.opts.CWD, b.opts.HomeDir)
	if err != nil {
		return err
	}
	b.configPath = configPath
	b.g = paths.NewGlobal(b.opts.HomeDir)
	b.l = paths.NewLocal(b.opts.CWD)
	if err := bootstrap.EnsureGlobal(b.g); err != nil {
		return err
	}
	uuid, err := bootstrap.EnsureProject(b.l, b.g, b.opts.CWD)
	if err != nil {
		return err
	}
	b.uuid = uuid
	b.proj = paths.NewProject(b.g, uuid)
	return nil
}

// openLog opens the rotating app log. Failure is non-fatal: it warns on stderr
// (the log itself being unavailable to record it) and falls back to Noop.
func (b *builder) openLog() {
	const logMaxBytes = 2 * 1024 * 1024
	const logArchives = 3
	log, logCloser, err := applog.Open(b.g.LogFile, logMaxBytes, logArchives)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: open log file:", err)
		b.log = applog.Noop{}
		return
	}
	b.log = log
	b.closers = append(b.closers, func() { _ = logCloser.Close() })
}

// loadConfig loads shell3.lua. The Lua/.env workdir is the config file's
// directory; the agent's bash cwd stays opts.CWD. These differ on purpose.
func (b *builder) loadConfig() error {
	lc, err := luacfg.Load(b.configPath, filepath.Dir(b.configPath))
	if err != nil {
		return err
	}
	b.lc = lc
	b.closers = append(b.closers, func() { lc.Close() })
	return nil
}

// resolveModel resolves the agent's configured model, builds the initial client
// and request params, and enumerates every model for the /model command.
func (b *builder) resolveModel() error {
	m, ok := b.lc.Model(b.lc.Agent.ModelName)
	if !ok {
		return fmt.Errorf("agent references unknown model %q", b.lc.Agent.ModelName)
	}
	b.m = m
	b.client, b.rp = buildClient(m)
	for _, md := range b.lc.Models {
		b.models = append(b.models, chat.ModelInfo{
			Name:          md.Name,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		})
	}
	return nil
}

// openStore opens the SQLite store when the agent gates memory or history, and
// loads core memories. Both are non-fatal: a failure warns and proceeds.
func (b *builder) openStore() {
	if b.lc.Agent.Gates.Memory || b.lc.Agent.Gates.History {
		if s, e := store.Open(b.proj.DB); e == nil {
			b.st = s
			b.closers = append(b.closers, func() { _ = s.Close() })
		} else {
			b.log.Warn("open store failed — memory and history unavailable", "error", e)
		}
	}
	if b.st != nil {
		if mems, e := b.st.MemoryQuery(true, 0); e != nil {
			b.log.Warn("load core memories failed", "error", e)
		} else {
			b.coreMemories = mems
		}
	}
}

// assemble renders the persona and builds the final chat.Config, including the
// switchModel / buildPrompt / ToolGuard closures stored into it.
func (b *builder) assemble() chat.Config {
	// buildPrompt renders the system prompt with a fresh timestamp each call.
	// Used once now for the initial prompt and again by /clear (via
	// cfg.RefreshPrompt) so a new conversation re-stamps the clock.
	buildPrompt := func() string {
		return b.lc.BuildPersona(luacfg.RuntimeData{
			Time:         time.Now().Format("Mon Jan 2 2006, 15:04 MST"),
			CWD:          b.opts.CWD,
			Model:        b.m.ModelID,
			CoreMemories: b.coreMemories,
		})
	}
	// switchModel rebuilds the active client when the user switches by name.
	switchModel := func(name string) (chat.ActiveModel, error) {
		md, ok := b.lc.Model(name)
		if !ok {
			return chat.ActiveModel{}, fmt.Errorf("unknown model %q", name)
		}
		cl, p := buildClient(md)
		return chat.ActiveModel{
			Client:        cl,
			Params:        p,
			ModelID:       md.ModelID,
			ContextWindow: md.ContextWindow,
		}, nil
	}

	customDefs := b.lc.CustomToolsFor(b.lc.Agent.CustomTools)
	hasSkills := b.lc.Agent.SkillsActive()
	toolDefs := luacfg.ToolDefs(b.lc.Agent.Gates, customDefs, hasSkills)

	pers := persona.Persona{
		Name:         b.lc.Agent.Name,
		SystemPrompt: buildPrompt(),
		Tools:        toolDefs,
		Parameters:   b.rp,
	}

	customNames := make(map[string]bool, len(b.lc.Agent.CustomTools))
	for _, n := range b.lc.Agent.CustomTools {
		customNames[n] = true
	}
	if hasSkills {
		customNames["skill"] = true
	}

	toolNames := make([]string, 0, len(toolDefs))
	for _, t := range toolDefs {
		toolNames = append(toolNames, t.Name)
	}

	return chat.Config{
		LLM:             b.client,
		Store:           b.st,
		Personality:     pers,
		RefreshPrompt:   buildPrompt,
		WorkDir:         b.opts.CWD,
		StatusLine:      fmt.Sprintf("%s │ %s", b.lc.Agent.Name, b.m.ModelID),
		ModeLabel:       b.lc.Agent.Name,
		ProjectRef:      b.uuid,
		ActiveSkills:    b.lc.Agent.Skills,
		ActiveTools:     toolNames,
		ContextWindow:   b.m.ContextWindow,
		Docs:            docs.Content,
		CustomTool:      b.lc.CallTool,
		CustomToolNames: customNames,
		ToolGuard: func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := b.lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		},
		Params:      b.rp,
		Log:         b.log,
		OutPath:     b.opts.OutPath,
		Headless:    b.opts.Headless,
		Models:      b.models,
		SwitchModel: switchModel,
	}
}

// buildClient constructs a streaming client plus its request params from a
// configured model. Reused for the initial client and for /model switches.
func buildClient(md luacfg.Model) (chat.LLMClient, llm.RequestParams) {
	cl := openai.NewClient(md.BaseURL, md.APIKey, md.ModelID)
	rp := llm.RequestParams{
		ReasoningEffort: md.Reasoning,
		MaxTokens:       md.MaxTokens,
		Temperature:     md.Temperature,
	}
	cl.SetParams(rp)
	if md.Extra != nil {
		cl.SetExtra(md.Extra)
	}
	return cl, rp
}
```

- [ ] **Step 3: Build — expect success**

Run: `go build ./...`
Expected: builds clean. If you see "imported and not used: io", you skipped
Step 1. If you see "declared and not used" for a builder field, re-check you
copied every stage method.

- [ ] **Step 4: Run the full agentsetup suite — expect PASS (behavior preserved)**

Run: `go test ./internal/agentsetup/ -v`
Expected: all four tests PASS — proving the extraction preserved behavior.

- [ ] **Step 5: Gate**

Run the full GATE (Context #3). All clean — watch `deadcode` (every stage method
must be reachable from `Build`; `buildClient` is used by `resolveModel` and
`assemble`) and `staticcheck` (no unused fields).

- [ ] **Step 6: Commit**

```bash
git add internal/agentsetup/agentsetup.go
git commit -m "$(cat <<'EOF'
refactor(agentsetup): stage Build into a builder with a cleanup stack

Replace the ~177-line Build and its repeated per-error close-ladders with a
private builder struct and stage methods (resolvePaths, openLog, loadConfig,
resolveModel, openStore, assemble). A LIFO closers stack centralizes teardown
and reproduces the original store → lc → log ordering on both success and every
error path. buildClient becomes a free function. Pure structural change; the
four agentsetup tests pass unchanged. Drops the now-unused io import.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Final verification + finish the branch

**Files:** none (verification + git)

- [ ] **Step 1: Full gate on the final tree**

Run all GATE commands once more; confirm each is clean and `go test -race ./...`
shows no FAIL/panic across ALL packages.

- [ ] **Step 2: Confirm Build shrank**

Run: `awk '/^func Build/{s=NR} s&&/^}/{print NR-s+1" lines"; exit}' internal/agentsetup/agentsetup.go`
Expected: ~15 lines (the orchestrator), down from ~177.

- [ ] **Step 3: Finish**

Invoke the **superpowers:finishing-a-development-branch** skill to verify tests,
present integration options (merge to `main` / PR / keep / discard), and execute
the choice.

---

## Self-review checklist (done during authoring)

- **Spec coverage:** Phase A covers both observable paths the spec names
  (malformed-config error path; store-open + cleanup). Phase B implements the
  exact builder/stage/closer design from the spec, preserving the store → lc →
  log teardown ordering and the non-fatal log/store fallbacks. `ResolveConfigPath`
  / `fileExists` and the `Build` signature are untouched. One file, as specified.
- **No placeholders:** every test and the full replacement code are shown
  verbatim; every command has an expected result; the `io`-import removal is
  called out explicitly.
- **Type/name consistency:** `builder`, `closeAll`, `resolvePaths`, `openLog`,
  `loadConfig`, `resolveModel`, `openStore`, `assemble`, `buildClient`, and the
  builder fields (`configPath`, `g`, `l`, `proj`, `uuid`, `log`, `lc`, `st`,
  `m`, `client`, `rp`, `models`, `coreMemories`, `closers`) are used identically
  across the orchestrator and the stage methods.
- **Behavior preservation:** stage bodies are a verbatim move of the original
  Build code; the LIFO closer stack reproduces the original cleanup ordering on
  success and on every error path; log/store opens stay non-fatal.
