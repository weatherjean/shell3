# File-backed Skills + Bash-template Tools — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove two layers of Lua/tool indirection from shell3 — make skills `.md` files the agent `cat`s (deleting the `skill` tool), and make custom tools declarative bash-command templates run with an injected env (deleting Lua `handler`s and the `shell3.bash`/`http`/`urlencode` helpers).

**Architecture:** Two independent phases. **Phase 1 (skills)** swaps `Skill.Body`/`BodyCmd` for a file `Path`, validated at load, indexed by absolute path in the system prompt; removes the `skill` tool def + dispatch. **Phase 2 (tools)** replaces the per-tool Lua `handler` with a `command` string plus `secrets`/`background`/`timeout`; resolution (args→env, secrets→env) lives in `luacfg`, execution moves to `internal/chat` (which owns `WorkDir`/`SinkPath`), reusing the bash-tool runner and `bgjobs.Start`.

**Tech Stack:** Go, gopher-lua (`internal/luacfg`), `internal/chat` tool loop, `internal/bgjobs` + `internal/sink`, `internal/scaffold` embedded defaults.

**Spec:** `docs/dev/superpowers/specs/2026-06-11-bash-first-tools-and-skills-design.md`

**Conventions:** Branch `feat/bash-first`. After every task: `go build ./... && go test ./... && gofmt -l . && go vet ./...` must be clean. Commit trailer on every commit:
`Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. Do NOT push/merge.

---

## File Structure

**Phase 1 (skills)**
- `internal/luacfg/luacfg.go` — `Skill` struct: `Body`/`BodyCmd` → `Path`; Load resolves+validates skill paths.
- `internal/luacfg/register.go` — `skillKeys` (`path`, drop `body`/`body_cmd`); `luaSkill` validation.
- `internal/luacfg/persona.go` — `## Skills` index renders absolute paths + "cat" guidance.
- `internal/luacfg/tooldefs.go` — delete `skillTool`; `ToolDefs` drops the `hasSkills` param.
- `internal/luacfg/cmdbody_test.go` — repoint the shared `runBodyCmd` tests (failing/empty/cwd/reload) onto agent `prompt_cmd`; delete skill-`body_cmd` tests.
- `internal/luacfg/skillpath_test.go` (new) — skill `path` resolution/validation/index.
- `internal/agentsetup/agentsetup.go` — `ToolDefs` call; drop `customNames["skill"]`.
- `internal/scaffold/defaults/base/lib/skills/*.md` (new) + `*.lua` (rewritten to `path=`).
- `internal/scaffold/defaults/base/shell3.lua.tmpl` — fix the Phase-9 `body_cmd` skill comment → `path`.
- `CLAUDE.md`, design docs — skill mechanism wording.

**Phase 2 (tools)**
- `internal/luacfg/luacfg.go` — `CustomTool`: drop `handler`; add `Command`/`Secrets`/`Background`/`Timeout`.
- `internal/luacfg/register.go` — `toolKeys` (`command`/`secrets`/`background`/`timeout`, drop `handler`); param-name lowercase validation.
- `internal/luacfg/customtool.go` (new) — `ResolvedCall` + `ResolveCustomCall` (args→env, secrets→env, declared-param filtering).
- `internal/luacfg/dispatch.go` — delete `CallTool` + `goToLua`.
- `internal/luacfg/lua_bash.go` — delete `luaBash`; keep `WrapBash`. `internal/luacfg/lua_http.go` — delete. `internal/luacfg/lua_misc.go` — delete `luaURLEncode`.
- `internal/luacfg/register.go` (`registerShell3`) — unregister `bash`/`http`/`urlencode`.
- `internal/chat/toolhandler.go`, `chat.go` — `Config`/`TurnConfig`: drop `CustomTool`; add `ResolveCustomTool` + `StubTools`; new `ResolvedTool` type.
- `internal/chat/handler_bash.go` — extract `runBashCapture` (env-aware, exit-aware), reused by `BashHandler`.
- `internal/chat/tools.go` — rewrite `dispatchCustomTool` (resolve → fg/bg exec).
- `internal/chat/turn.go` — route stubs via `cfg.StubTools`; pass `cfg` to `dispatchCustomTool`.
- `internal/bgjobs/bgjobs.go` — `Start` gains an `env []string` param.
- `internal/agentsetup/agentsetup.go` — `Parts.ResolveCustomTool`; wire `ResolveCustomTool`/`StubTools`; drop `Parts.CustomTool` + stub→`customNames`.
- `internal/scaffold/defaults/base/lib/tools.lua` — `web_fetch`/`brave_search` as command templates.
- Tests + docs.

---

# PHASE 1 — File-backed skills

### Task 1: Skill `path` (struct, Lua surface, load validation)

**Files:**
- Modify: `internal/luacfg/luacfg.go` (the `Skill` struct ~line 40; the skill resolution loop in `Load`)
- Modify: `internal/luacfg/register.go` (`skillKeys` ~line 145; `luaSkill` ~line 147)
- Test: `internal/luacfg/skillpath_test.go` (new)
- Modify: `internal/luacfg/cmdbody_test.go` (repoint shared-runner tests)

- [ ] **Step 1: Write failing tests** — create `internal/luacfg/skillpath_test.go`:

```go
package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSkillConfig writes shell3.lua plus sibling files into one temp dir and
// returns the config path. files maps a relative path (under the config dir) to
// its contents.
func writeSkillConfig(t *testing.T, lua string, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

const skillHdr = `shell3.model("m", { base_url="http://x", api_key="k", model="id" })` + "\n"

func TestSkillPathResolvesToAbs(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
local h = shell3.skill({ name="history", description="d", path="lib/skills/history.md" })
shell3.agent({ name="code", model="m", prompt="p", skills={ h } })
`, map[string]string{"lib/skills/history.md": "the history body\n"})
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	want := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if c.Skills[0].Path != want {
		t.Fatalf("Path = %q, want abs %q", c.Skills[0].Path, want)
	}
}

func TestSkillMissingPathFileErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d", path="lib/skills/nope.md" })
`, nil)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("missing skill file should fail Load")
	}
}

func TestSkillEmptyPathFileErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d", path="empty.md" })
`, map[string]string{"empty.md": "   \n"})
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("empty skill file should fail Load")
	}
}

