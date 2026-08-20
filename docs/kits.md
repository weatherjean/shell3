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

## Primitives

| | |
|---|---|
| **agent** | a prompt + a capability list + a workdir |
| **tool** | a declared verb: name, description, typed params, shell body |
| **skill** | knowledge the agent reads on demand |
| **gate** | what an agent may not do — runs before every tool call |
| **note** | a remark attached to a tool's result — advice, never a refusal |
| **mcp** | an external tool server |
| **memory** | a per-agent `memory.md` |
| **cron** | a schedule bound to an (agent, prompt) pair — or, for mechanical work, a schedule bound directly to a tool |

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

`gate:` and `note:` are the exception — they **name** the agents they govern
and may sit anywhere in the file. One function usually governs several agents,
and the alternative (a copy per agent) is how two sets of rules drift apart.

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
# context: [memory.md]
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

## Memory — `context:`

`context: [memory.md]` gives an agent a standing brain: the listed files are
re-read into its system prompt **at the start of every turn**, resolved
against the agent's own `workdir:` when it declares one. The agent maintains
them with `edit_file`, so what it learned on one tick is there on the next.

Mind the size. Re-read every turn means a context file's cost is paid many
times per run, and the agent that writes it cannot see what it costs — an
append-only brain file is a bill that grows on its own. Over **64 KB** the
middle is elided from the prompt (head and tail kept, with a marker naming
the file); over **32 KB** `shell3 health` says so.

Keep the durable conclusions in the context file and put per-run logs in a
sibling file that is *not* listed, which the agent can `rg` when it needs the
detail.

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
| cron (`agent:`) | a bound employee | the standing task |
| cron (`tool:`) | — no agent, no model turn | a declared tool's shell function, called directly |

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

Frontmatter takes exactly one of `agent:` or `tool:` — a job is either a
prompt or a tool call, never both, never neither. `tool:` names a declared
tool and skips the agent entirely: no dispatch, no subagent, no model turn at
all — just the tool's shell function, called directly on schedule. This is
the valve for mechanical, idempotent work (a sync, a rotation) where a
prompt job's whole cost is the turn spent judging its own output:

```
cron/sync.md
---
schedule: "@every 30m"
tool: sync-notion-recent
---
```

A tool job takes no body (there is no prompt) and no arguments (the tool
runs with none, so a tool declaring a *required* param can never be used
this way — `shell3 health` catches it). It honours `workdir:` like a prompt
job does. With no model turn around to relay its result, it posts for
itself: silent on an empty result or the `NO_REPLY` sentinel (the point of
scheduling an idempotent sync often), `⏰ <job>: <result>` otherwise, and
`⚠️ <job> failed: <error>` on error — capped at 120s, the same limit a
foreground `bash` call gets. Resolution is whole-kit: `tool:` names no
agent, so the lookup searches every agent's declared tools plus every shared
group, not just one agent's capability list — the operator scheduling a
tool they declared themselves is the trust boundary, not `use:` scoping.
The duplicate-name check at `tool:` declaration time is per-scope only (two
agents may each legally declare a tool called the same thing), so this
whole-kit lookup resolves first-match-wins (agents in declaration order,
then shared groups) when a name collides across scopes — `shell3 health`
is what actually catches this: it counts every scope that declares the
name a cron job requests and fails, naming all of them, rather than let
the job silently run whichever function happened to parse first.

## The gate

`gate:` runs before every tool call for the agents it names. Its stdin is
`{"name","command","args","headless"}`; printing `{}` runs the call,
`{"block":true,"reason":"…"}` refuses it, and `{"review":true,"reason":"…"}`
soft-denies: an LLM guardian (`review_model` in the wiring block, default =
the main model) assesses the command and approves or blocks — bash tools
only, and with no reviewer resolvable it fails closed. Use `review` for
judgment-call rules with real false positives; keep `block` for the
irreversible. A nonzero exit, malformed JSON, or a 10-second timeout fails
closed.

```sh
#---
# gate: [main, assistant]
#---
main_gate() {
  in=$(cat)
  cmd=$(printf '%s' "$in" | jq -r '.command // empty')
  case "$cmd" in
    *"rm -rf /"|*mkfs*) printf '{"block":true,"reason":"that destroys the machine"}'; exit 0 ;;
  esac
  printf '{}'
}
```

An agent no `gate:` names runs ungated, and there is no fallback between
agents — a subagent left out is a way around every rule the main agent has.

**What a gate is worth.** The agent can rewrite it in two lines of Python;
matching shell text stops an honest mistake, not an agent that means to get
around it. If you want the gate to hold, make the file unwritable by the agent:
run shell3 as a user that does not own it, or set the immutable flag
(`chflags uchg` on macOS, `chattr +i` on Linux). Isolation is a container, a
VM, or a separate user — never a regex.

Keep it short. A gate that refuses ordinary work does not teach an agent where
the boundary is; it teaches it that the whole subject is forbidden.

## Notes

`note:` sees a tool's result and may rewrite it: stdin
`{"name","args","output"}`, stdout `{"output": "…"}`. Use it to redact, or to
attach advice the agent reads at the moment it decides what to do next —
without refusing anything.

```sh
#---
# note: main
#---
main_note() {
  in=$(cat)
  printf '%s' "$in" | jq -c '{output: ((.output // "") + "\n\n[note] …")}'
}
```

A failure here fails closed too: the output is replaced by an error notice
rather than passed through unfiltered. Pass the original output through on
every path you did not mean to touch.

## See also

- [tools.md](tools.md) — writing a tool, its manifest, and its tests
- [cli.md](cli.md) — the command tree
- [security.md](security.md) — what the gate refuses, and what it cannot
