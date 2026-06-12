# Design: `wrap_bash` returns argv — a real bash wrapper

**Date:** 2026-06-12
**Status:** Approved, pending implementation plan
**Branch:** feat/bash-first

## Problem

`shell3.wrap_bash` is documented and intended as *the* bash wrapper — the one
hook that decides what happens when the agent asks to run a command. In practice
it is only a **string filter**: it inspects a command string and may return a
rewritten string, but whatever it returns is still executed as
`bash -c "<string>"` (see `runBashCapture` in `internal/chat/handler_bash.go`
and `exec.Command("bash","-c",command)` in `internal/bgjobs/bgjobs.go`).

That means you *can* forward to another runner — `return "docker exec myc bash -c " .. cmd`
— but the forwarded command lands inside an **outer `bash -c` that re-parses and
re-quotes it**. It is not a true wrapper; it cannot cleanly swap the process that
executes the command. The user's intent: `wrap_bash` should be able to forward
the bash call to an arbitrary CLI (`docker exec`, a different shell, a custom
`yourcli run …`) the way a real wrapper would.

This is one hook gaining a richer return type, **not** a new hook. `wrap_bash`
already owns "what runs when the agent asks for bash"; we let it answer fully.

## Approach

Widen `wrap_bash`'s return contract by adding one shape.

| Return value            | Meaning                                              |
| ----------------------- | ---------------------------------------------------- |
| string                  | allow; run as `bash -c <string>` (unchanged)         |
| **table (list of strings)** | **allow; exec that argv directly — no outer `bash -c`** (NEW) |
| nil / false [, reason]  | block (unchanged)                                    |

Because a table is exec'd as argv, `cmd` is passed as a **single argv element** —
nothing re-parses or re-quotes it. That is the difference between the current
string-rewrite and a real wrapper.

```lua
shell3.wrap_bash(function(cmd)
  -- block: unchanged
  if cmd:match("rm%s+%-rf%s+/") then return nil, "refusing rm -rf /" end

  -- string: unchanged — still "run under bash -c"
  -- return cmd

  -- NEW: table swaps the runner itself; cmd arrives un-re-quoted
  return {"docker", "exec", "myc", "bash", "-c", cmd}   -- sandbox in a container
  -- {"zsh", "-c", cmd}          -- swap the shell
  -- {"firejail", "--quiet", "bash", "-c", cmd}
  -- {"yourcli", "run", cmd}     -- custom CLI wrapper
end)
```

Per-command routing falls out naturally:

```lua
shell3.wrap_bash(function(cmd)
  if cmd:match("^git ") then return {"bash", "-c", cmd} end   -- git stays local
  return {"firejail", "--quiet", "bash", "-c", cmd}           -- rest sandboxed
end)
```

## Internals

### `LoadedConfig.WrapBash` (internal/luacfg/lua_bash.go)

Today's signature returns a rewritten **string**:

```go
func (c *LoadedConfig) WrapBash(ctx, cmd string) (rewritten string, allowed bool, reason string, err error)
```

Change it to return an **argv `[]string`** — the command to exec — instead of a
rewritten string:

```go
func (c *LoadedConfig) WrapBash(ctx, cmd string) (argv []string, allowed bool, reason string, err error)
```

Mapping inside `WrapBash`:

- hook returns a Lua **string** → `argv = {"bash", "-c", str}` (preserves current
  semantics)
- hook returns a Lua **table** → validate, then `argv = <the table's elements>`
- hook returns **nil / false** → `allowed = false`, optional reason (unchanged)
- hook **errors** / returns any other type → fail closed (unchanged)

### Exec sites take argv

Both sites stop hardcoding `"bash","-c",cmd` and exec the argv supplied by
`WrapBash` (or the default when no hook is declared):

- `runBashCapture` (`internal/chat/handler_bash.go`) — used by the **bash tool**
  *and* **custom command-template tools** (`internal/chat/tools.go`).
- `bgjobs.Start` (`internal/bgjobs/bgjobs.go`) — used by **bash_bg** and
  **subagents**. Its `command string` parameter becomes an argv `[]string`
  (or it gains the argv alongside, for the `bg.json` `Cmd` display — see Open
  Questions). The `SHELL3_NO_SUBAGENTS=1` env injection and process-group setup
  are unchanged.

**No hook declared** → both sites default to `{"bash","-c",cmd}`, byte-for-byte
today's behavior. Zero config required; existing configs are unaffected.

### Pipeline order

Unchanged: there is still exactly one hook. `wrap_bash` runs once per command and
its result (argv or block) drives execution directly.

## Fail-closed

`wrap_bash` remains the only bash safety surface and keeps its deny-on-doubt
discipline. The new table shape is validated; **any** of the following blocks the
command with an explanatory reason rather than exec'ing garbage:

- a Lua error in the hook
- a return that is neither string, table, nil, nor false
- an **empty** table
- a table with a **non-string** element
- a table that is not a clean 1..N list (map-style keys / holes)

A wrapper that fails open is worse than no wrapper, so the failure mode is deny —
identical to the existing contract.

## Testing

Extend `internal/luacfg/wrap_bash_test.go` and friends:

- table return → expected argv exec'd
- string return → still `bash -c <string>` (regression)
- block (nil / false, with and without reason) → unchanged
- fail-closed shapes: empty table, non-string element, map-style/holey table,
  Lua error, wrong scalar type — each blocks with a reason
- **integration:** `wrap_bash` returns an argv pointing at a recorder script;
  assert `cmd` arrives as a **single, un-re-quoted** argument (proves no outer
  `bash -c`)
- **coverage parity:** one `bash_bg` test asserting a table return applies to the
  background seam too

## Scaffold & docs

- Update the existing `wrap_bash` comment block in the scaffold `shell3.lua`
  (`internal/scaffold`) to document all three return shapes (string / table /
  nil), using the `docker exec` one-liner as the table example.
- Cookbook note: the docker/sandbox recipe.

## Open questions (resolve during planning)

- `bgjobs.Start` signature: replace `command string` with `argv []string`, or
  add `argv []string` while keeping `command` for the human-readable `Cmd` field
  in `bg.json` and the sink notification. Leaning toward passing argv for exec
  **and** a display string (the original `cmd`) so `bg.json` stays readable.
- Whether the TUI bash header (`ParseBashArgs`) needs any change — likely not,
  since it renders the model's requested command, not the post-wrap argv.

## Out of scope (YAGNI)

- No separate `bash_runner` hook. One surface.
- No per-runner timeout/env config — the argv can already invoke
  `env`/`timeout` itself if wanted.
- No Windows path (`handler_bash.go` is `//go:build unix`).
