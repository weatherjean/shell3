# Subagent registry: explicit, discoverable subagents

Date: 2026-06-10
Status: approved (design), pending implementation plan

## Goal

Replace the `tools = { subagents = true }` boolean — which exposes `spawn_agent`
with an undiscoverable, free-form `agent` parameter the model can't meaningfully
use — with an explicit registry of named subagents. Each subagent carries a
"when to use" description; an agent opts in by listing the subagents it may
delegate to; and the model sees exactly that list (names + descriptions) on a
single `spawn_agent` tool whose `subagent` parameter is an enum.

This makes delegation a curated, vetted choice instead of a blind guess, and
mirrors how Claude Code's Task tool exposes named subagent types.

## Background (what exists today)

From the agent-runtime work (now on `main`):

- `internal/luacfg`: `shell3.agent({name, model, prompt, tools, skills,
  on_tool_call})` registers a top-level agent into `c.agents` (the Tab/`/agent`
  rotation, surfaced as `AgentNames`). Agents are referenced by **name**;
  `shell3.agent` returns no handle. `shell3.tool`/`shell3.skill`/`shell3.mcp`
  DO return handles (tables marked `__tool`/`__skill`/`__mcp`), collected into
  arrays via `handleNames(tbl, marker)`.
- `ToolGates` has a `Subagents bool` field; `tools = { subagents = true }` sets
  it; `luacfg.ToolDefs(gates, custom, hasSkills)` emits the static
  `spawnAgentTool`/`listAgentsTool` defs when `gates.Subagents` is true. The
  `spawn_agent` `agent` param is a free-form string described generically.
- `pkg/shell3.Session.spawn` creates `rt.Session(SessionOpts{Agent: name,
  WorkDir, Headless: true, DisableSubagents: true, OutPath: .shell3/agents/<id>.jsonl})`
  on the shared Runtime, runs it on a runtime-scoped goroutine, posts the result
  to the parent inbox, and Wakes an idle parent. `DisableSubagents` strips the
  spawn tools from the spawned session (depth-limit 1). `SessionConfig` →
  `AgentRuntime(name)` resolves the agent config from `c.agents`.

## Decisions (locked in brainstorming)

1. **Subagents are a separate declaration**, not top-level agents. They are
   delegatable but NOT in the Tab/`/agent` rotation.
2. **Single tool + enum**: one `spawn_agent(task, subagent, workdir?)` tool; the
   `subagent` parameter is an enum of the registered subagent names; the tool
   description lists each name + its "when to use".
3. **Strict list only**: the enum is exactly the agent's registered subagents.
   No self-spawn, no arbitrary names. An agent with no `subagents` array gets no
   `spawn_agent` tool. The `subagents = true` boolean is removed entirely.
4. **Referenced like `mcp`**: `shell3.subagent{...}` returns a handle; an agent
   lists handles under `tools = { subagents = { researcher, planner } }`.
5. `description` is **required** on `shell3.subagent`. `subagent` is a
   **required** parameter on `spawn_agent`. Declaring a nested `subagents` inside
   a subagent's tools is a **load-time error** (depth-limit 1, enforced at config
   load in addition to the runtime strip).

## Surface

### `shell3.subagent{...}` (new)

```lua
local researcher = shell3.subagent({
  name        = "researcher",
  description = "Deep multi-file investigation across the repo — finding where/how something is implemented.",
  model       = "main",
  prompt      = [[ You are a focused code researcher. ... ]],
  tools       = { bash = true, history = true },   -- agent gates; `subagents` key here is a load error
  on_tool_call = { guards.no_env_edit },
})
```

- Required: `name`, `description`. Optional: `model` (default: first declared
  model, same rule as agents), `prompt`, `tools`, `skills`, `on_tool_call`.
- Returns a handle table marked `__subagent` (name carried like `__tool`).
- Registers into a **separate registry** (`c.subagents`), keyed by name, never
  added to `c.agents`/`AgentNames`. Names must be unique within the subagent
  registry; a subagent and an agent MAY share a name (different namespaces) —
  but to avoid confusion the loader rejects a subagent whose name collides with
  a declared agent name and vice-versa (single flat namespace for humans, two
  registries internally). [Resolved: reject cross-collisions for clarity.]
- A subagent's `tools` table accepts the same gate keys as an agent EXCEPT
  `subagents` — presence of `subagents` raises a load error
  ("a subagent may not declare its own subagents").

### Agent opt-in

```lua
shell3.agent({
  name = "code",
  tools = {
    bash = true, edit = true,
    subagents = { researcher, planner },   -- array of __subagent handles
  },
})
```

- `tools.subagents` is now a **table of handles** (was a bool). Parsed via
  `handleNames(tbl, "__subagent")` into `Agent.Subagents []string` (names).
- The `Subagents bool` gate is removed from `ToolGates`.
- Each listed name must resolve to a registered subagent at load (else a load
  error naming the offender), so typos fail fast rather than at spawn time.

