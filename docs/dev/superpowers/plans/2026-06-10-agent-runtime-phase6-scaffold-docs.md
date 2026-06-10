# Agent Runtime Phase 6: Scaffold/Bootstrap Refresh + Final Docs — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans. Steps use checkbox (`- [ ]`) syntax.

**Goal:** A freshly `shell3 boot`-ed config demonstrates the new runtime out of the box: the `code` agent gets the in-process `spawn_agent`/`list_agents` builtins (replacing the retired `spawning-subagents` skill), and an example `ask` guard (`guards.confirm_destructive`) so the y/N approval flow works in the TUI with no hand-editing. Cookbook and top-level docs (README + `pkg/shell3` package doc + CHANGELOG) are brought current for Runtime/Session, steering, approval, media, and subagents.

**Architecture:** Edits are to embedded scaffold templates (`internal/scaffold/defaults/base/`), their tests, the cookbook (`docs/cookbook/`), and prose docs. No production Go logic changes — Phase 5 already shipped the mechanics; Phase 6 wires the default config to use them and documents the whole agent-runtime surface.

**Tech Stack:** Go embedded templates + Lua config; tests via `go test` (the canonical scaffold test renders the template and loads it through the real `luacfg.Load`). Spec: `docs/dev/superpowers/specs/2026-06-10-agent-runtime-design.md` ("Bootstrap / scaffold updates" + "Testing").

**Conventions:**
- Never read `.env` (beside any `shell3.lua`) or `ai-do-not-read.*` files.
- Verify each task with `go test -race -count=1 ./...` (the scaffold test loads the rendered config, so a broken template fails the build).
- Commit bodies end with `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`.
- **Manual acceptance is the USER's** (delete `~/.shell3`, `shell3 boot`, play). Do NOT run it; stop and hand off after the automated work.

---

## File Structure

| File | Change | Task |
|------|--------|------|
| `internal/scaffold/defaults/base/shell3.lua.tmpl` | retire subagents skill require + `skills={subagents}`; add `subagents=true` to code agent; prompt "Subagents" section; wire `confirm_destructive` guard | 1 |
| `internal/scaffold/defaults/base/lib/skills/subagents.lua` | DELETE (retired) | 1 |
| `internal/scaffold/defaults/base/lib/guards.lua` | add `confirm_destructive` ask guard | 1 |
| `internal/scaffold/scaffold_test.go` | update skills/agents assertions; assert code agent exposes spawn tools + the ask guard | 2 |
| `docs/cookbook/lib/guards.lua` | add an `ask`-verdict recipe | 3 |
| `docs/cookbook/README.md` | note the `spawn_agent`/`list_agents` builtins; drop JSONL-polling guidance | 3 |
| `README.md`, `pkg/shell3/doc.go` (or package comment), `CHANGELOG.md` | final docs pass: Runtime/Session, steering, approval, media, subagents | 4 |

---

## Task 1: Scaffold template — retire subagents skill, enable builtins, add example ask guard

**Files:**
- Modify: `internal/scaffold/defaults/base/shell3.lua.tmpl`
- Delete: `internal/scaffold/defaults/base/lib/skills/subagents.lua`
- Modify: `internal/scaffold/defaults/base/lib/guards.lua`

- [ ] **Step 1: Read the three files first.** Confirm current line content before editing: the `require("lib.skills.subagents")` line (~line 9), the code agent's prompt `## Skills` section (~lines 50-51) and its `tools` table (~lines 59-69) and `skills = { subagents }` (~line 70) and `on_tool_call = { guards.no_env_edit }` (~line 71), and `lib/guards.lua`'s current `no_env_edit` + return table.

- [ ] **Step 2: Delete the retired skill module**

```bash
git rm internal/scaffold/defaults/base/lib/skills/subagents.lua
```

- [ ] **Step 3: Edit `shell3.lua.tmpl` — remove the subagents require**

Delete the line:
```lua
local subagents     = require("lib.skills.subagents")
```
Leave the `brainstorming` require intact.

