# Sandbox bash with the tool-call hook

The tool-call hook fires before every tool; for sandboxing you care about
`bash` and `bash_bg`, so guard on the payload's `name`. The `{"argv": […]}`
verdict is what makes it a real wrapper: you choose the program that runs the
agent's command, and the command arrives as a single argv element — nothing
re-parses or re-quotes it. Full verdict contract:
[configuration.md](../configuration.md#the-command-gate--hookssh).

## Run every command inside a container

Your kit's `gate:` function (needs `jq`):

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  # block first, if you like:
  case "$cmd" in
    *'rm -rf /'*) printf '{"block": true, "reason": "refusing rm -rf /"}'; exit 0 ;;
  esac
  # then run everything inside a container:
  jq -cn --arg cmd "$cmd" '{"argv": ["docker", "exec", "mycontainer", "bash", "-c", $cmd]}'
  exit 0
fi
exit 0
```

Swap `docker exec …` for `ssh host`, `firejail --quiet bash -c`, `zsh -c`, or
your own wrapper. Empty output still means "run the default `bash -c`".

## Route per command

```bash
in=$(cat)
name=$(printf '%s' "$in" | jq -r .name)
cmd=$(printf '%s' "$in" | jq -r '.command // empty')
if [ "$name" = "bash" ] || [ "$name" = "bash_bg" ]; then
  case "$cmd" in
    git\ *) exit 0 ;;   # git stays local
  esac
  jq -cn --arg cmd "$cmd" '{"argv": ["firejail", "--quiet", "bash", "-c", $cmd]}'
fi
exit 0
```

## Scope

Gates are per-agent, but one `gate:` block can name several
(`gate: [main, assistant]`) — list every agent the sandbox should cover, or
declare a second function for the ones that differ; an agent no `gate:` names
runs unsandboxed. The `name` guard keeps
your sandboxing off `edit_file`, `read_media`, and host tools like
`send_media_telegram`; gate those separately by name + args. A malformed argv
(empty, or any empty element) fails **closed** — blocked, never run
unwrapped.
