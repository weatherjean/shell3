# Sandbox bash with `on_tool_call`

`shell3.on_tool_call` is the policy hook (see the commented block in your
`shell3.lua`). It fires before **every** tool, and `t.name` is the real tool name.
For sandboxing you care about the three bash tools — `bash`, `bash_bg`, and
`shell_interactive` — so guard on them. A handler receives `t` with `t.name`,
`t.command` (the bash command for those three tools; `nil` otherwise), and `t.args`;
it returns one of:

- `nil` — pass (continue the chain / run)
- `{ command = "..." }` — rewrite the bash command text, continue the chain
- `{ argv = { ... } }` — exec this argv directly — this swaps the **runner**, and
  the command arrives as a single argv element, so nothing re-parses or re-quotes it
- `{ block = true, reason = "..." }` — block
- `{ ask = "prompt", reason = "...", ask_timeout = N }` — ask a human (TUI `y/N` or ACP `session/request_permission`);
  allowed → run, declined/headless → block. `ask_timeout` optional (seconds, default 300).

The `{ argv = { ... } }` form is what makes `on_tool_call` a real wrapper: you
choose the program that actually runs the agent's command. It applies to `bash` and
`bash_bg`; `shell_interactive` has no argv form, so an argv verdict blocks it (see
[Scope](#scope)).

## Run every command inside a container

```lua
shell3.on_tool_call(function(t)
  if t.name == "bash" or t.name == "bash_bg" or t.name == "shell_interactive" then
    -- block first, if you like:
    if shell3.regex([[(?s)rm\s+-rf\s+/]]):match(t.command) then
      return { block = true, reason = "refusing rm -rf /" }
    end
    -- then run everything inside a container (shell_interactive has no argv form,
    -- so this blocks it — set shell_interactive = false to drop it cleanly):
    return { argv = {"docker", "exec", "mycontainer", "bash", "-c", t.command} }
  end
end)
```

Swap `docker exec …` for `ssh host`, `firejail --quiet bash -c`, `zsh -c`, or
your own `yourcli run` wrapper. A `nil` return still means "run the default
`bash -c`".

## Route per command

```lua
shell3.on_tool_call(function(t)
  if t.name == "bash" or t.name == "bash_bg" or t.name == "shell_interactive" then
    if t.command:match("^git ") then return nil end                     -- git stays local
    return { argv = {"firejail", "--quiet", "bash", "-c", t.command} }  -- rest sandboxed
  end
end)
```

## Scope

These recipes sandbox the `bash`, `bash_bg`, and `shell_interactive` tools —
including inside subagents (in-process background jobs spawned via the `task`
tool), whose bash calls fire the same gate. `on_tool_call` also fires for `read`,
`list_files`, `edit_file`, and custom tools — the `t.name` guard keeps your bash
sandboxing from touching them, and you can gate those separately by name + args (see
[configuration.md](../configuration.md#opt-in-command-gate--on_tool_call)). A custom
command-template tool's command is your trusted author template (not model input), so
it is never rewritten — but the call still fires the hook, so you can `block`/`ask` it.

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped. A runner swap has no interactive-PTY form,
so a `shell_interactive` call under an `{argv=...}` policy also fails **closed**
(blocked) — set `shell_interactive = false` for that agent if you sandbox all bash
through a runner swap.
