# Lua Config Rework Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace shell3's YAML/markdown configuration with a strict, single-file `shell3.lua` + `.env`, mirroring seasnail, deleting every replaced path in the same change (clean lateral move).

**Architecture:** A new `internal/luacfg` package loads `shell3.lua` with `gopher-lua`, keeps the `*lua.LState` alive for the session, and produces exactly what `cmd/shell3/run.go` assembles today (a model list, the single agent's rendered persona, custom-tool defs, an `on_tool_call` guard runner, and the `.env` secrets map). `pkg/chat`, the TUI, the openai adapter, `store`/`ref`/`paths`/`skills` stay unchanged except two `pkg/chat` touch-points: custom-tool dispatch routes into the live LState, and the guard runner replaces `*hooks.Runner`. The YAML auth/secrets stores, markdown persona loader, YAML user-tool loader, shell-script hook system, and the anthropic adapter are deleted.

**Tech Stack:** Go 1.25, `github.com/yuin/gopher-lua` v1.1.2, existing `pkg/chat` engine + openai-go adapter.

---

## Decisions (resolve spec §12)

- **CLI:** `shell3 [path]`. `path` defaults to `./shell3.lua`, then `~/.shell3/shell3.lua`. The file's directory is the **workdir**; `.env` is read from there. Flags `--persona`, `--provider`, `--model`, `--no-bash`, `--no-memory-tools` are **removed** (model is named in the file; tools are gated in the file; one agent per file). `--out` (JSONL) is kept.
- **db:** always auto-derived from the project `.ref` UUID (`~/.shell3/projects/<uuid>/shell3.db`). No override field.
- **compact_at_tokens:** not adopted in Part 1 (manual `compact_history` stays).
- **prune_tool_result / compact_history:** always on, no gate.
- **`extra`:** opaque map plumbed into the openai adapter via the SDK's extra-fields mechanism; the sole strict-key exception.

## File Structure

**Create:**
- `internal/luacfg/luacfg.go` — types (`LoadedConfig`, `Model`, `Agent`, `ToolGates`, `CustomTool`, `Skill`, `GuardEntry`) + `Load(path, workdir)`.
- `internal/luacfg/register.go` — registers the `shell3` global table + constructors (`model`, `tool`, `skill`, `agent`, `guards.*`, `env.secret`).
- `internal/luacfg/strict.go` — strict-key validation helpers.
- `internal/luacfg/lua_http.go` — `shell3.http.{request,get,post}` bindings.
- `internal/luacfg/lua_bash.go` — `shell3.bash` binding.
- `internal/luacfg/lua_misc.go` — `shell3.urlencode`, `shell3.env.secret`.
- `internal/luacfg/dotenv.go` — `.env` loader.
- `internal/luacfg/dispatch.go` — `CallTool` (custom-tool handler dispatch) + `OnToolCall` (guard chain) + the `Decision` type.
- `internal/luacfg/guards.go` — built-in `confirm_dangerous` guard (the old denylist, in Go).
- `internal/luacfg/persona.go` — `BuildPersona(agent, runtimeData)` assembling the verbatim prompt + standard system blocks.
- `shell3-example.lua`, `shell3-example.env.example` — shipped reference (= the migration target).
- Test files alongside each (`*_test.go`).

**Modify:**
- `cmd/shell3/run.go` — replace load/resolve/build with `luacfg.Load`.
- `cmd/shell3/main.go` — drop anthropic blank import, drop `auth`/`secrets` command registration.
- `cmd/shell3/doctor.go` — validate `shell3.lua` loads instead of YAML.
- `pkg/chat/chat.go` + `pkg/chat/turn.go` — custom-tool dispatch + guard runner type.
- `internal/adapter/openai/client.go` — accept an `extra` map.
- `go.mod` / `go.sum` — add gopher-lua.

**Delete:**
- `internal/adapter/anthropic/` (whole dir), `internal/config/authstore.go`, `internal/config/config.go`, `internal/secrets/` (whole dir), `internal/usertools/` (whole dir), `cmd/shell3/auth.go`, `cmd/shell3/secrets.go`, `pkg/hooks/` (whole dir), and the YAML/template internals of `pkg/persona/persona.go`.

---

## Phase 0 — Scaffolding

### Task 1: Add gopher-lua dependency

**Files:** Modify `go.mod`, `go.sum`.

- [ ] **Step 1: Add the module**

Run: `cd /Users/weatherjean/CODE/AGENTS/shell3 && go get github.com/yuin/gopher-lua@v1.1.2`
Expected: `go.mod` gains `github.com/yuin/gopher-lua v1.1.2`.

- [ ] **Step 2: Verify it builds**

Run: `go build ./...`
Expected: exit 0 (nothing uses it yet).

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum && git commit -m "build: add gopher-lua dependency"
```

---

## Phase 1 — Loader core: types, global table, strict keys, model()

### Task 2: Loader types

**Files:** Create `internal/luacfg/luacfg.go`.

- [ ] **Step 1: Write the types and Load skeleton**

```go
// Package luacfg loads a strict single-file shell3.lua config.
package luacfg

import (
	"sync"

	lua "github.com/yuin/gopher-lua"
)

type Model struct {
	Name, BaseURL, APIKey, ModelID string
	ContextWindow                  int
	Reasoning                      string
	MaxTokens                      int
	Temperature                    *float64
	Extra                          map[string]any
}

type ToolGates struct {
	Bash, BashBg, ShellInteractive, Edit, Memory, History, Docs bool
}

type CustomTool struct {
	Name, Description string
	Parameters        map[string]any
	handler           *lua.LFunction
}

type Skill struct{ Name, Description, Body string }

// GuardEntry is one middleware in the on_tool_call chain: either a Lua
// function or a built-in guard identified by Builtin.
type GuardEntry struct {
	fn      *lua.LFunction
	Builtin string // "" unless a shell3.guards.* handle
	prompt  bool
}

type Agent struct {
	Name, ModelName, Prompt string
	Gates                   ToolGates
	CustomTools             []string
	Skills                  []string
	Guard                   []GuardEntry
}

// LoadedConfig is the parsed result. L stays alive for the session so custom
// tool handlers and guards can run; callers MUST call Close when done.
type LoadedConfig struct {
	Models  []Model
	Agent   Agent
	Tools   map[string]CustomTool
	Skills  []Skill
	Secrets map[string]string

	L  *lua.LState
	mu sync.Mutex
}

func (c *LoadedConfig) Close() {
	if c.L != nil {
		c.L.Close()
	}
}

func (c *LoadedConfig) Model(name string) (Model, bool) {
	for _, m := range c.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/luacfg/`
Expected: exit 0.

- [ ] **Step 3: Commit**

```bash
git add internal/luacfg/luacfg.go && git commit -m "feat(luacfg): loader types"
```

### Task 3: Strict-key helper

**Files:** Create `internal/luacfg/strict.go`, `internal/luacfg/strict_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import (
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestCheckKeys(t *testing.T) {
	L := lua.NewState()
	defer L.Close()
	tbl := L.NewTable()
	tbl.RawSetString("name", lua.LString("x"))
	tbl.RawSetString("bogus", lua.LString("y"))
	err := checkKeys(tbl, "model", map[string]bool{"name": true})
	if err == nil || err.Error() != `model: unknown key "bogus"` {
		t.Fatalf("want unknown-key error, got %v", err)
	}
}
```

- [ ] **Step 2: Run it (fails to compile — checkKeys undefined)**

Run: `go test ./internal/luacfg/ -run TestCheckKeys`
Expected: FAIL (undefined: checkKeys).

- [ ] **Step 3: Implement checkKeys**

```go
package luacfg

import (
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// checkKeys fails if tbl has any string key not in allowed.
func checkKeys(tbl *lua.LTable, ctx string, allowed map[string]bool) error {
	var bad string
	tbl.ForEach(func(k, _ lua.LValue) {
		if bad != "" {
			return
		}
		if s, ok := k.(lua.LString); ok && !allowed[string(s)] {
			bad = string(s)
		}
	})
	if bad != "" {
		return fmt.Errorf("%s: unknown key %q", ctx, bad)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestCheckKeys -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/strict.go internal/luacfg/strict_test.go
git commit -m "feat(luacfg): strict-key validator"
```

### Task 4: Global table + Load entry + model() constructor

**Files:** Create `internal/luacfg/register.go`; create `internal/luacfg/model_test.go`. Add `Load` to `luacfg.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestLoadModel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", {
  base_url = "https://api.x/v1",
  api_key = "sk-test",
  model = "m-1",
  context_window = 1000,
  reasoning = "medium",
  extra = { verbosity = "high" },
})
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, ok := c.Model("main")
	if !ok || m.BaseURL != "https://api.x/v1" || m.ModelID != "m-1" ||
		m.ContextWindow != 1000 || m.Reasoning != "medium" {
		t.Fatalf("bad model: %+v", m)
	}
	if m.Extra["verbosity"] != "high" {
		t.Fatalf("extra not captured: %+v", m.Extra)
	}
}

func TestLoadModelUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.model("m", { base_url="u", api_key="k", model="x", nope=1 })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want strict-key failure, got %v", err)
	}
}
```

- [ ] **Step 2: Add test helpers**

Create `internal/luacfg/helpers_test.go`:

```go
package luacfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
func contains(s, sub string) bool { return strings.Contains(s, sub) }
```

- [ ] **Step 3: Run it (fails — Load undefined)**

Run: `go test ./internal/luacfg/ -run TestLoadModel`
Expected: FAIL (undefined: Load).

- [ ] **Step 4: Implement Load + register.go (model + a stub agent)**

Add to `luacfg.go`:

```go
import (
	"fmt"
	"path/filepath"
)

// Load reads shell3.lua at path; workdir is used for .env + relative paths.
func Load(path, workdir string) (*LoadedConfig, error) {
	env, err := loadDotEnv(filepath.Join(workdir, ".env"))
	if err != nil {
		return nil, err
	}
	c := &LoadedConfig{Tools: map[string]CustomTool{}, Secrets: env, L: lua.NewState()}
	registerShell3(c)
	if err := c.L.DoFile(path); err != nil {
		c.L.Close()
		return nil, fmt.Errorf("config: %w", err)
	}
	if c.Agent.Name == "" {
		c.L.Close()
		return nil, fmt.Errorf("config: no shell3.agent declared")
	}
	if _, ok := c.Model(c.Agent.ModelName); !ok {
		c.L.Close()
		return nil, fmt.Errorf("config: agent references unknown model %q", c.Agent.ModelName)
	}
	return c, nil
}
```

Create `register.go`:

```go
package luacfg

import lua "github.com/yuin/gopher-lua"

func registerShell3(c *LoadedConfig) {
	L := c.L
	tbl := L.NewTable()
	L.SetGlobal("shell3", tbl)
	L.SetField(tbl, "model", L.NewFunction(c.luaModel))
	L.SetField(tbl, "agent", L.NewFunction(c.luaAgent))
	// tool, skill, guards, env, http, bash, urlencode added in later tasks.
}

var modelKeys = map[string]bool{
	"base_url": true, "api_key": true, "model": true, "context_window": true,
	"reasoning": true, "max_tokens": true, "temperature": true, "extra": true,
}

func (c *LoadedConfig) luaModel(L *lua.LState) int {
	name := L.CheckString(1)
	opts := L.CheckTable(2)
	if err := checkKeys(opts, "model", modelKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	m := Model{
		Name:          name,
		BaseURL:       optStr(opts, "base_url"),
		APIKey:        optStr(opts, "api_key"),
		ModelID:       optStr(opts, "model"),
		ContextWindow: optInt(opts, "context_window"),
		Reasoning:     optStr(opts, "reasoning"),
		MaxTokens:     optInt(opts, "max_tokens"),
		Temperature:   optFloatPtr(opts, "temperature"),
	}
	if m.BaseURL == "" || m.APIKey == "" || m.ModelID == "" {
		L.RaiseError("model %q: base_url, api_key, model are required", name)
	}
	if ex, ok := opts.RawGetString("extra").(*lua.LTable); ok {
		m.Extra = tableToMap(ex)
	}
	c.Models = append(c.Models, m)
	return 0
}

// luaAgent is a minimal stub here; fully implemented in Task 9.
func (c *LoadedConfig) luaAgent(L *lua.LState) int {
	opts := L.CheckTable(1)
	c.Agent.Name = optStr(opts, "name")
	c.Agent.ModelName = optStr(opts, "model")
	c.Agent.Prompt = optStr(opts, "prompt")
	return 0
}
```

Create `internal/luacfg/convert.go` with the value helpers:

```go
package luacfg

import lua "github.com/yuin/gopher-lua"

func optStr(t *lua.LTable, k string) string {
	if s, ok := t.RawGetString(k).(lua.LString); ok {
		return string(s)
	}
	return ""
}
func optInt(t *lua.LTable, k string) int {
	if n, ok := t.RawGetString(k).(lua.LNumber); ok {
		return int(n)
	}
	return 0
}
func optFloatPtr(t *lua.LTable, k string) *float64 {
	if n, ok := t.RawGetString(k).(lua.LNumber); ok {
		f := float64(n)
		return &f
	}
	return nil
}
func optBool(t *lua.LTable, k string) bool {
	return lua.LVAsBool(t.RawGetString(k))
}

// tableToMap converts a Lua table to a Go map (objects) or slice (arrays).
func tableToMap(t *lua.LTable) map[string]any {
	out := map[string]any{}
	t.ForEach(func(k, v lua.LValue) {
		if ks, ok := k.(lua.LString); ok {
			out[string(ks)] = luaToGo(v)
		}
	})
	return out
}
func luaToGo(v lua.LValue) any {
	switch x := v.(type) {
	case lua.LString:
		return string(x)
	case lua.LNumber:
		return float64(x)
	case lua.LBool:
		return bool(x)
	case *lua.LTable:
		// array if 1..n contiguous, else object
		n := x.Len()
		if n > 0 {
			arr := make([]any, 0, n)
			for i := 1; i <= n; i++ {
				arr = append(arr, luaToGo(x.RawGetInt(i)))
			}
			return arr
		}
		return tableToMap(x)
	default:
		return nil
	}
}
```

Add a stub `dotenv.go` so it compiles (real impl in Task 11):

```go
package luacfg

import (
	"bufio"
	"os"
	"strings"
)

func loadDotEnv(path string) (map[string]string, error) {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return out, sc.Err()
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./internal/luacfg/ -run TestLoadModel -v`
Expected: PASS (both TestLoadModel and TestLoadModelUnknownKey).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): global table, Load entry, strict model() constructor"
```

---

## Phase 2 — Inline tool() + skill() with handle sentinels

### Task 5: skill() constructor returning a handle

**Files:** Modify `register.go`; create `internal/luacfg/skill_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="d", body="B" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "web-search" || c.Skills[0].Body != "B" {
		t.Fatalf("bad skills: %+v", c.Skills)
	}
	if len(c.Agent.Skills) != 1 || c.Agent.Skills[0] != "web-search" {
		t.Fatalf("agent skills not linked: %+v", c.Agent.Skills)
	}
}
```

- [ ] **Step 2: Run it (fails — agent skills not parsed / skill undefined)**

Run: `go test ./internal/luacfg/ -run TestLoadSkill`
Expected: FAIL.

- [ ] **Step 3: Implement skill() + handle sentinel + agent skills parsing**

Add to `register.go` `registerShell3`: `L.SetField(tbl, "skill", L.NewFunction(c.luaSkill))`.

```go
var skillKeys = map[string]bool{"name": true, "description": true, "body": true}

func (c *LoadedConfig) luaSkill(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "skill", skillKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	s := Skill{Name: optStr(opts, "name"), Description: optStr(opts, "description"), Body: optStr(opts, "body")}
	if s.Name == "" || s.Description == "" || s.Body == "" {
		L.RaiseError("skill: name, description, body all required")
	}
	c.Skills = append(c.Skills, s)
	// Return a handle table carrying a sentinel + the name.
	h := L.NewTable()
	h.RawSetString("__skill", lua.LString(s.Name))
	L.Push(h)
	return 1
}
```

Replace the stub `luaAgent` skills handling — add after prompt:

```go
	if sk, ok := opts.RawGetString("skills").(*lua.LTable); ok {
		c.Agent.Skills = handleNames(sk, "__skill")
	}
```

Add to `convert.go`:

```go
func handleNames(list *lua.LTable, sentinel string) []string {
	var out []string
	list.ForEach(func(_, v lua.LValue) {
		if ht, ok := v.(*lua.LTable); ok {
			if s, ok := ht.RawGetString(sentinel).(lua.LString); ok {
				out = append(out, string(s))
			}
		}
	})
	return out
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestLoadSkill -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): inline skill() with handle sentinel"
```

### Task 6: tool() constructor storing the handler

**Files:** Modify `register.go`; create `internal/luacfg/tool_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestLoadTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local echo = shell3.tool({
  name="echo", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={"msg"} },
  handler=function(args) return "got:"..args.msg end,
})
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ echo } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	if _, ok := c.Tools["echo"]; !ok {
		t.Fatalf("tool not registered: %+v", c.Tools)
	}
	if len(c.Agent.CustomTools) != 1 || c.Agent.CustomTools[0] != "echo" {
		t.Fatalf("agent custom tools not linked: %+v", c.Agent.CustomTools)
	}
}
```

- [ ] **Step 2: Run it (fails)**

Run: `go test ./internal/luacfg/ -run TestLoadTool`
Expected: FAIL.

- [ ] **Step 3: Implement tool() + agent tools struct parse**

Add to `registerShell3`: `L.SetField(tbl, "tool", L.NewFunction(c.luaTool))`.

```go
var toolKeys = map[string]bool{"name": true, "description": true, "parameters": true, "handler": true}

func (c *LoadedConfig) luaTool(L *lua.LState) int {
	opts := L.CheckTable(1)
	if err := checkKeys(opts, "tool", toolKeys); err != nil {
		L.RaiseError("%s", err.Error())
	}
	ct := CustomTool{Name: optStr(opts, "name"), Description: optStr(opts, "description")}
	if fn, ok := opts.RawGetString("handler").(*lua.LFunction); ok {
		ct.handler = fn
	} else {
		L.RaiseError("tool %q: handler function required", ct.Name)
	}
	if p, ok := opts.RawGetString("parameters").(*lua.LTable); ok {
		ct.Parameters = tableToMap(p)
	}
	c.Tools[ct.Name] = ct
	h := L.NewTable()
	h.RawSetString("__tool", lua.LString(ct.Name))
	L.Push(h)
	return 1
}
```

Extend `luaAgent` to parse the `tools` struct (gates + custom):

```go
	if tt, ok := opts.RawGetString("tools").(*lua.LTable); ok {
		c.Agent.Gates = ToolGates{
			Bash:             optBool(tt, "bash"),
			BashBg:           optBool(tt, "bash_bg"),
			ShellInteractive: optBool(tt, "shell_interactive"),
			Edit:             optBool(tt, "edit"),
			Memory:           optBool(tt, "memory"),
			History:          optBool(tt, "history"),
			Docs:             optBool(tt, "docs"),
		}
		if cu, ok := tt.RawGetString("custom").(*lua.LTable); ok {
			c.Agent.CustomTools = handleNames(cu, "__tool")
		}
	}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestLoadTool -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): inline tool() with handler + agent tools struct"
```

---

## Phase 3 — Handler-time bindings + .env

### Task 7: shell3.urlencode + shell3.env.secret

**Files:** Create `internal/luacfg/lua_misc.go`, `internal/luacfg/misc_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestUrlencodeAndSecret(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "API=topsecret\n")
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key=shell3.env.secret("API"), model="x" })
out = shell3.urlencode("a b&c")
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	m, _ := c.Model("m")
	if m.APIKey != "topsecret" {
		t.Fatalf("secret not resolved: %q", m.APIKey)
	}
	if got := c.L.GetGlobal("out").String(); got != "a+b%26c" {
		t.Fatalf("urlencode: %q", got)
	}
}

func TestSecretMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.env.secret("NOPE")`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), "NOPE") {
		t.Fatalf("want missing-secret error, got %v", err)
	}
}
```

- [ ] **Step 2: Run it (fails — env/urlencode unregistered)**

Run: `go test ./internal/luacfg/ -run "TestUrlencode|TestSecret"`
Expected: FAIL.

- [ ] **Step 3: Implement lua_misc.go + register**

Add to `registerShell3`:

```go
	L.SetField(tbl, "urlencode", L.NewFunction(luaURLEncode))
	env := L.NewTable()
	L.SetField(env, "secret", L.NewFunction(c.luaSecret))
	L.SetField(tbl, "env", env)