### Model-facing schema

When an agent's `Subagents` list is non-empty, its tool schema includes:

```
spawn_agent(task, subagent, workdir?)
  task      — the full, self-contained prompt for the subagent
  subagent  — REQUIRED enum, one of the registered names; the description lists
              each: "researcher: Deep multi-file investigation …",
                    "planner: Turn a spec into a step-by-step plan …"
  workdir   — optional working directory (absolute or relative to caller)
```

plus `list_agents()` (unchanged). The `subagent` JSON-schema property carries
`"enum": [<names>]`; the tool `Description` is built from the agent's resolved
subagents (name + description per line). An agent with an empty list gets
neither tool.

### Execution

- `spawn_agent`'s handler validates `subagent` against the enum (belt-and-suspenders
  to the schema) and passes the resolved subagent name to the runtime.
- Spawned `sub:<id>` sessions resolve their config from the **subagent
  registry**, not `c.agents`. A `SessionOpts.Subagent string` (or equivalent)
  tells `SessionConfig`/`AgentRuntime` to resolve from `c.subagents`; this keeps
  subagent names out of `/agent` switching and `AgentNames`.
- `DisableSubagents` still strips spawn tools from the spawned session
  (depth-limit 1 holds even though subagents can't declare subagents anyway).
- Result delivery (parent inbox, mid-turn inject or Wake), per-subagent audit
  JSONL under `.shell3/agents/`, and `list_agents` are unchanged.

## Components / files

- `internal/luacfg`:
  - `register.go`: `luaSubagent` (new `shell3.subagent` registration + `__subagent`
    handle); `agentKeys`/subagent key set; parse `tools.subagents` as a handle
    array (drop `optBool`); cross-namespace collision + nested-subagents load
    errors; resolve listed names at load.
  - `luacfg.go`: `Subagent` type (or reuse `Agent` + `Description`); `c.subagents`
    registry + accessor; remove `ToolGates.Subagents`.
  - `tooldefs.go`: build `spawn_agent` def with an enum `subagent` param and a
    description assembled from `[]struct{Name, Description}` (replaces the static
    gate-driven emit). `list_agents` unchanged.
  - `dispatch.go`/persona as needed for subagent prompt assembly.
- `internal/agentsetup`: resolve an agent's `Subagents` names → (Name, Description)
  for schema assembly; provide subagent-config resolution for spawning (the
  separate registry); thread into `AgentRuntime`/`SessionConfig`.
- `pkg/shell3` + `internal/chat`: `spawn_agent` handler maps the enum value to a
  registered subagent; `SessionOpts.Subagent` (or equivalent) for spawn
  resolution; remove the old free-form `agent` param handling. `SpawnRequest`
  carries the chosen subagent name. Depth-limit unchanged.
- `internal/scaffold`: declare one example subagent in `shell3.lua.tmpl` (e.g.
  `explorer`/`researcher`), wire it into the `code` agent's `tools.subagents`;
  update `scaffold_test.go` (skills/agents/subagents counts + the rendered
  config loads with the new shape).
- Docs: `pkg/shell3` package doc (the subagents section), `README.md`,
  `CHANGELOG.md`, and `docs/cookbook` if it references the boolean.

## Testing

Fakellm-driven, race-enabled, hermetic (temp HOME), matching the existing suite:

- luacfg: `shell3.subagent` registers into the subagent registry and not
  `AgentNames`; `__subagent` handle round-trips; `tools.subagents = { handle }`
  resolves to names; load errors for (a) unknown subagent name in the list,
  (b) nested `subagents` inside a subagent, (c) name collision with an agent,
  (d) missing `description`.
- tooldefs: an agent with subagents emits one `spawn_agent` whose `subagent`
  enum == the registered names and whose description contains each name +
  description; an agent with none emits neither `spawn_agent` nor `list_agents`.
- pkg/shell3: spawning a registered subagent runs a `sub:<id>` with the
  subagent's model/prompt/tools (not a top-level agent); an unknown `subagent`
  value returns an error to the model; result still posts to the parent inbox /
  Wakes; depth-limit 1 (spawned subagent has no spawn tool); subagent names do
  NOT appear in `AgentNames`/`/agent`.
- scaffold: the rendered `shell3.lua` loads, the example subagent is registered,
  and the `code` agent's schema exposes `spawn_agent` with the example in its
  enum.

**Manual acceptance (USER's):** `rm -rf ~/.shell3 && shell3 boot`, then confirm
the `code` agent can `spawn_agent` the example subagent and the result arrives.

## Non-goals

- No change to result delivery, the Wake bus, steering, media, or approval.
- No per-call "when to use" override (the description lives on the subagent, not
  the registration site) — YAGNI; revisit if a real need appears.
- No nesting / multi-level delegation (depth-limit 1 retained).
- No backward-compat shim for `subagents = true` (removed outright; this is the
  user's own project, pre-external-release).
