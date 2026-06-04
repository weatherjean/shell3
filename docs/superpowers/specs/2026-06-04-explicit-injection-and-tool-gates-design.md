# Explicit prompt injection + gateable always-on tools

**Date:** 2026-06-04
**Status:** Approved (design)

## Summary

Make every part of an agent's system prompt and tool set **explicit opt-in**, so
a `shell3.lua` can declare a "pure text" agent — no engine-injected prompt
blocks, no tools at all. Today two things are forced on every agent regardless
of config:

- **Prompt blocks** (`persona.go` `BuildPersona`): the `## Environment` block
  (workdir/model/time) is always appended; `## Core memories` is appended
  whenever core memories exist.
- **Tools** (`tooldefs.go` `ToolDefs`): `prune_tool_result` and
  `compact_history` are hardcoded as always-on, ahead of any gate.

This change turns all four into opt-in flags (default `false`), matching how
every other tool gate already works (`bash`, `edit`, … all default off). There
are no agents in the wild yet, so flipping these defaults now avoids a breaking
change later.

## Decisions

| Decision | Choice |
|---|---|
| Default for new flags | **Opt-in (default `false`)** — explicit, uniform with existing tool gates. |
| Granularity | **Granular only** — independent flags; no `text_only` shortcut. |
| Prompt flags location | Top-level agent keys `environment`, `core_memories` (prompt stays a plain string). |
| Tool flags location | Inside the existing `tools` gate table: `prune`, `compact`. |
| Backward compatibility | Intentionally breaking: defaults flip. Scaffold, home config, and tests updated to opt in where they need the old behavior. |
| Skills block | Already opt-in via the `skills` list — unchanged. |
| In-conversation reminders | Out of scope (separate from the system prompt). |

## Lua surface

```lua
shell3.agent({
  name = "base",
  model = "main",
  prompt = [[ ... ]],

  environment   = true,   -- inject "## Environment" (workdir/model/time)
  core_memories = true,   -- inject "## Core memories" (only if any exist)

  tools = {
    bash = true, edit = true, memory = true, history = true, docs = true,
    prune   = true,        -- expose prune_tool_result
    compact = true,        -- expose compact_history
    custom  = { web_fetch },
  },

  skills = { ... },        -- skills block stays opt-in via this list
})
```

Pure-text agent:

```lua
shell3.agent({ name = "chat", model = "flash", prompt = "...", tools = {} })
```

Result: system prompt = the verbatim `prompt` only (no Environment, no core
memories, no skills); tool schema = empty.

## Components

### 1. luacfg data model (`internal/luacfg/luacfg.go`)

- `ToolGates` gains `Prune bool`, `Compact bool`.
- `Agent` gains `Environment bool`, `CoreMemories bool`.

### 2. Lua parsing (`internal/luacfg/register.go`)

- `agentKeys` += `environment`, `core_memories`.
- `toolGateKeys` += `prune`, `compact`.
- `luaAgent` reads `environment`/`core_memories` via `optBool(opts, …)` and
  `prune`/`compact` via `optBool(tt, …)` in the tools block.

### 3. Tool schema (`internal/luacfg/tooldefs.go`)

`ToolDefs` no longer hardcodes prune/compact. New order (preserving prune,
compact first when enabled):

```go
defs := []llm.ToolDefinition{}
if g.Prune {
    defs = append(defs, pruneToolResultTool)
}
if g.Compact {
    defs = append(defs, compactHistoryTool)
}
if hasSkills {
    defs = append(defs, skillTool)
}
// … existing gated tools unchanged …
```

The chat-loop handler map (`chat.NewHandlers`) still registers the prune handler
unconditionally; that is harmless because the LLM only ever calls a tool that is
present in the schema. No handler-map change needed.

### 4. Prompt assembly (`internal/luacfg/persona.go`)

`BuildPersona` gates the two blocks:

```go
func (c *LoadedConfig) BuildPersona(rd RuntimeData) string {
    a := c.Active()
    var b strings.Builder
    b.WriteString(a.Prompt)
    if a.Environment {
        fmt.Fprintf(&b, "\n\n## Environment\n- Workdir: %s\n- Model: %s\n- Time: %s\n", rd.CWD, rd.Model, rd.Time)
    }
    if a.CoreMemories && len(rd.CoreMemories) > 0 {
        b.WriteString("\n## Core memories\n")
        for _, m := range rd.CoreMemories {
            fmt.Fprintf(&b, "- %s: %s\n", m.Key, m.Value)
        }
    }
    if a.SkillsActive() {
        // unchanged
    }
    return b.String()
}
```

### 5. Scaffold (`internal/scaffold/defaults/shell3.lua`)

The `base` and `plan` agents set `environment = true`, `core_memories = true`,
and `prune = true`, `compact = true` in their tools tables — preserving today's
behavior under the new explicit defaults.

### 6. Docs (`internal/docs/shell3.md`)

Document the new agent keys (`environment`, `core_memories`) and tool gates
(`prune`, `compact`) in the `shell3.agent` reference, noting all default off.

### 7. Home config (post-merge step, not in the repo)

`~/.shell3/shell3.lua`: the `base` agent gains the four `= true` flags; the
existing `chat` agent stays bare (already `tools = {}`, no prompt flags) so it
becomes the pure-text test case.

## Testing

luacfg:
- `prune`/`compact` gates: present in `ToolDefs` only when enabled; absent by
  default; ordering (prune, compact, skill, …) preserved when enabled.
- `environment`/`core_memories`: `BuildPersona` includes each block only when its
  flag is true; a bare agent yields prompt == verbatim `prompt` text.
- A `tools = {}` agent with no prompt flags produces an empty tool schema and a
  prompt with no engine blocks.

scaffold:
- The embedded default still loads; `base` exposes prune/compact and the
  Environment block (assert via `ToolDefs`/`BuildPersona`).

Update any existing tests that assumed prune/compact were always present or that
the Environment block was unconditional.

## Out of scope

- A `text_only`/`bare` convenience flag (granular flags compose).
- Toggling in-conversation context-usage reminders.
- Per-path / fine-grained tool permissions.