```

```go
package luacfg

import (
	"net/url"

	lua "github.com/yuin/gopher-lua"
)

func luaURLEncode(L *lua.LState) int {
	L.Push(lua.LString(url.QueryEscape(L.CheckString(1))))
	return 1
}

func (c *LoadedConfig) luaSecret(L *lua.LState) int {
	key := L.CheckString(1)
	v, ok := c.Secrets[key]
	if !ok {
		L.RaiseError("config: secret %q not found in .env", key)
	}
	L.Push(lua.LString(v))
	return 1
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run "TestUrlencode|TestSecret" -v`
Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): shell3.urlencode + shell3.env.secret"
```

### Task 8: shell3.bash binding (mutex-releasing)

**Files:** Create `internal/luacfg/lua_bash.go`, `internal/luacfg/bash_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestLuaBash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local t = shell3.tool({ name="hi", description="d",
  parameters={ type="object", properties={} },
  handler=function() local r = shell3.bash("echo hello", { timeout=5 }); return r.stdout end })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ t } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	out, err := c.CallTool(t.Context(), "hi", "{}")
	if err != nil { t.Fatal(err) }
	if out != "hello\n" {
		t.Fatalf("bash output: %q", out)
	}
}
```

(Note: `CallTool` is implemented in Task 10; this test will compile only after Task 10. Sequence Task 8 implementation, then Task 10, then run this test. To keep the failing-test-first discipline within Task 8, the first test below targets the binding via a direct Lua call.)

Replace the test above with a binding-only test for Task 8:

```go
package luacfg

import "testing"

func TestLuaBashBinding(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
r = shell3.bash("echo hello", { timeout=5 })
ok = (r.exit == 0) and (r.stdout == "hello\n")
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	if c.L.GetGlobal("ok") != lua.LTrue {
		t.Fatalf("bash binding failed: exit/stdout mismatch")
	}
}
```

(Add `import lua "github.com/yuin/gopher-lua"` to the test file.)

- [ ] **Step 2: Run it (fails — bash unregistered)**

Run: `go test ./internal/luacfg/ -run TestLuaBashBinding`
Expected: FAIL.

- [ ] **Step 3: Implement lua_bash.go + register**

Add to `registerShell3`: `L.SetField(tbl, "bash", L.NewFunction(c.luaBash))`.

```go
package luacfg

import (
	"context"
	"os/exec"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func (c *LoadedConfig) luaBash(L *lua.LState) int {
	cmd := L.CheckString(1)
	timeout := 10
	if opts, ok := L.Get(2).(*lua.LTable); ok {
		if n := optInt(opts, "timeout"); n > 0 {
			timeout = n
		}
	}
	if timeout > 600 {
		timeout = 600
	}

	// Release the VM mutex around blocking IO so other tools proceed.
	c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	var stdout, stderr []byte
	ec := exec.CommandContext(ctx, "bash", "-c", cmd)
	var so, se = &captureBuf{}, &captureBuf{}
	ec.Stdout, ec.Stderr = so, se
	runErr := ec.Run()
	cancel()
	stdout, stderr = so.b, se.b
	exit := 0
	if ee, ok := runErr.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if runErr != nil {
		exit = -1
		stderr = append(stderr, []byte(runErr.Error())...)
	}
	c.mu.Lock()

	res := L.NewTable()
	res.RawSetString("exit", lua.LNumber(exit))
	res.RawSetString("stdout", lua.LString(string(stdout)))
	res.RawSetString("stderr", lua.LString(string(stderr)))
	L.Push(res)
	return 1
}

type captureBuf struct{ b []byte }

func (c *captureBuf) Write(p []byte) (int, error) { c.b = append(c.b, p...); return len(p), nil }
```

**Important:** the mutex Unlock/Lock pairing assumes `CallTool` (Task 10) holds `c.mu` while the handler runs. For this binding-only test the handler is not running under the lock; guard the Unlock so a top-level call doesn't panic. Use a recover-safe wrapper:

```go
func (c *LoadedConfig) withIOUnlock(f func()) {
	locked := c.mu.TryLock() // returns false if already held by CallTool
	if locked {
		// not under CallTool: we just acquired it; release for IO then reacquire+release
		c.mu.Unlock()
		f()
		return
	}
	c.mu.Unlock()
	f()
	c.mu.Lock()
}
```

Wrap the blocking section of `luaBash` in `c.withIOUnlock(func(){ ... })`. (`sync.Mutex.TryLock` exists in Go 1.18+.)

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestLuaBashBinding -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): shell3.bash binding with mutex release"
```

### Task 9: shell3.http.{request,get,post}

**Files:** Create `internal/luacfg/lua_http.go`, `internal/luacfg/http_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import (
	"net/http"
	"net/http/httptest"
	"testing"

	lua "github.com/yuin/gopher-lua"
)

func TestLuaHTTPGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("BODY"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
r, err = shell3.http.get(URL, { timeout = 5 })
ok = (err == nil) and (r.status == 201) and (r.body == "BODY")
`)
	c, e := Load(dir+"/shell3.lua", dir) // err: URL global injected below
	_ = e
	// inject URL before DoFile is impossible post-hoc; instead set via env:
	t.Skip("see Step 3 note — test uses a fixed-URL variant")
}
```

Because globals can't be injected after `Load`, use a fixed test that embeds the URL via `.env`:

```go
func TestLuaHTTPGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(201); _, _ = w.Write([]byte("BODY"))
	}))
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, dir, ".env", "URL="+srv.URL+"\n")
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
local r, err = shell3.http.get(shell3.env.secret("URL"), { timeout = 5 })
ok = (err == nil) and (r.status == 201) and (r.body == "BODY")
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	if c.L.GetGlobal("ok") != lua.LTrue {
		t.Fatalf("http.get failed")
	}
}
```

- [ ] **Step 2: Run it (fails — http unregistered)**

Run: `go test ./internal/luacfg/ -run TestLuaHTTPGet`
Expected: FAIL.

- [ ] **Step 3: Implement lua_http.go + register**

Add to `registerShell3`:

```go
	httpT := L.NewTable()
	L.SetField(httpT, "request", L.NewFunction(c.luaHTTPRequest))
	L.SetField(httpT, "get", L.NewFunction(c.luaHTTPGet))
	L.SetField(httpT, "post", L.NewFunction(c.luaHTTPPost))
	L.SetField(tbl, "http", httpT)
```

```go
package luacfg

import (
	"io"
	"net/http"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func (c *LoadedConfig) luaHTTPGet(L *lua.LState) int  { return c.httpDo(L, "GET", 1, 2) }
func (c *LoadedConfig) luaHTTPPost(L *lua.LState) int { return c.httpDo(L, "POST", 1, 2) }

// shell3.http.request{ url, method, headers, body, timeout, max_bytes }
func (c *LoadedConfig) luaHTTPRequest(L *lua.LState) int {
	o := L.CheckTable(1)
	url := optStr(o, "url")
	method := optStr(o, "method")
	if method == "" { method = "GET" }
	return c.httpExec(L, method, url, o)
}

func (c *LoadedConfig) httpDo(L *lua.LState, method string, urlIdx, optIdx int) int {
	url := L.CheckString(urlIdx)
	o, _ := L.Get(optIdx).(*lua.LTable)
	if o == nil { o = L.NewTable() }
	return c.httpExec(L, method, url, o)
}

func (c *LoadedConfig) httpExec(L *lua.LState, method, url string, o *lua.LTable) int {
	timeout := optInt(o, "timeout")
	if timeout <= 0 || timeout > 120 { timeout = 30 }
	maxBytes := optInt(o, "max_bytes")
	if maxBytes <= 0 || maxBytes > 16<<20 { maxBytes = 1 << 20 }
	body := optStr(o, "body")

	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		L.Push(lua.LNil); L.Push(lua.LString("error: " + err.Error())); return 2
	}
	if h, ok := o.RawGetString("headers").(*lua.LTable); ok {
		h.ForEach(func(k, v lua.LValue) { req.Header.Set(k.String(), v.String()) })
	}

	var resp *http.Response
	c.withIOUnlock(func() {
		cl := &http.Client{Timeout: time.Duration(timeout) * time.Second}
		resp, err = cl.Do(req)
	})
	if err != nil {
		L.Push(lua.LNil); L.Push(lua.LString("error: " + err.Error())); return 2
	}
	defer resp.Body.Close()
	lr := io.LimitReader(resp.Body, int64(maxBytes)+1)
	raw, _ := io.ReadAll(lr)
	truncated := len(raw) > maxBytes
	if truncated { raw = raw[:maxBytes] }

	res := L.NewTable()
	res.RawSetString("status", lua.LNumber(resp.StatusCode))
	res.RawSetString("body", lua.LString(string(raw)))
	res.RawSetString("truncated", lua.LBool(truncated))
	hdr := L.NewTable()
	for k := range resp.Header {
		hdr.RawSetString(strings.ToLower(k), lua.LString(resp.Header.Get(k)))
	}
	res.RawSetString("headers", hdr)
	L.Push(res); L.Push(lua.LNil)
	return 2
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestLuaHTTPGet -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): shell3.http.{request,get,post}"
```

### Task 10: CallTool dispatch (handler invocation under mutex)

**Files:** Create `internal/luacfg/dispatch.go`, `internal/luacfg/dispatch_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestCallTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local echo = shell3.tool({ name="echo", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={"msg"} },
  handler=function(args) return "got:"..args.msg end })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ echo } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	out, err := c.CallTool(t.Context(), "echo", `{"msg":"hi"}`)
	if err != nil { t.Fatal(err) }
	if out != "got:hi" {
		t.Fatalf("CallTool: %q", out)
	}
}
```

- [ ] **Step 2: Run it (fails — CallTool undefined)**

Run: `go test ./internal/luacfg/ -run TestCallTool`
Expected: FAIL.

- [ ] **Step 3: Implement CallTool**

```go
package luacfg

import (
	"context"
	"encoding/json"
	"fmt"

	lua "github.com/yuin/gopher-lua"
)

// CallTool invokes a custom tool's Lua handler with JSON args, returning the
// handler's string result. Holds the VM mutex; IO bindings release it.
func (c *LoadedConfig) CallTool(ctx context.Context, name, argsJSON string) (string, error) {
	tool, ok := c.Tools[name]
	if !ok {
		return "", fmt.Errorf("unknown custom tool %q", name)
	}
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return "", fmt.Errorf("tool %q: bad args json: %w", name, err)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	argsT := goToLua(c.L, args)
	if err := c.L.CallByParam(lua.P{Fn: tool.handler, NRet: 1, Protect: true}, argsT); err != nil {
		return "", fmt.Errorf("tool %q handler: %w", name, err)
	}
	ret := c.L.Get(-1)
	c.L.Pop(1)
	return ret.String(), nil
}
```

Add `goToLua` to `convert.go`:

```go
func goToLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(x)
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case map[string]any:
		t := L.NewTable()
		for k, vv := range x {
			t.RawSetString(k, goToLua(L, vv))
		}
		return t
	case []any:
		t := L.NewTable()
		for i, vv := range x {
			t.RawSetInt(i+1, goToLua(L, vv))
		}
		return t
	default:
		return lua.LNil
	}
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestCallTool -v`
Expected: PASS.

- [ ] **Step 5: Run the deferred Task 8 end-to-end bash test now**

Re-add `TestLuaBashViaCallTool` (the first variant from Task 8) and run:
Run: `go test ./internal/luacfg/ -run TestLuaBash -v`
Expected: PASS (binding + via CallTool).

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): CallTool handler dispatch under VM mutex"
```

### Task 11: .env loader tests + redaction set

**Files:** Create `internal/luacfg/dotenv_test.go`; add `RedactionValues` to `luacfg.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "# comment\nFOO=bar\nQUOTED=\"a b\"\n\nEMPTY=\n")
	got, err := loadDotEnv(dir + "/.env")
	if err != nil { t.Fatal(err) }
	if got["FOO"] != "bar" || got["QUOTED"] != "a b" || got["EMPTY"] != "" {
		t.Fatalf("dotenv parse: %+v", got)
	}
}