- [ ] **Step 4: Edit `shell3.lua.tmpl` — code agent prompt: replace the Skills section with a Subagents note**

In the code agent's prompt string, replace the `## Skills` block:
```lua
## Skills
- `spawning-subagents`: read it and follow it when delegating an independent sub-task to a fresh parallel shell3 process.
```
with a Subagents tools note (place it as the last bullet of the existing `## Tools` section, then drop the now-empty `## Skills` heading):
```lua
- `spawn_agent` / `list_agents`: hand an independent sub-task to a subagent that runs in the background; its result comes back to you automatically when it finishes (you don't poll). Use `list_agents` to check progress. Good for parallelizable work you don't need to watch.
```
(If the `## Skills` heading would be left with no bullets, remove the heading line too. The code agent ends up with no `## Skills` section.)

- [ ] **Step 5: Edit `shell3.lua.tmpl` — code agent: enable the builtins, drop the skill wiring, add the example guard**

In the code agent's `tools` table, add `subagents = true` (place it after `media = true`):
```lua
  tools = {
    bash              = true,
    bash_bg           = true,
    shell_interactive = true,
    edit              = true,
    history           = true,
    prune             = true,
    compact           = true,
    media             = true,
    subagents         = true,
    custom            = { tools.web_fetch, tools.brave_search },
  },
```
Remove the `skills = { subagents },` line from the code agent (it now has no skills). Change its guard wiring to include the new example ask guard:
```lua
  on_tool_call = { guards.no_env_edit, guards.confirm_destructive },
```

- [ ] **Step 6: Edit `shell3.lua.tmpl` — plan agent stays subagent-free**

The plan agent keeps `skills = { brainstorming }` and does NOT get `subagents` (omit the key — `optBool` defaults false). Add `guards.confirm_destructive` to the plan agent's `on_tool_call` too so the approval flow is visible there as well:
```lua
  on_tool_call = { guards.no_env_edit, guards.confirm_destructive },
```

- [ ] **Step 7: Add the `confirm_destructive` ask guard to `lib/guards.lua`**

Append a guard that returns the `ask` verdict for obviously destructive bash, then export it. Final file:
```lua
-- lib/guards.lua — on_tool_call guards. Returned for require() in shell3.lua.
local function no_env_edit(call)
  local tool   = call.tool or ""
  local params = call.params or {}
  if tool == "edit_file" then
    local path = tostring(params.file_path or "")
    if path:match("%.env$") then
      return { action = "block", reason = "editing .env files is not allowed; manage secrets manually" }
    end
  end
  return { action = "allow" }
end

-- confirm_destructive asks the human before running obviously destructive bash
-- (recursive force-remove, force-push). Returning action="ask" suspends the
-- call until the front-end answers: the TUI shows an inline y/N prompt, a bot
-- shows Approve/Deny. With no approver wired (headless), ask fails closed (deny).
local function confirm_destructive(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("rm%s+%-rf") or cmd:match("git%s+push%s+.-%-%-force") then
      return { action = "ask", reason = "destructive command — confirm before running" }
    end
  end
  return { action = "allow" }
end

return { no_env_edit = no_env_edit, confirm_destructive = confirm_destructive }
```

- [ ] **Step 8: Verify the rendered config loads and run the suite**

```bash
go test ./internal/scaffold -run RenderedConfigLoads -v
```
This will FAIL on the now-stale assertions in Task 2; that's expected — proceed to Task 2 to fix the test, then re-run. First just confirm the template still *parses* (the failure should be an assertion mismatch like "expected 2 skills", NOT a Lua load error / unrendered `{{`). If you get a Lua parse/load error, the template edit is malformed — fix it before moving on.

- [ ] **Step 9:** (Commit happens after Task 2, since the test update is part of the same logical change. Do not commit a red tree.)

---

## Task 2: Scaffold tests — reflect retired skill + enabled builtins

**Files:**
- Modify: `internal/scaffold/scaffold_test.go`

