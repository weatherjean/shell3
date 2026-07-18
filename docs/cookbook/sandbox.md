# Sandbox bash with `on_tool_call`

`shell3.on_tool_call` fires before every tool; for sandboxing you care about
`bash` and `bash_bg`, so guard on `t.name`. The `{ argv = { … } }` verdict is
what makes it a real wrapper: you choose the program that runs the agent's
command, and the command arrives as a single argv element — nothing re-parses
or re-quotes it. Full verdict contract:
[configuration.md](../configuration.md#the-command-gate--on_tool_call).

## Run every command inside a container

```lua
shell3.on_tool_call(function(t)
  if t.name == "bash" or t.name == "bash_bg" then
    -- block first, if you like:
    if shell3.regex([[(?s)rm\s+-rf\s+/]]):match(t.command) then
      return { block = true, reason = "refusing rm -rf /" }
    end
    -- then run everything inside a container:
    return { argv = {"docker", "exec", "mycontainer", "bash", "-c", t.command} }
  end
end)
```

Swap `docker exec …` for `ssh host`, `firejail --quiet bash -c`, `zsh -c`, or
your own wrapper. A `nil` return still means "run the default `bash -c`".

## Route per command

```lua
shell3.on_tool_call(function(t)
  if t.name == "bash" or t.name == "bash_bg" then
    if t.command:match("^git ") then return nil end                     -- git stays local
    return { argv = {"firejail", "--quiet", "bash", "-c", t.command} }  -- rest sandboxed
  end
end)
```

## Scope

These recipes cover `bash`/`bash_bg` everywhere they run — including inside
subagents, whose calls fire the same gate. The `t.name` guard keeps your
sandboxing off `edit_file`, `read_media`, and host tools like
`image_generate`; gate those separately by name + args. A malformed argv
(empty, or any non-string element) fails **closed** — blocked, never run
unwrapped.