func TestRedactionValues(t *testing.T) {
	c := &LoadedConfig{Secrets: map[string]string{"K": "sekret", "E": ""}}
	vals := c.RedactionValues()
	if len(vals) != 1 || vals[0] != "sekret" {
		t.Fatalf("redaction values: %+v", vals)
	}
}
```

- [ ] **Step 2: Run it (fails — RedactionValues undefined)**

Run: `go test ./internal/luacfg/ -run "TestDotEnv|TestRedaction"`
Expected: FAIL (RedactionValues).

- [ ] **Step 3: Implement RedactionValues**

Add to `luacfg.go`:

```go
// RedactionValues returns non-empty secret values, used to strip secrets from
// tool/model output before display.
func (c *LoadedConfig) RedactionValues() []string {
	var out []string
	for _, v := range c.Secrets {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run "TestDotEnv|TestRedaction" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): .env redaction set + dotenv tests"
```

---

## Phase 4 — Guard middleware chain

### Task 12: Decision type + guards() registration + Lua-fn chain

**Files:** Modify `register.go`; create `internal/luacfg/guards.go`, `internal/luacfg/guard_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestGuardChainBlocks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local g = {
  function(call)
    if call.tool == "edit_file" then return { action="block", reason="no edits" } end
    return { action="allow" }
  end,
}
shell3.agent({ name="a", model="m", prompt="p", tools={ edit=true }, on_tool_call=g })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil { t.Fatal(err) }
	defer c.Close()
	d, reason, err := c.OnToolCall(t.Context(), "edit_file", map[string]any{"file_path": "x"})
	if err != nil { t.Fatal(err) }
	if d != DecisionBlock || reason != "no edits" {
		t.Fatalf("guard: d=%v reason=%q", d, reason)
	}
	d2, _, _ := c.OnToolCall(t.Context(), "bash", map[string]any{"command": "ls"})
	if d2 != DecisionAllow {
		t.Fatalf("guard should allow bash, got %v", d2)
	}
}
```

- [ ] **Step 2: Run it (fails — OnToolCall/DecisionBlock undefined)**

Run: `go test ./internal/luacfg/ -run TestGuardChain`
Expected: FAIL.

- [ ] **Step 3: Implement Decision + agent on_tool_call parse + OnToolCall**

Add to `register.go` `luaAgent`:

```go
	if g, ok := opts.RawGetString("on_tool_call").(*lua.LTable); ok {
		g.ForEach(func(_, v lua.LValue) {
			switch x := v.(type) {
			case *lua.LFunction:
				c.Agent.Guard = append(c.Agent.Guard, GuardEntry{fn: x})
			case *lua.LTable:
				if b, ok := x.RawGetString("__guard").(lua.LString); ok {
					c.Agent.Guard = append(c.Agent.Guard, GuardEntry{
						Builtin: string(b), prompt: optBool(x, "prompt"),
					})
				}
			}
		})
	}