- [ ] **Step 1: Read `scaffold_test.go`.** Identify every assertion that will drift: `TestRenderedConfigLoads` asserts `len(c.Skills) != 2` (now 1 — only `brainstorming`); `TestRenderBaseConfig` may assert the output contains `lib/skills/subagents.lua` or the string `spawning-subagents` (now gone). Find them.

- [ ] **Step 2: Update `TestRenderedConfigLoads`**

- Change the skills assertion from 2 to 1 and update the comment:
```go
	if len(c.Skills) != 1 {
		t.Errorf("expected 1 skill (brainstorming), got %d", len(c.Skills))
	}
```
- Add an assertion that the `code` agent has the subagents gate on and the `plan` agent does not. Use the loaded agents (`agents[0]` is `code`, `agents[1]` is `plan`). Check the gate via whatever `Agent` exposes — `agents[0].Gates.Subagents` should be true, `agents[1].Gates.Subagents` false. (Confirm the field path by reading the `Agent`/`ToolGates` types; `Gates` is a public field on `luacfg.Agent`.)
```go
	if !agents[0].Gates.Subagents {
		t.Error("code agent should have subagents enabled")
	}
	if agents[1].Gates.Subagents {
		t.Error("plan agent should not have subagents enabled")
	}
```
- Assert the code agent's rendered tool schema actually exposes the builtins. The cleanest available signal: the agent's tool-definition list includes `spawn_agent`/`list_agents`. If the test already builds tool defs (via `luacfg.ToolDefs(agents[0].Gates, ...)`), assert presence; otherwise add:
```go
	defs := luacfg.ToolDefs(agents[0].Gates, nil, agents[0].SkillsActive())
	var sawSpawn bool
	for _, d := range defs {
		if d.Name == "spawn_agent" {
			sawSpawn = true
		}
	}
	if !sawSpawn {
		t.Error("code agent schema should expose spawn_agent")
	}
```
(Check `ToolDefs`' real signature — Task-1-of-phase-5 set it to `ToolDefs(gates, custom, hasSkills)`. Pass `nil` custom and `agents[0].SkillsActive()`.)

- [ ] **Step 3: Update any `TestRenderBaseConfig` string assertions**

If `TestRenderBaseConfig` asserts the rendered tree contains `lib/skills/subagents.lua` or the prompt contains `spawning-subagents`, remove/replace those. If it asserts `lib/skills/brainstorming.lua` is present, keep that. Add (optional but recommended) an assertion that the rendered `shell3.lua` contains `subagents         = true` and `confirm_destructive`, so a future template regression is caught:
```go
	if !strings.Contains(out, "subagents         = true") {
		t.Error("rendered code agent should enable subagents")
	}
	if !strings.Contains(out, "confirm_destructive") {
		t.Error("rendered config should wire the confirm_destructive ask guard")
	}
```
(Match the exact whitespace you wrote in the template; adjust the literal if your alignment differs.)

- [ ] **Step 4: Run the scaffold suite**

```bash
go test -race -count=1 ./internal/scaffold -v
```
Expected: PASS. Then `go test -race -count=1 ./internal/scaffold ./internal/bootstrap ./internal/luacfg` → PASS (bootstrap/luacfg unaffected but confirm).

- [ ] **Step 5: Full suite + commit (Tasks 1+2 together)**

```bash
go test -race -count=1 ./... && go build ./...
gofmt -w internal/scaffold
git add -A
git commit -m "feat(scaffold): retire spawning-subagents skill; enable spawn_agent builtins + confirm_destructive ask guard

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 3: Cookbook — ask-verdict recipe, drop JSONL-polling guidance

**Files:**
- Modify: `docs/cookbook/lib/guards.lua`
- Modify: `docs/cookbook/README.md`

- [ ] **Step 1: Read both files.** `docs/cookbook/lib/guards.lua` currently shows `block_destructive_bash` (a `block` recipe). `docs/cookbook/README.md` lists the recipe files.

- [ ] **Step 2: Add an `ask`-verdict recipe to `docs/cookbook/lib/guards.lua`**

Keep `block_destructive_bash`; add an `ask` example and export it, so cookbook readers see the approval flow alongside the hard-block:
```lua
-- Ask the human before a risky-but-sometimes-legitimate command, instead of
-- blocking it outright. action="ask" suspends the call until the front-end
-- answers (TUI inline y/N; bot Approve/Deny). With no approver (headless), ask
-- fails closed. The scaffold ships a confirm_destructive guard in this shape.
local function ask_before_push(call)
  if (call.tool or "") == "bash" then
    local cmd = tostring((call.params or {}).command or "")
    if cmd:match("git%s+push") then
      return { action = "ask", reason = "pushing to a remote — confirm" }
    end
  end
  return { action = "allow" }
end
```
Update the return table to export both (e.g. `return { block_destructive_bash = block_destructive_bash, ask_before_push = ask_before_push }`).

- [ ] **Step 3: Update `docs/cookbook/README.md`**

- Update the `lib/guards.lua` line to mention it shows both `block` and `ask` verdicts.
- If the README (or any cookbook doc) references the retired JSONL-polling / `bash_bg` subagent pattern, replace that guidance with a short note: subagents are now a built-in — enable `subagents = true` on an agent's `tools` table and the model calls `spawn_agent(task, agent?, workdir?)`; results return automatically (no JSONL polling). Grep the cookbook for `bash_bg`, `spawning-subagents`, `--out`, `jsonl` to find anything stale:
```bash
grep -rin "spawning-subagents\|bash_bg.*shell3\|jsonl\|--out" docs/cookbook
```
Fix any hit that describes the retired pattern; leave unrelated mentions alone.

- [ ] **Step 4: Verify + commit**

These are docs; confirm nothing references a deleted file and the cookbook guards.lua is valid Lua (it's illustrative, not loaded by tests, but keep it correct). `go test -race -count=1 ./...` should be unaffected (re-run to be safe).
```bash
git add docs/cookbook
git commit -m "docs(cookbook): add ask-verdict guard recipe; replace subagent JSONL-polling with the builtin

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## Task 4: Final docs pass — README + pkg/shell3 package doc + CHANGELOG consolidation

**Files:**
- Modify: `README.md`
- Modify/Create: `pkg/shell3/doc.go` (package doc) — or the package comment at the top of `pkg/shell3/shell3.go` if that's where the package doc currently lives
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read the current README and the existing `pkg/shell3` package doc.** Determine where the package-level doc comment lives (a `doc.go`, or atop `shell3.go`/`runtime.go`). Identify the README sections that describe the embedding API and the TUI.

- [ ] **Step 2: Update the `pkg/shell3` package doc** to cover the full agent-runtime surface a bot binary consumes. It should describe, concisely and accurately (verify each symbol exists before documenting it):
  - **Runtime/Session split:** `NewRuntime(RuntimeSpec)` → `Runtime.Session(SessionOpts)` (named sessions like `tg:1234`), `Runtime.Close`. `Start`/`Run` remain the convenient single-session wrappers.
  - **Steering:** `Session.Send`/`SendParts` (strict `ErrBusy`), `Session.Interject(text, parts...)` (never fails; mid-turn drain or idle `Wake`), `Session.RunQueued(ctx)`.
  - **Out-of-turn bus:** `Runtime.Events() <-chan HostEvent` (`HostEvent{Session, Kind, Payload}`, v1 `Wake`), and `Session.WakeEvents()` for the single-session case. Sketch the one-select-loop host pattern.
  - **Approval:** guard `ask` verdict + `SessionOpts.Approve`/`Session.SetApprover` (`ApprovalRequest`); no approver ⇒ deny.
  - **Inbound media:** `Part{Kind, Path, Data, MIME}`, `SendParts`, media on `Interject`.
  - **Subagents:** `spawn_agent`/`list_agents` builtins gated by `tools.subagents`; results to the parent inbox; depth limit 1; audit JSONL under `.shell3/agents/`.
  Keep it a doc comment (godoc-rendered), not a tutorial. Don't invent symbols — grep to confirm names/signatures.

- [ ] **Step 3: Update `README.md`** with a short section (or update the existing embedding/feature section) covering the same five capabilities at a higher level, pointing at `pkg/shell3` for the API. Mention the TUI gains (mid-turn steering, inline y/N approval, subagent auto-wake notice). Keep it proportional to the existing README's depth; don't balloon it.

- [ ] **Step 4: Consolidate the CHANGELOG.** Ensure the `## [Unreleased] / ### Added` section reads coherently across the whole agent-runtime effort (Runtime/Session split, inbox/Interject/steering, `ask` approval, inbound media, subagents + Wake bus). The phase-4 and phase-5 bullets already exist; verify earlier phases (1–3) are represented — if phases 1–3 lack changelog bullets, add concise ones so the release notes are complete. Do not duplicate; merge overlapping bullets into a clean list. Read the existing entries first and edit for coherence rather than appending blindly.

- [ ] **Step 5: Verify + commit**

```bash
go build ./... && go vet ./... && go test -race -count=1 ./...
```
(Docs-only, but a malformed `doc.go` would fail the build — so build must pass.)
```bash
git add README.md pkg/shell3 CHANGELOG.md
git commit -m "docs: document the agent-runtime surface (runtime/session, steering, approval, media, subagents)

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

- [ ] **Step 6: STOP and hand off to the user for manual acceptance.** Do not run `shell3 boot`. Report that the automated work is complete and the manual acceptance test is theirs: `rm -rf ~/.shell3 && shell3 boot`, then confirm in the TUI that (a) the `code` agent can `spawn_agent` and the result auto-arrives, and (b) a `rm -rf`/`git push --force` triggers the inline y/N approval prompt. Also surface the outstanding decision: nothing has been pushed or merged (all work is on `agent-runtime`).

---

## Self-review notes

- **Spec coverage:** retire `lib/skills/subagents.lua` + require/skills wiring ✓ (Task 1 Steps 2-6); gate builtins via `subagents = true` (code true, plan false) ✓ (Steps 5-6); prompt "Subagents" tools section replacing the skill mention ✓ (Step 4); ship example `ask` guard `guards.confirm_destructive` so boot users see the y/N flow ✓ (Step 7, wired in Steps 5-6); cookbook `ask` recipe + drop JSONL-polling ✓ (Task 3); README + package doc + CHANGELOG ✓ (Task 4); manual acceptance left to the user ✓ (Task 4 Step 6).
- **Why the test count changes:** the canonical `TestRenderedConfigLoads` loads the rendered template through the real `luacfg.Load`, so it is the regression guard for the template. Retiring one skill drops `c.Skills` 2→1; the code agent loses its `skills` block (no skills) and gains the `subagents` gate. The `plan` agent is unchanged except for the added `confirm_destructive` guard (guards aren't counted by that test). New assertions pin the gate state and that the schema actually exposes `spawn_agent` — so a future template edit that drops the gate is caught.
- **Headless safety of the example guard:** `confirm_destructive` returns `ask`; in a headless session with no approver, Phase 3 made `ask` fail closed (deny-with-reason), so spawned subagents (which run headless) can't be hung waiting on a human — they get a denial they can react to. This is the correct default and worth the inline comment in the guard.
- **No production logic changes:** every Go symbol the docs/tests reference (`ToolGates.Subagents`, `luacfg.ToolDefs(gates, custom, hasSkills)`, `Runtime.Events`, `Session.RunQueued`/`WakeEvents`/`Interject`/`SetApprover`, `Part`) already exists from Phases 1–5. Task 4 Steps verify-before-document; if a symbol named here turns out renamed, document the real name, don't add a shim.
- **Open verification for the implementer:** confirm the exact field path for the subagents gate on a loaded agent (`agents[i].Gates.Subagents` — verify `Gates` is exported on `luacfg.Agent`); confirm where the `pkg/shell3` package doc currently lives before creating a `doc.go` (avoid two package comments, which fails to compile).
