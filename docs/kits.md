# Kits

A kit is one file that defines everything shell3 runs: the agent you talk to,
every employee it can dispatch, and the tools and knowledge each of them has.

```
~/.shell3/
  shell3.sh      wiring + main agent + every employee + their tools and skills
  .env           secrets
  cron/          schedules
  hooks/         gates
```

A directory holding only `shell3.sh` and `.env` is a complete, runnable config.

## Six primitives

| | |
|---|---|
| **agent** | a prompt + a capability list + a workdir |
| **tool** | a declared verb: name, description, typed params, shell body |
| **skill** | knowledge inlined into the agent's prompt |
| **mcp** | an external tool server |
| **memory** | a per-agent `memory.md` |
| **cron** | a schedule bound to an (agent, prompt) pair |

## Grammar

Three elements, nothing else.

1. **Declaration blocks** — YAML inside a `#---` … `#---` comment fence.
2. **Function definitions** — the implementation of the block above them.
3. **Heredocs** — prose bodies, always `<<'EOF'` so the text is literal.

A block binds to the **next function definition, whatever it is named**. The
declared name is what the model sees; the function name is an implementation
detail. That is what lets a dozen agents share one flat bash namespace.

Scoping is positional: an `agent:` or `shared:` block opens a scope, and every
`tool:`, `skill:`, and `test:` block after it belongs to that scope until the
next one opens.

The file is **definitions only** at the top level. That is what makes two things
true at once: loading a kit never executes it, and running one tool is just
sourcing the file and calling one function.

## Example

```sh
#---
# shell3:
#   models:
#     main: {base_url: "https://api.example.com/v1", api_key: env:API_KEY, model: some-model}
#   telegram: {token: env:TELEGRAM_TOKEN, chat_id: "123456"}
#---

#---
# agent: main
# model: main
#---
main_prompt() { cat <<'EOF'
You are the agent I talk to. You do work directly, or dispatch an employee.
EOF
}

#---
# agent: leads
# description: finds UK/IE shops that need SEO help
# model: main
# workdir: ~/leads
# use: [bash, web]
#---
leads_prompt() { cat <<'EOF'
One tick = one niche. Judge each candidate yourself. Write what you
learned to memory.md before you finish.
EOF
}

#---
# tool: stack-check
# description: Classify a site's stack — wp_wc, shopify, wp_only, none
# params:
#   url:     {type: string, required: true, description: homepage URL}
#   timeout: {type: int, default: 20}
#---
leads_stack_check() {
  local html
  html=$(curl -sL --max-time "$timeout" "$url") || return 1
  if   grep -q 'cdn\.shopify\.com'     <<<"$html"; then echo shopify
  elif grep -q '/plugins/woocommerce/' <<<"$html"; then echo wp_wc
  elif grep -q 'wp-content'            <<<"$html"; then echo wp_only
  else echo none; fi
}

#---
# skill: qualify
#---
leads_qualify() { cat <<'EOF'
A real shop has products with prices, a cart, a company name in the footer.
An agency has case studies and no cart. Reject agencies and marketplaces.
EOF
}

#---
# shared: web
#---
#---
# tool: search
# description: Search the web
# params:
#   q: {type: string, required: true}
#---
web_search() { searx_query "$q"; }
```

## Capabilities

**The main agent gets every built-in, always** — it is bash-first because you
are steering it in real time. The first agent declared in the kit is the main
agent.

**An employee gets nothing by default.** Every capability is declared, and a
well-scoped employee often needs no shell at all.

One `use:` list, resolved in order:

- a built-in name — `bash`, `bash_bg`, `edit`, `read`, `list_files`, `media`,
  `history`
- `mcp:<server>` — that server's tools
- anything else — a shared group declared in the kit

An entry matching none of those is a load error. A typo in `use:` must not
silently mean "no capability".

## Calling convention

Declared params reach the shell function as **environment variables**. shell3
validates the model's arguments against the manifest — required present, types
coerced, defaults filled — then calls the function with each param exported.
The body reads `$url` and `$timeout` and takes no positional arguments.

Debugging by hand uses the same mechanism, with the assignment as a prefix:

```sh
source shell3.sh
url=https://example.com timeout=20 leads_stack_check
```

`leads_stack_check url=…` would pass a positional argument and leave `$url`
unset.

## Skills

A skill is prose, and it is **inlined into the agent's system prompt** under
`## Skills`. Kit skills are the agent's own knowledge and are small by
construction, so there is no read-it-yourself indirection and no `skill` tool.

Skill and prompt bodies are read **statically** from their heredocs. shell3
never runs a kit to find out what it says.

## Execution

One path: run agent K with prompt P. Three dispatchers:

| Entry | K | P |
|---|---|---|
| Telegram | main | your message |
| `task` | any employee | the main agent's ask |
| cron | a bound employee | the standing task |
| `shell3 ask --kit <file>` | any kit file | argv or stdin |

Delegation is one level. An employee that needs help runs `shell3 ask` from
bash.

Every dispatch is a fresh session. Continuity is written state: a `memory.md`
the agent keeps current, plus the `history` built-in when declared.

## Cron

Schedules stay their own surface, because a schedule binds an (agent, prompt)
pair — one employee commonly has several standing tasks.

```
cron/discovery.md
---
schedule: "@every 30m"
agent: leads
---
Drain one niche from the queue. Report only what changed.
```

## See also

- [tools.md](tools.md) — writing a tool, its manifest, and its tests
- [cli.md](cli.md) — the command tree
- [security.md](security.md) — the hook gate