```

Add `dispatch.go` (extend) / new section:

```go
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionBlock
	DecisionCancel
)

// OnToolCall runs the guard chain in order; first non-allow short-circuits.
func (c *LoadedConfig) OnToolCall(ctx context.Context, tool string, params map[string]any) (Decision, string, error) {
	for _, g := range c.Agent.Guard {
		var d Decision
		var reason string
		var err error
		if g.Builtin != "" {
			d, reason = runBuiltinGuard(g, tool, params)
		} else {
			d, reason, err = c.runLuaGuard(ctx, g.fn, tool, params)
		}
		if err != nil {
			return DecisionAllow, "", err
		}
		if d != DecisionAllow {
			return d, reason, nil
		}
	}
	return DecisionAllow, "", nil
}

func (c *LoadedConfig) runLuaGuard(ctx context.Context, fn *lua.LFunction, tool string, params map[string]any) (Decision, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.L.SetContext(ctx)
	call := c.L.NewTable()
	call.RawSetString("tool", lua.LString(tool))
	call.RawSetString("params", goToLua(c.L, anyMap(params)))
	if err := c.L.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, call); err != nil {
		return DecisionAllow, "", err
	}
	ret := c.L.Get(-1)
	c.L.Pop(1)
	rt, ok := ret.(*lua.LTable)
	if !ok {
		return DecisionAllow, "", nil
	}
	return parseAction(optStr(rt, "action")), optStr(rt, "reason"), nil
}

func parseAction(s string) Decision {
	switch s {
	case "block":
		return DecisionBlock
	case "cancel":
		return DecisionCancel
	default:
		return DecisionAllow
	}
}

func anyMap(m map[string]any) map[string]any { if m == nil { return map[string]any{} }; return m }
```

Add `guards.go`:

```go
package luacfg

import lua "github.com/yuin/gopher-lua"

func registerGuards(c *LoadedConfig, tbl *lua.LTable) {
	L := c.L
	g := L.NewTable()
	L.SetField(g, "confirm_dangerous", L.NewFunction(func(L *lua.LState) int {
		prompt := false
		if o, ok := L.Get(1).(*lua.LTable); ok {
			prompt = optBool(o, "prompt")
		}
		h := L.NewTable()
		h.RawSetString("__guard", lua.LString("confirm_dangerous"))
		h.RawSetString("prompt", lua.LBool(prompt))
		L.Push(h)
		return 1
	}))
	L.SetField(tbl, "guards", g)
}
```

Call `registerGuards(c, tbl)` at the end of `registerShell3`.

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestGuardChain -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): on_tool_call guard chain + guards.confirm_dangerous handle"
```

