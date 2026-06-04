# Multi-agent configs with Tab switching

**Date:** 2026-06-04
**Status:** Approved (design)

## Summary

Let a single `shell3.lua` register **multiple agents**, each a complete working
setup (model + system prompt + tool gates + guards + skills). The user switches
the active agent at runtime with **Tab** (when the model is not busy) or the
**`/agent`** command. The active agent's key is shown in the status bar where
the persona name (`base`) is shown today.

This generalizes the existing single-agent model into a keyed collection. It
gives shell3 the equivalent of opencode's plan-vs-build modes — but composed
from resources already shared in the file (models, tools, skills), with no
per-agent duplication.

`/model` is **removed**: each agent owns its model, so switching agent is the
single control for changing the working setup (model + behavior together).

## Motivation

opencode implements "plan" vs "build" as two agents that differ only in a
permission ruleset, switched by stamping the next message with an agent key.
shell3 already has the pieces:

- `internal/luacfg.LoadedConfig` already holds `Models []Model`,
  `Tools map[string]CustomTool`, and `Skills []Skill` as **shared, multi-valued**
  collections referenced by name.
- The **only** singular field is `Agent Agent`; `luaAgent` overwrites it on each
  call (`register.go:117-119`).
- A `guard` chain (`OnToolCall`, `dispatch.go:20-44`, invoked at
  `turn.go:241-258`) already gates every tool call with allow/block/cancel —
  the enforcement hook opencode had to build.

So the feature reduces to: make `Agent` a keyed collection and let the user swap
the active one in-memory.

## Design decisions

| Decision | Choice |
|---|---|
| Subconfig shape | A full registered agent (model + prompt + tools + guards + skills), composed from shared file-level resources. |
| Declaration | Repeat `shell3.agent({name=...})`; calls accumulate. **First declared = active at startup.** |
| Key | The agent's `name` field; must be unique; shown in the status bar. |
| Switch semantics | **Continue, keep history.** New agent takes over from the next turn. |
| Per-agent model | Each agent owns its model. `/model` is removed. Omitted `model` falls back to the first declared model. |
| Switch triggers | **Tab** (only when not busy) and **`/agent [name]`** command. |
| Context overflow on switch | If the switched-to agent's model has a smaller context window than current usage, the next turn errors naturally. No special handling. |
| Back-compat | A single `shell3.agent()` file behaves exactly as today (one-entry collection, it is active). |

## Lua surface

`shell3.agent()` becomes additive. Example:

```lua
shell3.agent({
  name   = "build",          -- key shown in the bar; must be unique
  model  = "opus",           -- each agent owns its model
  prompt = build_prompt,
  tools  = { bash = true, edit = true, memory = true },
  on_tool_call = { guard_dangerous },
})

shell3.agent({
  name   = "plan",
  model  = "haiku",          -- may differ → Tab also swaps the LLM
  prompt = plan_prompt,
  tools  = { bash = true, edit = false },     -- read-only-ish
  on_tool_call = { guard_dangerous, guard_block_writes },
})
```

Rules:

- Models / tools / skills remain shared file-level resources referenced by name —
  no duplication across agents.
- `model` omitted → fall back to the first declared model.
- Duplicate `name` → **load error**.
- A single `shell3.agent()` file works unchanged.

## Components

### 1. luacfg data model (`internal/luacfg`)

- `LoadedConfig.Agent Agent` → `Agents []Agent` (declaration order) +
  `activeIdx int`.
- `luaAgent` (`register.go:112`) appends instead of overwriting; errors on
  duplicate `name`.
- New accessors:
  - `Active() Agent` — current agent.
  - `NextAgent() Agent` — cycle to the next agent with wraparound; advances
    `activeIdx`.
  - `SwitchAgent(name string) (Agent, error)` — set active by name; error if
    unknown.
- `Agents` is read-only after load. `activeIdx` is the only mutable field,
  guarded by the existing `LoadedConfig.mu` (consistent with the single-VM
  invariant).

### 2. agentsetup refactor (`internal/agentsetup`)

Today the builder constructs persona, `ToolGuard`, `ModeLabel`, active skills,
and model wiring from the single `b.lc.Agent` (around `agentsetup.go:211-251`).

Extract a pure builder:

```
buildAgentRuntime(agent Agent) → {
    Personality   persona.Persona
    ToolGuard     func(ctx, tool, params) (decision, reason, error)
    ModeLabel     string         // = agent.Name
    ActiveSkills  []string
    LLM           llm client
    Params        params
    ContextWindow int
    StatusLine    string
}
```