func TestSkillNoPathErrors(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
shell3.skill({ name="x", description="d" })
`, nil)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("skill with no path should error")
	}
}
```

- [ ] **Step 2: Run, verify they fail to compile/fail**

Run: `go test ./internal/luacfg/ -run TestSkill -count=1`
Expected: FAIL (compile error: `Skills[0].Path` undefined / `body` still required).

- [ ] **Step 3: Change the `Skill` struct** in `internal/luacfg/luacfg.go` (replace the existing `Skill` line):

```go
// Skill is a granted capability surfaced as a one-line entry in the agent's
// ## Skills index (name + description). Body lives in the file at Path; the
// agent reads it with `cat` when the skill applies. Path is stored relative as
// declared, then rewritten to an absolute path during Load (see the skill
// resolution loop) so the index can point the agent at it from any cwd.
type Skill struct{ Name, Description, Path string }
```

- [ ] **Step 4: Update `skillKeys` + `luaSkill`** in `internal/luacfg/register.go`:

Replace `var skillKeys = ...` with:
```go
var skillKeys = map[string]bool{"name": true, "description": true, "path": true}
```
Replace the body of `luaSkill` (from `s := Skill{...}` through the `c.Skills = append` line, keeping the handle-return tail) with:
```go
	s := Skill{Name: optStr(opts, "name"), Description: optStr(opts, "description"), Path: optStr(opts, "path")}
	if s.Name == "" || s.Description == "" || s.Path == "" {
		L.RaiseError("skill: name, description, and path are all required")
	}
	c.Skills = append(c.Skills, s)
```

- [ ] **Step 5: Replace the skill resolution loop in `Load`** (`internal/luacfg/luacfg.go`). Find the existing skill `BodyCmd` loop (added in Phase 9, the `for i := range c.Skills { if c.Skills[i].BodyCmd == "" ...` block) and replace it with path resolution:

```go
	// Resolve + validate skill file paths ONCE (fail closed). Each path is
	// resolved relative to the config dir (cfgDir, set above for prompt_cmd) and
	// must be a readable, non-empty regular file. A broken skill path is caught
	// here at load/reload, never at turn time. The resolved absolute path is
	// stored back so the ## Skills index can point the agent at it (BuildPersonaFor).
	for i := range c.Skills {
		path := c.Skills[i].Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(cfgDir, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("config: skill %q path %q: %w", c.Skills[i].Name, c.Skills[i].Path, err)
		}
		if info.IsDir() || info.Size() == 0 {
			return nil, fmt.Errorf("config: skill %q path %q: not a non-empty file", c.Skills[i].Name, c.Skills[i].Path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("config: skill %q path %q: %w", c.Skills[i].Name, c.Skills[i].Path, err)
		}
		if len(strings.TrimSpace(string(data))) == 0 {
			return nil, fmt.Errorf("config: skill %q path %q: file is empty", c.Skills[i].Name, c.Skills[i].Path)
		}
		c.Skills[i].Path = path
	}
```

Ensure `internal/luacfg/luacfg.go` imports `os` and `strings` (add to the import block if missing). `cfgDir` is the `filepath.Dir(path)` value the Phase-9 prompt-resolution code already computes near the top of the resolution section; if it is scoped above the deleted loop, keep that declaration. If not present, add `cfgDir := filepath.Dir(path)` before this loop.

- [ ] **Step 6: Repoint the shared-runner tests in `cmdbody_test.go`.** The `runBodyCmd` failing/empty/cwd/reload tests currently exercise it through a skill `body_cmd`; skills no longer use it (agents/subagents still do via `prompt_cmd`). Edit `internal/luacfg/cmdbody_test.go`:
  - **Delete** these skill-only tests: `TestSkillBodyCmdResolves`, `TestSkillBothBodyAndBodyCmdErrors`, `TestSkillNeitherBodyNorBodyCmdErrors`.
  - **Rewrite** `TestBodyCmdFailingCommandErrors`, `TestBodyCmdEmptyOutputErrors`, `TestBodyCmdCwdIsConfigDir`, `TestBodyCmdReResolvesOnReload` to use an **agent `prompt_cmd`** instead of a skill `body_cmd`. Example for the failing-command case (apply the analogous swap to the other three, asserting on `c.Agents()[0].Prompt`):

```go
func TestBodyCmdFailingCommandErrors(t *testing.T) {
	p := writeConfig(t, `
shell3.model("m", { base_url="http://x", api_key="k", model="id" })
shell3.agent({ name="code", model="m", prompt_cmd="exit 3" })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("failing prompt_cmd should error")
	}
}
```
  Keep `TestAgentPromptCmdResolves`, `TestSubagentPromptCmdResolves`, `TestAgentBothPromptAndPromptCmdErrors`, `TestSubagentBothPromptAndPromptCmdErrors` unchanged.

- [ ] **Step 7: Run tests**

Run: `go test ./internal/luacfg/ -run 'TestSkill|TestBodyCmd|TestAgentPromptCmd|TestSubagentPromptCmd' -count=1 -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/skillpath_test.go internal/luacfg/cmdbody_test.go
git commit -m "$(printf 'feat(bash-first): skills carry a file path (drop skill body/body_cmd)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```
(The build is NOT green yet — `persona.go`/`tooldefs.go`/`dispatch.go` still reference `Skill.Body`. Task 2 fixes that. If your executor requires green-per-commit, do Tasks 1+2 as one commit.)

---

### Task 2: Drop the `skill` tool; index by path

**Files:**
- Modify: `internal/luacfg/persona.go`
- Modify: `internal/luacfg/tooldefs.go`
- Modify: `internal/luacfg/dispatch.go` (remove the `skill` branch from `CallTool`)
- Modify: `internal/agentsetup/agentsetup.go` (`ToolDefs` call; drop `customNames["skill"]`)
- Test: `internal/luacfg/skillpath_test.go` (add index test)

- [ ] **Step 1: Add a failing index test** to `internal/luacfg/skillpath_test.go`:

```go
import "strings"

func TestSkillIndexUsesAbsPath(t *testing.T) {
	p := writeSkillConfig(t, skillHdr+`
local h = shell3.skill({ name="history", description="query sqlite", path="lib/skills/history.md" })
shell3.agent({ name="code", model="m", prompt="BASE", skills={ h } })
`, map[string]string{"lib/skills/history.md": "body\n"})
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	persona := c.BuildPersonaFor(c.Agents()[0])
	abs := filepath.Join(filepath.Dir(p), "lib/skills/history.md")
	if !strings.Contains(persona, "cat") {
		t.Fatalf("persona missing cat guidance:\n%s", persona)
	}
	if !strings.Contains(persona, "- history ("+abs+"): query sqlite") {
		t.Fatalf("persona missing path-indexed skill line:\n%s", persona)
	}
	if strings.Contains(persona, "`skill` tool") {
		t.Fatalf("persona still references the removed skill tool:\n%s", persona)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/luacfg/ -run TestSkillIndexUsesAbsPath -count=1`