### Task 13: confirm_dangerous denylist in Go

**Files:** Modify `guards.go`; create `internal/luacfg/dangerous_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import "testing"

func TestConfirmDangerous(t *testing.T) {
	cases := map[string]Decision{
		"ls -la":              DecisionAllow,
		"rm -rf /tmp/x":       DecisionBlock,
		"sudo reboot":         DecisionBlock,
		"git push --force":    DecisionBlock,
		"echo hi":             DecisionAllow,
	}
	g := GuardEntry{Builtin: "confirm_dangerous", prompt: false}
	for cmd, want := range cases {
		d, _ := runBuiltinGuard(g, "bash", map[string]any{"command": cmd})
		if d != want {
			t.Errorf("%q: got %v want %v", cmd, d, want)
		}
	}
	// non-shell tools always allowed
	if d, _ := runBuiltinGuard(g, "edit_file", map[string]any{}); d != DecisionAllow {
		t.Errorf("edit_file should be allowed by confirm_dangerous")
	}
}
```

- [ ] **Step 2: Run it (fails — runBuiltinGuard undefined)**

Run: `go test ./internal/luacfg/ -run TestConfirmDangerous`
Expected: FAIL.

- [ ] **Step 3: Implement runBuiltinGuard with the denylist**

Add to `guards.go` (port the ERE patterns from the old `confirm-bash.sh`; `prompt=false` blocks on match — interactive prompting is wired in Task 17 via the TUI releaser):

```go
import (
	"regexp"
)

var dangerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(^|[ \t;|&])rm[ \t]`),
	regexp.MustCompile(`(^|[ \t;|&])rmdir[ \t]`),
	regexp.MustCompile(`(^|[ \t;|&])shred([ \t]|$)`),
	regexp.MustCompile(`(^|[ \t;|&])dd[ \t].*[ \t]of=`),
	regexp.MustCompile(`(^|[ \t;|&])mkfs(\.[a-z0-9]+)?([ \t]|$)`),
	regexp.MustCompile(`(^|[ \t;|&])sudo([ \t]|$)`),
	regexp.MustCompile(`(^|[ \t;|&])su[ \t]`),
	regexp.MustCompile(`(^|[ \t;|&])chmod[ \t]+-R`),
	regexp.MustCompile(`(^|[ \t;|&])chown[ \t]+-R`),
	regexp.MustCompile(`(^|[ \t;|&])git[ \t]+push[ \t].*(--force|-f([ \t]|$)|--mirror|--delete)`),
	regexp.MustCompile(`(^|[ \t;|&])git[ \t]+reset[ \t]+--hard`),
	regexp.MustCompile(`(^|[ \t;|&])git[ \t]+clean[ \t]+-[a-zA-Z]*[fF]`),
	regexp.MustCompile(`(curl|wget)[ \t][^|]*\|[ \t]*(sudo[ \t]+)?(bash|sh|zsh)([ \t]|$)`),
	regexp.MustCompile(`(^|[ \t;|&])shutdown([ \t]|$)`),
	regexp.MustCompile(`(^|[ \t;|&])reboot([ \t]|$)`),
	regexp.MustCompile(`DROP[ \t]+(TABLE|DATABASE|SCHEMA|INDEX)`),
	regexp.MustCompile(`TRUNCATE[ \t]+TABLE`),
}

func runBuiltinGuard(g GuardEntry, tool string, params map[string]any) (Decision, string) {
	if g.Builtin != "confirm_dangerous" {
		return DecisionAllow, ""
	}
	switch tool {
	case "bash", "bash_bg", "shell_interactive":
	default:
		return DecisionAllow, ""
	}
	cmd, _ := params["command"].(string)
	for _, re := range dangerPatterns {
		if re.MatchString(cmd) {
			// prompt handling (interactive approve/deny) is layered in Task 17;
			// default policy blocks the dangerous call.
			return DecisionBlock, "blocked dangerous command (confirm_dangerous guard)"
		}
	}
	return DecisionAllow, ""
}
```

(The full denylist from `hooks/confirm-bash.sh` should be ported here verbatim — the subset above is the failing-test target; the executor copies the remaining patterns from `~/.shell3/hooks/confirm-bash.sh` into this slice.)

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestConfirmDangerous -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): confirm_dangerous denylist in Go"
```

---

## Phase 5 — Persona assembly + openai extra plumbing

### Task 14: BuildPersona (verbatim prompt + standard blocks)

**Files:** Create `internal/luacfg/persona.go`, `internal/luacfg/persona_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package luacfg

import (
	"strings"
	"testing"
)

func TestBuildPersonaSystemPrompt(t *testing.T) {
	c := &LoadedConfig{
		Agent:  Agent{Name: "base", Prompt: "You are base.", Skills: []string{"web-search"}},
		Skills: []Skill{{Name: "web-search", Description: "search the web", Body: "..."}},
	}
	rd := RuntimeData{Time: "Mon Jun 1", CWD: "/work", Model: "m-1"}
	sp := c.BuildPersona(rd)
	for _, want := range []string{"You are base.", "/work", "m-1", "web-search", "search the web"} {
		if !strings.Contains(sp, want) {
			t.Fatalf("system prompt missing %q:\n%s", want, sp)
		}
	}
}
```

- [ ] **Step 2: Run it (fails — BuildPersona undefined)**

Run: `go test ./internal/luacfg/ -run TestBuildPersona`
Expected: FAIL.

- [ ] **Step 3: Implement RuntimeData + BuildPersona**

```go
package luacfg

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/store"
)

type RuntimeData struct {
	Time, CWD, Model string
	CoreMemories     []store.MemoryEntry
}

// BuildPersona renders the final system prompt: the verbatim agent prompt
// followed by engine-injected standard blocks. Replaces text/template.
func (c *LoadedConfig) BuildPersona(rd RuntimeData) string {
	var b strings.Builder
	b.WriteString(c.Agent.Prompt)
	fmt.Fprintf(&b, "\n\n## Environment\n- Workdir: %s\n- Model: %s\n- Time: %s\n", rd.CWD, rd.Model, rd.Time)
	if len(rd.CoreMemories) > 0 {
		b.WriteString("\n## Core memories\n")
		for _, m := range rd.CoreMemories {
			fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
		}
	}
	if len(c.Agent.Skills) > 0 {
		b.WriteString("\n## Skills\nRead a skill body with the `skill` tool when it applies.\n")
		for _, name := range c.Agent.Skills {
			for _, s := range c.Skills {
				if s.Name == name {
					fmt.Fprintf(&b, "- %s: %s\n", s.Name, s.Description)
				}
			}
		}
	}
	return b.String()
}
```

(Verify `store.MemoryEntry` field names with `grep -n "type MemoryEntry" internal/store/*.go`; adjust `.Key`/`.Value` if different.)

- [ ] **Step 4: Run test to verify pass**

Run: `go test ./internal/luacfg/ -run TestBuildPersona -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/luacfg/
git commit -m "feat(luacfg): BuildPersona system-prompt assembly"
```

### Task 15: Plumb model `extra` into the openai adapter

**Files:** Modify `internal/adapter/openai/client.go`; add test `internal/adapter/openai/extra_test.go`.

- [ ] **Step 1: Inspect the SDK extra-fields mechanism**

Run: `grep -rn "SetExtraFields\|WithJSONSet\|ExtraFields\|extra" internal/adapter/openai/*.go`
Expected: identify how request params are built in `Stream` (around client.go:200-230). Note the openai-go option for arbitrary JSON fields (`option.WithJSONSet`).

- [ ] **Step 2: Write the failing test**