Called once at startup for the active agent, and again on each switch. All work
is in-memory: **no `.env` re-read, no Lua re-parse, no skill re-index, no
LLM client teardown beyond what the existing model-swap already does.**

### 3. TUI switching (`internal/tui`)

- **Tab key**: bind in the patchapp input loop, gated on `!busy` (the app
  already tracks busy via `SetBusy`). Handler:
  1. `agent := cfg.NextAgent()`
  2. `rt := buildAgentRuntime(agent)`
  3. mutate `cfg.{Personality, ToolGuard, ModeLabel, ActiveSkills, LLM, Params,
     ContextWindow, StatusLine}` — the same fields `/model` already mutates at
     `interactive.go:657-663`.
  4. refresh UI: `app.SetMode(agent.Name)`, `app.SetStatus(cfg.StatusLine)`,
     `app.SetContextWindow(rt.ContextWindow)`.
  - History is untouched.
  - If only one agent is configured, Tab is a no-op (optional one-line dim hint).

- **`/agent [name]` command**: register alongside the other slash commands
  (model the handler on the existing `/model` block at `interactive.go:630-666`).
  - No arg → list agents, marking the active one.
  - With arg → `cfg.SwitchAgent(name)`, then the same runtime rebuild + UI
    refresh as Tab. Unknown name → dim error line.

- **Remove `/model`**: delete the slash command (`interactive.go:630-666`) and
  the now-unused `cfg.SwitchModel` plumbing. `/info` continues to show the
  active agent and its model.

### 4. patchapp (`internal/patchapp`)

- Add `func (a *App) SetMode(name string)` to update the agent badge live
  (mirrors `SetStatus`, `app.go:194`). The badge is the `mode` field rendered at
  `status.go:34`.

## Data flow

```
shell3.lua: shell3.agent{build}, shell3.agent{plan}
        │  (Load)
        ▼
LoadedConfig.Agents = [build, plan], activeIdx = 0
        │  (startup: buildAgentRuntime(build))
        ▼
chat.Config { Personality, ToolGuard, LLM, ... }  ── badge "build"
        │
   user presses Tab  /  /agent plan      (only when !busy)
        ▼
NextAgent()/SwitchAgent("plan")  →  activeIdx = 1
        │  buildAgentRuntime(plan)
        ▼
chat.Config mutated in place; history preserved  ── badge "plan"
        │
   next turn runs under plan (its model, prompt, tools, guards)
```

## Error handling

- Duplicate agent `name` at load → fatal config error with the offending name.
- `/agent <unknown>` → dim error line, no state change.
- Switching to an agent whose model context is smaller than current usage → the
  next turn errors through the normal path. No pre-check.
- Tab while busy → ignored (no switch).

## Testing

luacfg:
- Multiple `shell3.agent()` calls accumulate in order; first is active.
- Duplicate `name` → load error.
- Single-agent file → one-entry collection, active (back-compat).
- `model` omitted → resolves to the first declared model.
- `NextAgent` cycles with wraparound; `SwitchAgent` by name; unknown name errors.

agentsetup:
- `buildAgentRuntime` produces a persona/guard/model bundle matching the agent.

tui:
- Tab switches active agent and refreshes badge/status/context window.
- Tab while busy is a no-op.
- `/agent` with no arg lists agents; with a valid name switches; unknown errors.
- Single-agent config → Tab no-op.

## Library (pkg/shell3) parity

The embeddable library API must be **1:1** with the TUI: agent switching, no model
switching. Accordingly:

- `pkg/shell3.Session.SwitchModel` is **removed** (along with `chat.Config.SwitchModel`,
  the `chat.ActiveModel` type, and the agentsetup `switchModel` closure — all now dead).
- `pkg/shell3.Session.SwitchAgent(name) error` is **added**, mirroring the TUI's
  `applyAgent`: it swaps the model client, system prompt, tools, guards, custom-tool
  routing, skills, status line, and context window, keeping history. Call between turns.
- `Session.AgentNames() []string` and `Session.ActiveAgent() string` let an embedder
  list/cycle agents (Tab-style) programmatically.

## Out of scope

- Per-path / fine-grained permission rules (opencode-style declarative ruleset).
  shell3's guard chain remains the enforcement mechanism; "read-only" agents are
  expressed via tool gates + a block-writes guard.
- Persisting the active agent across sessions.
- Any change to non-interactive (`once`/headless) paths beyond using the active
  (first) agent.
