# Sandbox bash with `wrap_bash`

`shell3.wrap_bash` is the single bash-safety hook (see the block in your
`shell3.lua`). It receives the command string and returns one of:

- a **string** → run it under `bash -c` (rewrite the command text),
- a **table** (list of strings) → an argv list exec'd directly — this swaps the
  **runner**, and the command arrives as a single argv element, so nothing
  re-parses or re-quotes it,
- **nil / false[, reason]** → block.

The table form is what makes `wrap_bash` a real wrapper: you choose the program
that actually runs the agent's command.

## Run every command inside a container

```lua
shell3.wrap_bash(function(cmd)
  -- block first, if you like:
  if cmd:match("rm%s+%-rf%s+/") then return nil, "refusing rm -rf /" end
  -- then run everything inside a container:
  return {"docker", "exec", "mycontainer", "bash", "-c", cmd}
end)
```

Swap `docker exec …` for `ssh host`, `firejail --quiet bash -c`, `zsh -c`, or
your own `yourcli run` wrapper. A string return still means "run under `bash -c`".

## Route per command

```lua
shell3.wrap_bash(function(cmd)
  if cmd:match("^git ") then return {"bash", "-c", cmd} end   -- git stays local
  return {"firejail", "--quiet", "bash", "-c", cmd}           -- rest sandboxed
end)
```

## Scope

This applies to the `bash` and `bash_bg` tools (and subagents, which run via
`bash_bg`). Custom command-template tools (`shell3.tool{ command=... }`) bypass
`wrap_bash` by design — the command is your trusted author template, not model
input — so bake any sandbox directly into the tool's own command string.

A malformed argv table (empty, or any non-string element) fails **closed**: the
command is blocked, never run unwrapped.