```go
package openai

import "testing"

func TestSetExtra(t *testing.T) {
	c := NewClient("https://x/v1", "k", "m")
	c.SetExtra(map[string]any{"verbosity": "high"})
	if c.extra["verbosity"] != "high" {
		t.Fatalf("extra not stored: %+v", c.extra)
	}
}
```

- [ ] **Step 3: Run it (fails — SetExtra undefined)**

Run: `go test ./internal/adapter/openai/ -run TestSetExtra`
Expected: FAIL.

- [ ] **Step 4: Add the field + setter + merge into requests**

In `client.go`, add `extra map[string]any` to the `Client` struct, a setter:

```go
func (c *Client) SetExtra(m map[string]any) { c.extra = m }
```

and, in `Stream`, apply each extra field as a request option (using the openai-go option for raw JSON, e.g. `opts = append(opts, option.WithJSONSet(k, v))` for each `k,v` in `c.extra`). Match the exact option import already used in the file.

- [ ] **Step 5: Run test to verify pass**

Run: `go test ./internal/adapter/openai/ -run TestSetExtra -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/adapter/openai/
git commit -m "feat(openai): SetExtra plumbs model.extra into requests"
```

---

## Phase 6 — pkg/chat integration: custom-tool dispatch + guard runner

### Task 16: chat.Config custom-tool dispatcher + guard runner fields

**Files:** Modify `pkg/chat/chat.go`, `pkg/chat/turn.go`, `pkg/chat/tools.go`; add `pkg/chat/luatool_test.go`.

- [ ] **Step 1: Write the failing test**

```go
package chat

import (
	"context"
	"testing"
)

func TestCustomToolDispatcher(t *testing.T) {
	called := ""
	cfg := Config{
		CustomTool: func(_ context.Context, name, args string) (string, error) {
			called = name + ":" + args
			return "ok", nil
		},
	}
	out := dispatchCustomTool(context.Background(), cfg, "echo", `{"a":1}`)
	if out != "ok" || called != `echo:{"a":1}` {
		t.Fatalf("dispatch: out=%q called=%q", out, called)
	}
}
```

- [ ] **Step 2: Run it (fails)**

Run: `go test ./pkg/chat/ -run TestCustomToolDispatcher`
Expected: FAIL.

- [ ] **Step 3: Add fields + dispatcher; rewire turn.go**

In `pkg/chat/chat.go` `Config`, add:

```go
	// CustomTool dispatches a custom (Lua-handler) tool call. Nil = none.
	CustomTool func(ctx context.Context, name, argsJSON string) (string, error)
	// ToolGuard runs the on_tool_call chain. Nil = allow all.
	ToolGuard func(ctx context.Context, tool string, params map[string]any) (guardDecision int, reason string, err error)
```

Remove the `UserTools map[string]usertools.Tool` and `Secrets`/`Hooks` fields that are now obsolete (kept only if still referenced; see Task 19 cleanup). Add to `pkg/chat/tools.go`:

```go
func dispatchCustomTool(ctx context.Context, cfg Config, name, rawArgs string) string {
	if cfg.CustomTool == nil {
		return fmt.Sprintf("error: unknown tool %q", name)
	}
	out, err := cfg.CustomTool(ctx, name, rawArgs)
	if err != nil {
		return "error: " + err.Error()
	}
	return out
}
```

In `turn.go`: replace the `cfg.Hooks.OnToolCall(...)` call (line ~167) with:

```go
	var decision int
	var hookReason string
	var hookErr error
	if cfg.ToolGuard != nil {
		decision, hookReason, hookErr = cfg.ToolGuard(ctx, tc.Name, parseRawArgs(tc.RawArgs))
	}
```

and map `decision` (0 allow / 1 block / 2 cancel) onto the existing cancel/deny/allow branches. Replace the `cfg.UserTools[tc.Name]` branch (line ~203) with `dispatchCustomTool(ctx, cfg, tc.Name, tc.RawArgs)` gated on the tool being a known custom tool name (pass the set via `cfg.CustomToolNames map[string]bool` or check `cfg.CustomTool != nil` and fall through to handler map otherwise — implement by checking a `cfg.CustomToolNames` set added alongside `CustomTool`).

- [ ] **Step 4: Run test + full package**

Run: `go test ./pkg/chat/ -run TestCustomToolDispatcher -v && go build ./pkg/chat/`
Expected: PASS + build (after adjusting references to removed fields).

- [ ] **Step 5: Commit**

```bash
git add pkg/chat/
git commit -m "feat(chat): custom-tool dispatcher + guard runner hooks (replace hooks.Runner)"
```

### Task 17: Guard decision constants shared with luacfg

**Files:** Modify `pkg/chat/turn.go` mapping; ensure `luacfg.Decision` values (0/1/2) match chat's expectation.

- [ ] **Step 1: Add a mapping test**

```go
package chat

import "testing"

func TestGuardDecisionConstants(t *testing.T) {
	// Document the contract: 0=allow,1=block,2=cancel — matches luacfg.Decision.
	if guardAllow != 0 || guardBlock != 1 || guardCancel != 2 {
		t.Fatal("guard decision constants drifted from luacfg.Decision")
	}
}
```

- [ ] **Step 2: Run (fails — constants undefined)**

Run: `go test ./pkg/chat/ -run TestGuardDecisionConstants`
Expected: FAIL.

- [ ] **Step 3: Define constants in pkg/chat**

```go
const (
	guardAllow  = 0
	guardBlock  = 1
	guardCancel = 2
)
```

Use them in the `turn.go` decision mapping.

- [ ] **Step 4: Run + commit**

Run: `go test ./pkg/chat/ -run TestGuardDecisionConstants -v`
Expected: PASS.

```bash
git add pkg/chat/ && git commit -m "feat(chat): guard decision constants matching luacfg.Decision"
```

---

## Phase 7 — Bootstrap rewrite, deletions, example, cleanup gate

### Task 18: Rewrite run.go to load shell3.lua

**Files:** Modify `cmd/shell3/run.go`.

- [ ] **Step 1: Replace flag set + path resolution**

Replace `--persona/--provider/--model/--no-bash/--no-memory-tools` with a positional `path` arg defaulting to `./shell3.lua` then `~/.shell3/shell3.lua`. Keep `--out`. Set `workdir = filepath.Dir(path)`.

- [ ] **Step 2: Replace load/resolve/build phases**

Replace `config.LoadAuthStore`, `secrets.Load`, `persona.ParseConfig/Load`, `usertools.LoadAll`, the `buildClient` closure, and `hooks.NewRunner` with:

```go
	lc, err := luacfg.Load(path, workdir)
	if err != nil {
		return err
	}
	defer lc.Close()

	m, _ := lc.Model(lc.Agent.ModelName)
	client := openai.NewClient(m.BaseURL, m.APIKey, m.ModelID)
	client.SetParams(llm.RequestParams{
		ReasoningEffort: m.Reasoning, MaxTokens: m.MaxTokens, Temperature: m.Temperature,
	})
	if m.Extra != nil {
		client.SetExtra(m.Extra)
	}
```

Build the system prompt via `lc.BuildPersona(luacfg.RuntimeData{Time:…, CWD: workdir, Model: m.ModelID, CoreMemories: coreMemories})`. Build the tool-definition list from `lc.Agent.Gates` (built-ins) + `lc.Tools` (custom), assemble `chat.Config` with `CustomTool: lc.CallTool`, `CustomToolNames`, `ToolGuard: func(ctx,t,p)(int,string,error){ d,r,e := lc.OnToolCall(ctx,t,p); return int(d),r,e }`, and the redaction values.

(The tool-definition assembly that previously lived in `persona.Load` moves into a small `cmd/shell3` helper or `luacfg.ToolDefs(gates, customTools) []llm.ToolDefinition`. Add `luacfg.ToolDefs` with the built-in `llm.ToolDefinition` values — port the tool schemas currently in `pkg/persona/persona.go` var blocks into `luacfg`.)