Expected: FAIL (still says "skill tool", no path).

- [ ] **Step 3: Rewrite the `## Skills` block** in `internal/luacfg/persona.go` (replace the `if a.SkillsActive() { ... }` body):

```go
	if a.SkillsActive() {
		b.WriteString("\n## Skills\nRead a skill's file with `cat` when it applies.\n")
		for _, name := range a.Skills {
			for _, s := range c.Skills {
				if s.Name == name {
					fmt.Fprintf(&b, "- %s (%s): %s\n", s.Name, s.Path, s.Description)
				}
			}
		}
	}
```

- [ ] **Step 4: Delete the `skill` tool def** in `internal/luacfg/tooldefs.go`:
  - Remove the `skillTool` var (lines ~7–18).
  - Change `func ToolDefs(g ToolGates, custom []CustomTool, hasSkills bool)` to `func ToolDefs(g ToolGates, custom []CustomTool)` and delete the `if hasSkills { defs = append(defs, skillTool) }` block.

- [ ] **Step 5: Remove the `skill` branch from `CallTool`** in `internal/luacfg/dispatch.go` — delete the entire `if name == "skill" { ... }` block (lines ~40–48). (The rest of `CallTool` is removed in Phase 2; leave it for now.)

- [ ] **Step 6: Update `agentsetup.go`:**
  - Change the `toolDefs := luacfg.ToolDefs(a.Gates, customDefs, hasSkills)` call to `luacfg.ToolDefs(a.Gates, customDefs)`.
  - Delete the `if hasSkills { customNames["skill"] = true }` block.
  - `hasSkills := a.SkillsActive()` is still used for `BuildPersonaFor`/index gating via `a.SkillsActive()`; if `hasSkills` is now otherwise unused, inline it away or keep only if referenced. Verify with the build.

- [ ] **Step 7: Build + test**

Run: `go build ./... && go test ./internal/luacfg/ ./internal/agentsetup/ -count=1`
Expected: build OK; PASS.

- [ ] **Step 8: Full suite + lint**

Run: `go test ./... && gofmt -l . && go vet ./...`
Expected: green; `gofmt -l` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/luacfg/persona.go internal/luacfg/tooldefs.go internal/luacfg/dispatch.go internal/agentsetup/agentsetup.go internal/luacfg/skillpath_test.go
git commit -m "$(printf 'feat(bash-first): index skills by file path; remove the skill tool\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 3: Migrate scaffold skills to `.md` + `path`

**Files:**
- Create: `internal/scaffold/defaults/base/lib/skills/brainstorming.md`, `history.md`, `browser.md` (bodies extracted from the current `.lua` inline strings)
- Modify: `internal/scaffold/defaults/base/lib/skills/brainstorming.lua`, `history.lua`, `browser.lua` (register with `path=`)
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (fix the Phase-9 `body_cmd` skill comment)
- Test: `internal/scaffold` (whatever asserts the embedded default loads — run it)

- [ ] **Step 1: For each of `brainstorming`, `history`, `browser`:** open `internal/scaffold/defaults/base/lib/skills/<name>.lua`, copy the exact text inside the `body = [[ ... ]]` heredoc into a new sibling `internal/scaffold/defaults/base/lib/skills/<name>.md`, then replace the `.lua` file contents with the path form. Example for `history.lua`:

```lua
-- lib/skills/history.lua — registers the history skill from its .md body.
return shell3.skill({
  name        = "history",
  description = "Query your past sessions: a read-only SQLite DB you inspect with sqlite3.",
  path        = "lib/skills/history.md",
})
```
Use the existing `name`/`description` from each file verbatim; only `body = [[...]]` becomes `path = "lib/skills/<name>.md"`.

- [ ] **Step 2: Fix the scaffold tmpl comment.** In `internal/scaffold/defaults/base/shell3.lua.tmpl`, find the Phase-9 tip block that mentions `body_cmd` for a skill and replace the skill example line with the path form:

```
--   shell3.skill({ name="history", description="...", path="lib/skills/history.md" })
```
Leave the `prompt_cmd` examples for agents/subagents intact.

- [ ] **Step 3: Run scaffold tests + a real load of the embedded default**

Run: `go test ./internal/scaffold/ ./internal/bootstrap/ -count=1`
Expected: PASS. If a test materializes the scaffold to a temp dir and calls `luacfg.Load`, it now exercises the new skill files end-to-end.

- [ ] **Step 4: Full suite**

Run: `go test ./... -count=1`
Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/defaults/base/lib/skills internal/scaffold/defaults/base/shell3.lua.tmpl
git commit -m "$(printf 'feat(bash-first): scaffold skills as .md files referenced by path\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

# PHASE 2 — Bash-template tools

### Task 4: `CustomTool` struct + Lua surface (`command`/`secrets`/`background`/`timeout`)

**Files:**
- Modify: `internal/luacfg/luacfg.go` (`CustomTool` struct ~line 34)
- Modify: `internal/luacfg/register.go` (`toolKeys` ~line 164; `luaTool` ~line 177; add param-name validation)
- Test: `internal/luacfg/customtool_test.go` (new)

- [ ] **Step 1: Write failing tests** — create `internal/luacfg/customtool_test.go`:

```go
package luacfg

import (
	"path/filepath"
	"testing"
)

const toolHdr = `shell3.model("m", { base_url="http://x", api_key="k", model="id" })` + "\n"

func loadToolCfg(t *testing.T, lua string) *LoadedConfig {
	t.Helper()
	p := writeConfig(t, toolHdr+lua)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestToolCommandFieldsParse(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({
  name="echoer", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={ "msg" } },
  command="echo $msg", secrets={ "TOKEN" }, background=true, timeout=42,
})
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	ct := c.Tools["echoer"]
	if ct.Command != "echo $msg" || ct.Timeout != 42 || !ct.Background {
		t.Fatalf("fields = %+v", ct)
	}
	if len(ct.Secrets) != 1 || ct.Secrets[0] != "TOKEN" {
		t.Fatalf("secrets = %v", ct.Secrets)
	}
}

func TestToolHandlerKeyRejected(t *testing.T) {
	p := writeConfig(t, toolHdr+`
shell3.tool({ name="x", description="d", handler=function() return "" end })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("handler key should be rejected now")
	}
}