- [ ] **Step 3: Build**

Run: `go build ./cmd/shell3/`
Expected: compile errors only from not-yet-deleted imports — resolved in Task 19. Iterate until run.go itself is consistent.

- [ ] **Step 4: Commit**

```bash
git add cmd/shell3/run.go internal/luacfg/
git commit -m "feat(cmd): run.go loads shell3.lua via luacfg"
```

### Task 19: Delete replaced packages + fix references

**Files:** Delete dirs/files; modify `cmd/shell3/main.go`, `cmd/shell3/doctor.go`, `pkg/persona/persona.go`, `pkg/shell3/shell3.go`.

- [ ] **Step 1: Delete**

```bash
git rm -r internal/adapter/anthropic internal/config internal/secrets internal/usertools pkg/hooks
git rm cmd/shell3/auth.go cmd/shell3/secrets.go
```

- [ ] **Step 2: Fix main.go**

Remove the anthropic blank import; remove `newAuthCommand()` and `newSecretsCommand()` registrations.

- [ ] **Step 3: Reduce pkg/persona to the carrier types**

Delete `ParseConfig`, `Load`, `extractParts`, `PersonaConfig`, `PersonaParams`, the YAML import, and the tool `var` blocks moved to `luacfg`. Keep only what other code still imports (likely nothing — if so, delete the package and drop imports). Verify with `grep -rn "pkg/persona" --include=*.go .`.

- [ ] **Step 4: Rework doctor.go**

Replace `config.LoadAuthStore`/`secrets.Load` checks with: resolve the shell3.lua path, call `luacfg.Load`, report success/failure and the model count + agent name.

- [ ] **Step 5: Fix pkg/shell3/shell3.go**

It imports `internal/config`. Repoint `shell3.New` to build its `chat.Config` from a `luacfg.LoadedConfig` (add a `Path` field to `Options`, default `~/.shell3/shell3.lua`), or mark the embed API as updated. Update `examples/webui/main.go` accordingly.

- [ ] **Step 6: Build the whole repo**

Run: `go build ./...`
Expected: exit 0. Fix every remaining reference to deleted packages.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: delete YAML/markdown/anthropic config paths (clean lateral move)"
```

### Task 20: Ship the example config

**Files:** Create `shell3-example.lua`, `shell3-example.env.example`.

- [ ] **Step 1: Write the example**

Use the converged design from the spec: one `shell3.model("main", …)`, the `web_fetch` (http) and `brave_search` (bash+jq) tools, the five inline skills (paste the verbatim bodies from `~/.shell3/skills/*.md`), the guard chain (`shell3.guards.confirm_dangerous{prompt=true}` + a custom middleware), and the `shell3.agent` with the separated bash tool gates. `shell3-example.env.example` lists `OPENCODE_KEY`, `BRAVE_API_KEY`.

- [ ] **Step 2: Golden-parse test**

Create `internal/luacfg/example_test.go`:

```go
package luacfg

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestExampleParses(t *testing.T) {
	_, thisFile, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(thisFile), "..", "..")
	c, err := Load(filepath.Join(root, "shell3-example.lua"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Agent.Name == "" || len(c.Models) == 0 {
		t.Fatal("example produced empty config")
	}
}
```

(The example references secrets; create a sibling `.env` in `root` for the test via `t` setup, or guard `shell3.env.secret` calls — simplest: have the test write a temp `.env` next to a copy. Adjust: copy `shell3-example.lua` into `t.TempDir()` with a generated `.env` defining `OPENCODE_KEY`/`BRAVE_API_KEY`, then `Load` from there.)

- [ ] **Step 3: Run**

Run: `go test ./internal/luacfg/ -run TestExampleParses -v`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add shell3-example.lua shell3-example.env.example internal/luacfg/example_test.go
git commit -m "docs: ship shell3-example.lua + golden-parse test"
```

### Task 21: Cleanup gate — prove no stale code

**Files:** none (verification).

- [ ] **Step 1: No references to deleted packages**

Run: `grep -rn "internal/config\|internal/secrets\|internal/usertools\|pkg/hooks\|adapter/anthropic" --include=*.go . ; echo "exit=$?"`
Expected: no matches (grep exit 1). Any match = a stale reference to fix.

- [ ] **Step 2: No seagull/telegram anywhere in repo**

Run: `grep -rin "seagull\|telegram\|gateway" --include=*.go . ; echo "exit=$?"`
Expected: no matches.

- [ ] **Step 3: No leftover YAML/markdown config loaders**

Run: `grep -rn "ai-do-not-read\|ParseConfig\|LoadAuthStore\|frontmatter\|text/template" --include=*.go . ; echo "exit=$?"`
Expected: no matches (text/template only if some unrelated code uses it — verify each hit is unrelated).

- [ ] **Step 4: Vet + full test suite**

Run: `go vet ./... && go test ./...`
Expected: vet clean, all tests pass.

- [ ] **Step 5: Manual smoke**

Run: `cp shell3-example.lua /tmp/s.lua && cp shell3-example.env.example /tmp/.env` then edit `/tmp/.env` with a real key, then `go run ./cmd/shell3 /tmp/s.lua` and send one message.
Expected: a normal coding-agent turn, identical UX to before; `shell3 doctor` reports the Lua config loads.

- [ ] **Step 6: Commit (if any fixes) + open PR**

```bash
git add -A && git commit -m "test: cleanup gate — no stale config code"
git push -u origin feat/lua-config
gh pr create --title "Lua config rework (part 1)" --body "Strict single-file shell3.lua + .env replacing YAML/markdown/anthropic config. Clean lateral move per docs/superpowers/specs/2026-06-01-lua-config-rework-design.md."
```

---

## Self-Review

**Spec coverage:** §3.1 env.secret → Task 7; §3.2 model() + extra → Tasks 4, 15; §3.3 tool() → Task 6, dispatch Task 10; §3.4 skill() → Task 5; §3.5 guard chain + confirm_dangerous → Tasks 12, 13; §3.6 agent + gates → Tasks 4/6/12; §3.7 strict keys → Task 3 (applied in every constructor); §4 http/bash/urlencode bindings → Tasks 7–9; §5 tool gating → Task 18 (`ToolDefs`); §6 prompt blocks → Task 14; §7 architecture (luacfg, gopher-lua, LState lifetime) → Phases 1–4; §8 cleanup ledger → Tasks 15/19/21; §9 migration/example → Task 20; §10 testing → tests in every task + Task 21 gate. All spec sections map to tasks.

**Placeholder scan:** Task 13 explicitly defers the *full* denylist to a verbatim copy step (patterns enumerated, source file named) — not a placeholder but a copy instruction. Task 18/19 note iterative compile-fix loops (inherent to a deletion that spans call sites). No "TBD/handle edge cases/similar to Task N" left.

**Type consistency:** `LoadedConfig`, `Model.ModelID` (not `Model`), `ToolGates`, `Decision`(0/1/2) ↔ `guardAllow/Block/Cancel`(0/1/2), `CallTool(ctx,name,argsJSON)`, `OnToolCall(ctx,tool,params)→(Decision,string,error)`, `BuildPersona(RuntimeData)`, `SetExtra(map[string]any)` — names used identically across Tasks 2–20. The Task 8 test ordering caveat (binding test now, end-to-end after Task 10) is called out inline.

**Known integration risks to watch during execution:** (1) `withIOUnlock` mutex choreography under `CallTool` — verify no double-unlock; (2) openai-go's exact extra-fields option name (Task 15 Step 1 inspects it first); (3) `pkg/shell3` embed API + `examples/webui` must be repointed (Task 19 Step 5), or the build gate (Task 21) fails.