func TestToolNoCommandErrors(t *testing.T) {
	p := writeConfig(t, toolHdr+`shell3.tool({ name="x", description="d" })`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("tool without command should error")
	}
}

func TestToolUppercaseParamRejected(t *testing.T) {
	p := writeConfig(t, toolHdr+`
shell3.tool({ name="x", description="d", command="echo hi",
  parameters={ type="object", properties={ Query={ type="string" } } } })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("uppercase param name should be rejected")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/luacfg/ -run TestTool -count=1`
Expected: FAIL (fields undefined; `handler` still accepted).

- [ ] **Step 3: Change the `CustomTool` struct** in `internal/luacfg/luacfg.go`:

```go
// CustomTool is a declarative bash-command tool. The model supplies typed
// parameters (validated against Parameters); at call time each declared param is
// exported into the command's environment by its (lowercase) name and the
// command (a bash template) runs with that env. Secrets names each .env key to
// also export — kept out of the command string. Background dispatches via
// bash_bg (sink-reported) instead of blocking. There is no Lua handler.
type CustomTool struct {
	Name, Description string
	Parameters        map[string]any
	Command           string
	Secrets           []string
	Background         bool
	Timeout            int
}
```

- [ ] **Step 4: Update `toolKeys` + `luaTool`** in `internal/luacfg/register.go`. Replace `var toolKeys = ...`:
```go
var toolKeys = map[string]bool{
	"name": true, "description": true, "parameters": true,
	"command": true, "secrets": true, "background": true, "timeout": true,
}
```
Replace the body of `luaTool` (between `opts := L.CheckTable(1)`/`checkKeys` and the handle-return tail) with:
```go
	ct := CustomTool{
		Name:        optStr(opts, "name"),
		Description: optStr(opts, "description"),
		Command:     optStr(opts, "command"),
		Background:  optBool(opts, "background"),
		Timeout:     optInt(opts, "timeout"),
	}
	if ct.Name == "" || ct.Description == "" || ct.Command == "" {
		L.RaiseError("tool: name, description, and command are all required")
	}
	if sec, ok := opts.RawGetString("secrets").(*lua.LTable); ok {
		ct.Secrets = stringList(sec)
	}
	if p, ok := opts.RawGetString("parameters").(*lua.LTable); ok {
		ct.Parameters = tableToMap(p)
		if err := validateParamNames(ct.Name, ct.Parameters); err != nil {
			L.RaiseError("%s", err.Error())
		}
	}
	c.Tools[ct.Name] = ct
```
Add this helper to `internal/luacfg/register.go` (and `import "regexp"`):
```go
// paramNameRe constrains custom-tool parameter names to lowercase identifiers.
// Params are exported into the command env by their bare name; secrets and
// standard env vars are uppercase by convention, so a lowercase rule guarantees
// a param can never clobber PATH/HOME/IFS or collide with a declared secret.
var paramNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// validateParamNames rejects any declared parameter property whose name is not
// a lowercase identifier. params is the tool's JSON-schema map.
func validateParamNames(tool string, params map[string]any) error {
	props, _ := params["properties"].(map[string]any)
	for name := range props {
		if !paramNameRe.MatchString(name) {
			return fmt.Errorf("tool %q: parameter %q must be a lowercase identifier ([a-z][a-z0-9_]*)", tool, name)
		}
	}
	return nil
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/luacfg/ -run TestTool -count=1 -v`
Expected: PASS. (Build of `internal/luacfg` may still fail because `dispatch.go`'s `CallTool` references `tool.handler`; Task 5 removes it. If your executor needs a green build here, do Tasks 4+5 together.)

- [ ] **Step 6: Commit**

```bash
git add internal/luacfg/luacfg.go internal/luacfg/register.go internal/luacfg/customtool_test.go
git commit -m "$(printf 'feat(bash-first): custom tools declare a bash command, not a Lua handler\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 5: `ResolveCustomCall` (args→env) + delete `CallTool`/Lua helpers

**Files:**
- Create: `internal/luacfg/customtool.go`
- Modify: `internal/luacfg/dispatch.go` (delete `CallTool` + `goToLua`)
- Modify: `internal/luacfg/convert.go` (delete `goToLua` if it lives there — it's at convert.go:79)
- Modify: `internal/luacfg/lua_bash.go` (delete `luaBash`; keep `WrapBash`/`luaWrapBash`; delete `withIOUnlock` + `captureBuf` if now unused)
- Delete: `internal/luacfg/lua_http.go`, `internal/luacfg/lua_http_test.go` (if present)
- Modify: `internal/luacfg/lua_misc.go` (delete `luaURLEncode`; keep `luaSecret`)
- Modify: `internal/luacfg/register.go` (`registerShell3`: drop `bash`/`http`/`urlencode` registrations)
- Modify/Delete: `internal/luacfg/lua_bash_test.go` (drop `shell3.bash` tests)
- Test: `internal/luacfg/customtool_test.go` (extend)

- [ ] **Step 1: Add failing resolution tests** to `internal/luacfg/customtool_test.go`:

```go
import "strings"

func envMap(env []string) map[string]string {
	m := map[string]string{}
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i >= 0 {
			m[e[:i]] = e[i+1:]
		}
	}
	return m
}

func TestResolveExportsParamsAndSecrets(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({
  name="search", description="d",
  parameters={ type="object", properties={ query={type="string"}, count={type="integer"} } },
  secrets={ "BRAVE_API_KEY" }, command="echo $query",
})
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	c.Secrets["BRAVE_API_KEY"] = "sekret"
	rc, err := c.ResolveCustomCall("search", `{"query":"foo bar","count":5}`)
	if err != nil {
		t.Fatal(err)
	}
	m := envMap(rc.Env)
	if m["query"] != "foo bar" || m["count"] != "5" || m["BRAVE_API_KEY"] != "sekret" {
		t.Fatalf("env = %v", m)
	}
	if rc.Command != "echo $query" {
		t.Fatalf("command = %q", rc.Command)
	}
}

func TestResolveMissingSecretErrors(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({ name="x", description="d", command="echo hi", secrets={ "NOPE" } })
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	if _, err := c.ResolveCustomCall("x", "{}"); err == nil {
		t.Fatal("missing secret should error")
	}
}

func TestResolveDropsUndeclaredArgs(t *testing.T) {
	c := loadToolCfg(t, `
local tl = shell3.tool({ name="x", description="d", command="echo hi",
  parameters={ type="object", properties={ query={type="string"} } } })
shell3.agent({ name="code", model="m", prompt="p", tools={ custom={ tl } } })
`)
	defer c.Close()
	// A misbehaving model sends an undeclared key; it must NOT reach the env.
	rc, err := c.ResolveCustomCall("x", `{"query":"ok","PATH":"/evil"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, bad := envMap(rc.Env)["PATH"]; bad {
		t.Fatal("undeclared arg PATH leaked into env")
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/luacfg/ -run TestResolve -count=1`
Expected: FAIL (`ResolveCustomCall` undefined).

- [ ] **Step 3: Create `internal/luacfg/customtool.go`:**

```go
package luacfg

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ResolvedCall is a custom-tool invocation reduced to what the executor (in
// internal/chat) needs: the bash command, the environment to run it with
// (declared params + secrets, as KEY=VALUE), and the dispatch knobs.
type ResolvedCall struct {
	Command    string
	Env        []string
	Background bool
	Timeout    int
}

// ResolveCustomCall validates a custom-tool call and returns its ResolvedCall.
// Only arguments matching a DECLARED parameter are exported (so a misbehaving
// model cannot inject arbitrary env vars). Each declared secret is looked up in
// .env and exported by name; a missing secret is an error (never a silent
// empty value). The command itself is the trusted, author-defined template — it
// is NOT passed through wrap_bash (the model supplies only env values).
func (c *LoadedConfig) ResolveCustomCall(name, argsJSON string) (ResolvedCall, error) {
	tool, ok := c.Tools[name]
	if !ok {
		return ResolvedCall{}, fmt.Errorf("unknown custom tool %q", name)
	}
	var args map[string]any
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
			return ResolvedCall{}, fmt.Errorf("tool %q: bad args json: %w", name, err)
		}
	}
	declared := declaredParamNames(tool.Parameters)
	env := make([]string, 0, len(args)+len(tool.Secrets))
	for k, v := range args {
		if !declared[k] {
			continue // undeclared key: never export (anti-injection)
		}
		env = append(env, k+"="+envValue(v))
	}
	for _, s := range tool.Secrets {
		val, ok := c.Secrets[s]
		if !ok {
			return ResolvedCall{}, fmt.Errorf("tool %q: secret %q not found in .env", name, s)
		}
		env = append(env, s+"="+val)
	}
	return ResolvedCall{Command: tool.Command, Env: env, Background: tool.Background, Timeout: tool.Timeout}, nil
}

// declaredParamNames returns the set of property names from a tool's JSON-schema
// parameters map.
func declaredParamNames(params map[string]any) map[string]bool {
	out := map[string]bool{}
	if props, ok := params["properties"].(map[string]any); ok {
		for k := range props {
			out[k] = true
		}
	}
	return out
}

// envValue renders a JSON-decoded argument as an environment value: scalars in
// their natural string form (numbers without trailing zeros), composites as
// compact JSON.
func envValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
```

- [ ] **Step 4: Delete `CallTool` and `goToLua`.**
  - In `internal/luacfg/dispatch.go`: delete the whole `CallTool` function. Keep `lockVM` and `toolContext` (used by `WrapBash`). Remove the now-unused `encoding/json` import if nothing else needs it.
  - In `internal/luacfg/convert.go`: delete `goToLua` (it was only used by `CallTool`). `luaToGo`/`tableToMap`/`stringList`/`handleNames` stay.

- [ ] **Step 5: Delete the Lua IO helpers.**
  - `internal/luacfg/lua_http.go`: delete the file. Also delete `internal/luacfg/lua_http_test.go` if it exists.
  - `internal/luacfg/lua_misc.go`: delete `luaURLEncode` (keep `luaSecret`); remove the now-unused `net/url` import.
  - `internal/luacfg/lua_bash.go`: delete `luaBash`, `captureBuf`, and `withIOUnlock` (all only served `shell3.bash`/`http`). Keep `luaWrapBash`, `WrapBash`, `optReason`. If `withIOUnlock` removal leaves `vmLockHeld` write-only, simplify `lockVM` to drop the flag and remove `vmLockHeld` from the struct in `luacfg.go` — verify against `go vet`.
  - Delete `shell3.bash` tests in `internal/luacfg/lua_bash_test.go` (keep any `WrapBash` tests).

- [ ] **Step 6: Unregister the removed globals** in `internal/luacfg/register.go` `registerShell3`. Delete these lines:
```go
	L.SetField(tbl, "urlencode", L.NewFunction(luaURLEncode))
	L.SetField(tbl, "bash", L.NewFunction(c.luaBash))
	...
	httpT := L.NewTable()
	L.SetField(httpT, "request", L.NewFunction(c.luaHTTPRequest))
	L.SetField(httpT, "get", L.NewFunction(c.luaHTTPGet))
	L.SetField(httpT, "post", L.NewFunction(c.luaHTTPPost))
	L.SetField(tbl, "http", httpT)
```
Keep `shell3.env.secret` and `shell3.wrap_bash` registrations.

- [ ] **Step 7: Build the package + run tests**

Run: `go build ./internal/luacfg/ && go test ./internal/luacfg/ -count=1`
Expected: build OK; PASS. (`internal/agentsetup` still calls `p.lc.CallTool` via `Parts.CustomTool` — that package won't build until Task 7. Do not run `go build ./...` yet.)

- [ ] **Step 8: Commit**

```bash
git add internal/luacfg
git commit -m "$(printf 'feat(bash-first): ResolveCustomCall (args+secrets to env); drop Lua tool helpers\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 6: `bgjobs.Start` gains an env parameter

**Files:**
- Modify: `internal/bgjobs/bgjobs.go` (`Start` signature + `c.Env`)
- Modify: `internal/chat/handler_bash_bg.go` (pass `nil` env)
- Modify: `internal/bgjobs/bgjobs_test.go` (update `Start` calls)
- Test: `internal/bgjobs/bgjobs_test.go` (add env test)

- [ ] **Step 1: Add a failing env test** to `internal/bgjobs/bgjobs_test.go` (follow the existing test's setup for workdir/sink; this is the shape):

```go
func TestStartInjectsEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	job, err := Start(`printf "%s" "$GREETING" > `+out, dir, []string{"GREETING=hi-env"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	// wait for the detached job to finish writing
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(out); string(b) == "hi-env" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = job
	t.Fatalf("env var not visible to background job; out=%q", readFile(out))
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }
```
Ensure the test file imports `os`, `path/filepath`, `time`.

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/bgjobs/ -run TestStartInjectsEnv -count=1`
Expected: FAIL (compile: `Start` takes 4 args, not 5).

- [ ] **Step 3: Update `Start`** in `internal/bgjobs/bgjobs.go`. Change the signature and add env wiring (and `import "os"` if not already imported):
```go
func Start(command, workdir string, env []string, sinkPath string, notifyOnExit bool) (Job, error) {
```
After `c.Dir = workdir` add:
```go
	if len(env) > 0 {
		c.Env = append(os.Environ(), env...)
	}
```

- [ ] **Step 4: Update the existing caller + tests.**
  - `internal/chat/handler_bash_bg.go`: `bgjobs.Start(p.Command, wd, nil, cfg.SinkPath, notifyOnExit)`.
  - Every other `bgjobs.Start(` call in `internal/bgjobs/bgjobs_test.go` (and anywhere else `grep -rn 'bgjobs.Start(' --include='*.go'` finds): insert `nil,` as the new third arg.

- [ ] **Step 5: Build + test**

Run: `go build ./internal/bgjobs/ ./internal/chat/ && go test ./internal/bgjobs/ -count=1`
Expected: build OK; PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/bgjobs internal/chat/handler_bash_bg.go
git commit -m "$(printf 'feat(bash-first): bgjobs.Start accepts an env for command-template tools\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 7: Chat-side custom-tool execution + stub routing + agentsetup wiring

This is the atomic cross-cutting task: `internal/chat` gains the executor and the new config fields, and `internal/agentsetup` rewires to them — together, so `go build ./...` is green again.

**Files:**
- Modify: `internal/chat/handler_bash.go` (extract `runBashCapture`)
- Modify: `internal/chat/toolhandler.go` (`ToolConfig`?/`TurnConfig`: drop `CustomTool`; add `ResolveCustomTool`, `StubTools`; add `ResolvedTool` type)
- Modify: `internal/chat/chat.go` (`Config`: same; `NewTurnConfig` copy)
- Modify: `internal/chat/tools.go` (rewrite `dispatchCustomTool`)
- Modify: `internal/chat/turn.go` (call `dispatchCustomTool(ctx, cfg, …)`; add stub fallback branch)
- Modify: `internal/agentsetup/agentsetup.go` (`Parts.ResolveCustomTool`; wire `ResolveCustomTool`/`StubTools`; drop `Parts.CustomTool`; drop stub→`customNames`)
- Test: `internal/chat/tools_test.go` (custom-tool fg/bg + stub)

- [ ] **Step 1: Write failing chat tests** in `internal/chat/tools_test.go` (new or appended):

```go
package chat

import (
	"context"
	"strings"
	"testing"
)

func TestDispatchCustomToolForeground(t *testing.T) {
	cfg := TurnConfig{
		WorkDir: t.TempDir(),
		CustomToolNames: map[string]bool{"echoer": true},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `printf "%s" "$msg"`, Env: []string{"msg=hello-tool"}}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "echoer", `{"msg":"hello-tool"}`)
	if res.isError || strings.TrimSpace(res.output) != "hello-tool" {
		t.Fatalf("res = %+v", res)
	}
}

func TestDispatchCustomToolNonZeroExitIsError(t *testing.T) {
	cfg := TurnConfig{
		WorkDir: t.TempDir(),
		CustomToolNames: map[string]bool{"boom": true},
		ResolveCustomTool: func(name, args string) (ResolvedTool, error) {
			return ResolvedTool{Command: `echo nope; exit 7`}, nil
		},
	}
	res := dispatchCustomTool(context.Background(), cfg, "boom", `{}`)
	if !res.isError || !strings.Contains(res.output, "exited 7") {
		t.Fatalf("res = %+v", res)
	}
}
```

- [ ] **Step 2: Run, verify fail**

Run: `go test ./internal/chat/ -run TestDispatchCustomTool -count=1`
Expected: FAIL (`ResolvedTool`/fields undefined).

- [ ] **Step 3: Extract `runBashCapture`** in `internal/chat/handler_bash.go`. Add this function (note the `os` import) and rewrite `BashHandler.Execute`'s tail to call it:

```go
// runBashCapture runs command via `bash -c` in workdir with extraEnv appended to
// os.Environ() (nil = inherit only), capturing combined stdout+stderr, honoring
// timeout + cancellation. It returns the elided output and the process exit code
// (124 on timeout, -1 on a start error). Shared by the bash tool and foreground
// command-template tools.
func runBashCapture(ctx context.Context, command, workdir string, extraEnv []string, timeout time.Duration) (string, int) {
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c := exec.CommandContext(tctx, "bash", "-c", command)
	c.Dir = workdir
	if len(extraEnv) > 0 {
		c.Env = append(os.Environ(), extraEnv...)
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		if c.Process == nil {
			return nil
		}
		return syscall.Kill(-c.Process.Pid, syscall.SIGTERM)
	}
	c.WaitDelay = bashWaitDelay
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	exit := 0
	err := c.Run()
	if err != nil {
		switch {
		case errors.Is(tctx.Err(), context.DeadlineExceeded):
			exit = 124
			fmt.Fprintf(&buf, "\nerror: command timed out after %s\n", timeout)
		default:
			if ee, ok := err.(*exec.ExitError); ok {
				exit = ee.ExitCode()
			} else {
				exit = -1
				if buf.Len() == 0 {
					fmt.Fprintf(&buf, "error: %v\n", err)
				}
			}
		}
	}
	if buf.Len() == 0 {
		return "(no output)", exit
	}
	return elideMiddle(buf.Bytes(), MaxBashOutputBytes), exit
}
```
Then replace the body of `BashHandler.Execute` from `tctx, cancel := ...` through the final `return elideMiddle(...)` with:
```go
	out, _ := runBashCapture(ctx, command, cfg.WorkDir, nil, timeout)
	return out, nil
```
Add `"os"` to the imports.

- [ ] **Step 4: Add the new config types/fields.** In `internal/chat/toolhandler.go`:
  - Add the type:
```go
// ResolvedTool is a custom-tool call reduced to an executable form: the bash
// command, the env to run it with (declared params + secrets), and dispatch
// knobs. Produced by agentsetup (via luacfg.ResolveCustomCall) and run by
// dispatchCustomTool.
type ResolvedTool struct {
	Command    string
	Env        []string
	Background bool
	Timeout    int
}
```
  - In `TurnConfig`: remove the `CustomTool func(...)` field; add:
```go
	// ResolveCustomTool resolves a custom-tool call to its executable form
	// (command + env). Names in CustomToolNames route here.
	ResolveCustomTool func(name, argsJSON string) (ResolvedTool, error)
	// StubTools maps a hallucinated tool name to its redirect message (a nudge,
	// never an error). Checked after real/custom tools so a real tool always wins.
	StubTools map[string]string
```
  In `internal/chat/chat.go` `Config`: make the same swap (remove `CustomTool`, add `ResolveCustomTool` + `StubTools`), and in `NewTurnConfig` replace `CustomTool: cfg.CustomTool,` with:
```go
		ResolveCustomTool: cfg.ResolveCustomTool,
		StubTools:         cfg.StubTools,
```

- [ ] **Step 5: Rewrite `dispatchCustomTool`** in `internal/chat/tools.go` (replace the existing function; add imports `fmt`, `time`, and `github.com/weatherjean/shell3/internal/bgjobs`):

```go
func dispatchCustomTool(ctx context.Context, cfg TurnConfig, name, rawArgs string) toolResult {
	if cfg.ResolveCustomTool == nil {
		return errResult(fmt.Sprintf("error: unknown tool %q", name))
	}
	rt, err := cfg.ResolveCustomTool(name, rawArgs)
	if err != nil {
		return errResult("error: " + err.Error())
	}
	if rt.Background {
		job, err := bgjobs.Start(rt.Command, cfg.WorkDir, rt.Env, cfg.SinkPath, true)
		if err != nil {
			return errResult("error: " + err.Error())
		}
		return okResult(fmt.Sprintf("started background tool %s\npid: %d\nlog: %s\n", job.ID, job.PID, job.Log))
	}
	timeout := time.Duration(DefaultBashTimeoutSeconds) * time.Second
	if rt.Timeout > 0 {
		t := rt.Timeout
		if t > MaxBashTimeoutSeconds {
			t = MaxBashTimeoutSeconds
		}
		timeout = time.Duration(t) * time.Second
	}
	out, code := runBashCapture(ctx, rt.Command, cfg.WorkDir, rt.Env, timeout)
	if code != 0 {
		return errResult(fmt.Sprintf("error: command exited %d\n%s", code, out))
	}
	return okResult(out)
}
```

- [ ] **Step 6: Update `turn.go` routing.** In `executeToolCalls`, change the custom-tool branch and add a stub fallback. Replace:
```go
		} else if cfg.CustomToolNames[tc.Name] {
			res = dispatchCustomTool(ctx, cfg.CustomTool, tc.Name, tc.RawArgs)
		} else if h, ok := cfg.Handlers[tc.Name]; ok {
			handler = h
		} else {
			res = errResult(fmt.Sprintf("error: unknown tool %q", tc.Name))
		}
```
with:
```go
		} else if cfg.CustomToolNames[tc.Name] {
			res = dispatchCustomTool(ctx, cfg, tc.Name, tc.RawArgs)
		} else if h, ok := cfg.Handlers[tc.Name]; ok {
			handler = h
		} else if msg, ok := cfg.StubTools[tc.Name]; ok {
			res = okResult(msg) // hallucinated tool: return its redirect nudge
		} else {
			res = errResult(fmt.Sprintf("error: unknown tool %q", tc.Name))
		}
```
(Match the exact surrounding lines in `turn.go`; the `else if`/`else` shape is what changes.)

- [ ] **Step 7: Rewire `agentsetup.go`.**
  - Replace `Parts.CustomTool` (the `CallTool` wrapper) with:
```go
// ResolveCustomTool resolves a custom-tool call to its executable form for the
// chat layer (which owns WorkDir/SinkPath and runs it).
func (p *Parts) ResolveCustomTool(name, argsJSON string) (chat.ResolvedTool, error) {
	rc, err := p.lc.ResolveCustomCall(name, argsJSON)
	if err != nil {
		return chat.ResolvedTool{}, err
	}
	return chat.ResolvedTool{Command: rc.Command, Env: rc.Env, Background: rc.Background, Timeout: rc.Timeout}, nil
}
```
  - In `SessionConfig`, replace `CustomTool: p.CustomTool,` with:
```go
		ResolveCustomTool: p.ResolveCustomTool,
		StubTools:         p.lc.StubNames(),
```
  - In `runtimeForAgent`, in the stub-tools loop, delete the line `customNames[name] = true` (stubs route via `StubTools` now, not `CustomToolNames`). Keep appending the stub `toolDefs`/`toolNames` entries.

- [ ] **Step 8: Build everything + run tests**

Run: `go build ./... && go test ./internal/chat/ ./internal/agentsetup/ -count=1`
Expected: build OK; PASS.

- [ ] **Step 9: Full suite + lint**

Run: `go test ./... && gofmt -l . && go vet ./...`
Expected: green.

- [ ] **Step 10: Commit**

```bash
git add internal/chat internal/agentsetup/agentsetup.go
git commit -m "$(printf 'feat(bash-first): run custom tools as bash templates; route stubs by map\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 8: Migrate scaffold tools to command templates

**Files:**
- Modify: `internal/scaffold/defaults/base/lib/tools.lua`
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl` (any `handler`/`shell3.http`/`shell3.bash` examples in comments)
- Test: `internal/scaffold` / `internal/bootstrap` load tests

- [ ] **Step 1: Rewrite `internal/scaffold/defaults/base/lib/tools.lua`** as two command-template tools:

```lua
-- lib/tools.lua — example custom tools as bash command templates.
-- Params are exported into the command env by their (lowercase) name; declared
-- secrets are exported too (and kept out of the command string). Returns
-- { web_fetch, brave_search } for require().

local web_fetch = shell3.tool({
  name        = "web_fetch",
  description = "Fetch a URL and return its plain-text content (tags stripped) plus a list of links.",
  parameters  = {
    type = "object",
    properties = { url = { type = "string", description = "The URL to fetch." } },
    required = { "url" },
  },
  command = [[
curl -sfL --max-time 15 "$url" | python3 - "$url" <<'PY'
import sys, re, html
url = sys.argv[1]
data = sys.stdin.read()
links = sorted(set(re.findall(r'href="(https?://[^"]+)"', data)))
text = re.sub(r'(?is)<(script|style)[^>]*>.*?</\1>', ' ', data)
text = re.sub(r'(?s)<!--.*?-->', ' ', text)
text = re.sub(r'(?s)<[^>]+>', ' ', text)
text = html.unescape(text)
text = re.sub(r'\s+', ' ', text).strip()
print("URL:", url)
print()
print(text)
if links:
    print()
    print("Links:")
    print("\n".join(links))
PY
]],
})

local brave_search = shell3.tool({
  name        = "brave_search",
  description = "Search the web via Brave Search; returns titles, URLs, and snippets.",
  parameters  = {
    type = "object",
    properties = {
      query = { type = "string",  description = "The search query." },
      count = { type = "integer", description = "Results to return (1-20, default 10)." },
    },
    required = { "query" },
  },
  secrets = { "BRAVE_API_KEY" },
  command = [[
curl -sf -G "https://api.search.brave.com/res/v1/web/search" \
  -H "Accept: application/json" \
  -H "X-Subscription-Token: $BRAVE_API_KEY" \
  --data-urlencode "q=$query" --data "count=${count:-10}" \
| jq -r '.web.results[]? | .title + "\n" + .url + "\n" + (.description // "") + "\n---"'
]],
})

return { web_fetch = web_fetch, brave_search = brave_search }
```

- [ ] **Step 2: Scrub the tmpl** `internal/scaffold/defaults/base/shell3.lua.tmpl` for any commented `handler = function`, `shell3.http`, `shell3.bash`, or `shell3.urlencode` usage and update to the `command=`/`secrets=` form (or delete the stale example). `grep -n 'handler\|shell3.http\|shell3.bash\|urlencode' internal/scaffold/defaults/base/shell3.lua.tmpl` to find them.

- [ ] **Step 3: Load the embedded default**

Run: `go test ./internal/scaffold/ ./internal/bootstrap/ -count=1`
Expected: PASS (a scaffold materialize+`Load` test now validates the new `tools.lua`). If no such test exists, add one that writes the scaffold to a temp dir and calls `luacfg.Load`, asserting no error and that `web_fetch`/`brave_search` are registered with non-empty `Command`.

- [ ] **Step 4: Full suite**

Run: `go test ./... -count=1`
Expected: green.

- [ ] **Step 5: Commit**

```bash
git add internal/scaffold/defaults/base/lib/tools.lua internal/scaffold/defaults/base/shell3.lua.tmpl
git commit -m "$(printf 'feat(bash-first): scaffold tools as bash command templates (curl/jq/python3)\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

### Task 9: Docs + design-doc cross-update

**Files:**
- Modify: `CLAUDE.md` (skill/tool mechanism, tool count)
- Modify: `docs/dev/superpowers/specs/2026-06-11-bash-first-design.md` (note §7 superseded for skills; tool surface)
- Modify: the new spec `docs/dev/superpowers/specs/2026-06-11-bash-first-tools-and-skills-design.md` (mark IMPLEMENTED)
- Modify: `docs/cookbook/*` if it documents `shell3.tool{handler=…}`, `shell3.http`, or the `skill` tool

- [ ] **Step 1: Update `CLAUDE.md`.** Where it describes the agent surface, state that skills are `.md` files read with `cat` (no `skill` tool) and custom tools are bash-command templates (`command`/`secrets`/`background`), and that `shell3.bash`/`http`/`urlencode` were removed. Keep it to the existing intro paragraph's style.

- [ ] **Step 2: Cross-reference the design docs.** In `2026-06-11-bash-first-design.md`, add a one-line note under §7 that command-backed skill *bodies* are superseded by file-`path` skills (this spec); `prompt_cmd` for agents/subagents stands. In the new spec, change the top to note it is implemented.

- [ ] **Step 3: Cookbook sweep.** `grep -rn 'shell3.http\|handler =\|handler=\|\bskill\b tool\|body = \[\[' docs/` and fix any tool/skill examples to the new shapes.

- [ ] **Step 4: Verify nothing references removed APIs in-repo**

Run: `grep -rn 'shell3.http\|shell3.bash\|shell3.urlencode\|\.luaBash\|CallTool\|body_cmd' --include='*.go' --include='*.lua' --include='*.md' . | grep -v 2026-06-11-bash-first-tools-and-skills`
Expected: only intentional historical mentions (e.g. changelog). No live code/config references `CallTool`, `shell3.http`, `shell3.bash`, or skill `body_cmd`.

- [ ] **Step 5: Full suite + lint (final)**

Run: `go build ./... && go test ./... && gofmt -l . && go vet ./...`
Expected: green; `gofmt -l` empty.

- [ ] **Step 6: Commit**

```bash
git add CLAUDE.md docs
git commit -m "$(printf 'docs(bash-first): file-backed skills + bash-template tools\n\nCo-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>')"
```

---

## Post-plan: live config follow-up (outside the repo)

After the branch is green, the user's personal `~/.shell3/` configs need the same migration (they currently use inline-body skills and, where any custom tools exist, the old `handler`/`shell3.http` shape):
- Split `~/.shell3/lib/skills/*.lua` and `~/.shell3/telegram/lib/skills/*.lua` inline bodies into `.md` files + `path=` registration.
- Convert any `shell3.tool{handler=…}` to `command=`/`secrets=`.
- Re-validate both configs with the loader (the `internal/cfgcheck` throwaway pattern used during the live-config fix).
This is a separate, post-merge config edit — not part of this repo plan.

---

## Self-Review

- **Spec coverage:** §1 skills → Tasks 1–3; §2 tool surface/param-env/secrets/exec/security → Tasks 4–7; §3 scaffold migration → Tasks 3 (skills) + 8 (tools); removals (§2/§4) → Task 5; docs → Task 9. The spec's "foreground returns stdout, non-zero exit surfaces error" → Task 7 `dispatchCustomTool` (exit-code branch). Anti-injection (declared-param filtering) is an addition beyond the spec's lowercase rule, covered in Task 5 + `TestResolveDropsUndeclaredArgs`.
- **Type consistency:** `ResolvedCall` (luacfg) ↔ `chat.ResolvedTool` (mapped in `Parts.ResolveCustomTool`); `runBashCapture(ctx, command, workdir, extraEnv, timeout) (string, int)` used by both `BashHandler` and `dispatchCustomTool`; `bgjobs.Start(command, workdir, env, sinkPath, notifyOnExit)` consistent across Task 6 caller + Task 7 background dispatch; `Skill{Name,Description,Path}` set in Task 1, read in Task 2 persona.
- **Build-green sequencing:** Tasks 1–2 are paired (note in Task 1 Step 8); Tasks 4–5 are paired (note in Task 4 Step 5); Task 5 deliberately defers `go build ./...` to Task 7, which is the atomic luacfg↔chat↔agentsetup rewire. Every other task ends green.
- **Placeholder scan:** none — all code/tests/commands are concrete.
