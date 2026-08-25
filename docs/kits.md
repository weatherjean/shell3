# Kits

A kit is one file that defines everything shell3 runs: the agent you talk to,
every employee it can dispatch, and the tools and knowledge each of them has.

```
~/.shell3/
  shell3.sh      wiring + main agent + every employee + their tools, skills and gate
  .env           secrets
  skills/        main-agent skills
  projects/<agent>/skills/   an employee's own skills
  lib/bin/       reusable glue the agent runs through bash
```

A directory holding only `shell3.sh` and `.env` is a complete, runnable config.

## Primitives

| | |
|---|---|
| **agent** | a prompt + a capability list + a workdir |
| **tool** | a declared verb: name, description, typed params, shell body |
| **skill** | knowledge the agent reads on demand — a `skills/*.md` file, not a kit block |
| **gate** | what an agent may not do — runs before every tool call |
| **note** | a remark attached to a tool's result — advice, never a refusal |
| **command** | a `/verb` the front-end answers by running a shell function — no model turn, no tokens |
| **event** | a subscriber on the session event stream — observes, never refuses |
| **mcp** | an external tool server |
| **memory** | a per-agent `memory.md` |
| **cron** | a schedule bound to an agent + prompt — or, for mechanical work, bound directly to a tool |

## Grammar

Three elements, nothing else.

1. **Declaration blocks** — YAML inside a `#---` … `#---` comment fence.
2. **Function definitions** — the implementation of the block above them.
3. **Heredocs** — prose bodies, always `<<'EOF'` so the text is literal.

A block binds to the **next function definition, whatever it is named**. The
declared name is what the model sees; the function name is an implementation
detail. That is what lets a dozen agents share one flat bash namespace.

Scoping is positional: an `agent:` or `shared:` block opens a scope, and every
`tool:` and `test:` block after it belongs to that scope until the next one
opens.

`gate:`, `note:` and `event:` are the exception — they **name** the agents they
govern and may sit anywhere in the file. One function usually governs several
agents, and the alternative (a copy per agent) is how two sets of rules drift
apart.

`command:` and `cron:` are positional like `tool:` but belong to no agent: a
command is answered by the front-end, not by a model, and a cron job names
its own target agent, so there is nothing for either to be scoped to.

`cron:` is also the one block where `agent:` and `tool:` are payload rather
than a declaration kind — they name what the job runs.

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
# description: keeps my saved links tidy
# model: main
# workdir: ~/bookmarks
# context: [memory.md]
# use: [bash, web]
#---
bm_prompt() { cat <<'EOF'
One tick = one batch of saved links. Judge each page yourself. Write what
you learned to memory.md before you finish.
EOF
}

#---
# tool: page-kind
# description: Classify a saved link — article, wiki, shop, dead
# params:
#   url:     {type: string, required: true, description: homepage URL}
#   timeout: {type: int, default: 20}
#---
bm_page_kind() {
  local html
  html=$(curl -sL --max-time "$timeout" "$url") || return 1
  if   grep -q 'id="mw-content-text"' <<<"$html"; then echo wiki
  elif grep -q 'add-to-cart'          <<<"$html"; then echo shop
  elif grep -q '<article'             <<<"$html"; then echo article
  else echo dead; fi
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

- a built-in name — `bash`, `bash_bg`, `edit`, `media`, `history`
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
url=https://example.com timeout=20 bm_page_kind
```

`bm_page_kind url=…` would pass a positional argument and leave `$url`
unset.

## Skills

A skill is a **file**, never a kit block: every `*.md` in `skills/` (and in
`projects/<agent>/skills/` for an employee) is one skill, indexed in the prompt
by name, description and absolute path. The agent `cat`s the body when it
applies — there is no `skill` tool. See
[configuration.md](configuration.md#skills--skills).

Prompt bodies are read **statically** from their heredocs. shell3 never runs a
kit to find out what it says.

## Execution

One path: run agent K with prompt P. Three dispatchers:

| Entry | K | P |
|---|---|---|
| Telegram | main | your message |
| `task` | any employee | the main agent's ask |
| cron (`agent:`) | a bound employee | the standing task |
| `task` from an employee | a peer employee | that employee's ask |

Delegation is two levels. An employee may dispatch a peer; that peer may not
dispatch again — its `task` call is refused with an error telling it to do the
work itself or report up. An employee with no peer, or one at the second
level, that genuinely needs another model call runs `shell3 ask --agent` from
bash.

Every dispatch is a fresh session. Continuity is written state: a `memory.md`
the agent keeps current, plus the `history` built-in when declared.

## Cron

A `cron:` block binds a schedule to one job. Like `command:`, it is
positional in form but scoped to nothing — it names its own target agent, so
it neither opens nor closes a scope, and it may sit anywhere in the file.

```sh
#---
# cron: discovery
# schedule: "@every 30m"
# agent: leads
#---
cron_discovery() { cat <<'EOF'
Drain one niche from the queue. Report only what changed.
EOF
}
```

`schedule` is robfig/cron syntax and is parsed at load with the same parser
the scheduler arms with, so a schedule that loads is a schedule that boots.
The function under the block holds the prompt in a heredoc, exactly as an
agent's own prompt does.

An `agent:` job's result comes back as a task report the main agent has to
read and judge, and that judgment is a full turn at live conversation
context — routinely the majority of what a frequent job costs. The judgment
is skipped entirely when the job's OWN reply ends with the `NO_REPLY`
sentinel: the completion router drops that result before any turn starts. So
say so in the prompt — *"if the run found nothing, reply with exactly
NO_REPLY"* — and keep the summary for runs that carry something. A prompt
that ends with a summary unconditionally buys a main-agent turn on every
tick, and the sentinel has to come from the JOB: the main agent answering
`NO_REPLY` after reading the report has already spent the turn.

A block names exactly one target, and it is always an `agent:`. Cron runs
agent turns only. `tool:` was a second job kind — the tool's shell function,
called directly on schedule with no model turn at all — and it was removed:
a scheduled shell call has no model in the loop to judge its result, which is
exactly where judgment leaks out of a turn and into a script nobody reviews.
It also bypassed the job runtime, so tool jobs were the one piece of
scheduled work with no concurrency cap. A kit still carrying `tool:` on a
cron block is a LOAD error naming the replacement, rather than arming
nothing.

Mechanical, idempotent work is still a job — it just keeps a model in the
loop. Declare the agent, hand it the tool, and let it read the output:

```sh
#---
# cron: sync
# schedule: "@every 30m"
# agent: syncer
#---
cron_sync() { cat <<'EOF'
Run sync-notion-recent. It is silent when nothing was inserted and nothing
failed: if it printed nothing, reply with exactly NO_REPLY. If it reported
failures, say what failed and how many.
EOF
}
```

That prompt is the whole difference. The tool still does the work; the turn
decides what its output means, and the `NO_REPLY` sentinel keeps a quiet tick
down to one cheap employee turn with no main-agent turn behind it. If a job
has no judgment in it whatsoever, it does not belong on the schedule: call
the tool from inside a tick that is already running, or give it a system
timer.

`report:` is the one axis for what the tick's finish does to the chat:
`auto` (the default) spends a main-agent turn to judge the result, `raw`
posts it straight to the chat instead and spends no turn, `always` spends the
turn and requires it to answer. The pre-`report:` spelling `direct: true` is
a load error naming its replacement, rather than a silently ignored key.

Every target resolves at load, next to the check that a `gate:` names a real
agent — an unknown agent, a duplicate job name, a malformed schedule. All of
them are load errors rather than a failed dispatch on the first tick, and
`shell3 health` inherits every one by parsing the kit.

A job's recorded history describes the RUN, not its dispatch. The scheduler
fires and learns only that the subagent was accepted, so the outcome comes
back later from the completion router: the dash's Cron table shows a job that
dispatches cleanly and fails its work as a failure. Between a firing and its
result the row still carries the previous run's verdict beside the new
timestamp — inventing one for work still in flight would be the same lie in
the other direction.

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
agents — an employee left out is a way around every rule the main agent has.

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

## Commands

`command:` declares a `/verb` the front-end answers itself. There is no model
turn and no tokens are spent — the shell function's stdout is the reply.

```sh
#---
# command: standup
# description: Yesterday's commits across my repos
#---
cmd_standup() {
  cd ~/code || exit 1
  for d in */; do
    git -C "$d" log --since=yesterday --oneline 2>/dev/null | sed "s|^|${d%/}: |"
  done
}
```

Everything typed after the verb arrives as `$ARG` (`/standup week` →
`ARG=week`). Empty output posts nothing, so an idempotent command with nothing
to say stays silent; a nonzero exit posts the failure. Declared commands join
the client's `/` autocomplete menu.

A command may not be named after a built-in (`/dash`, `/stop`, `/superstop`,
`/new`, `/run`, `/btw`, `/reload`, `/quiet`) — built-ins are matched first, so
the declaration would never fire. That is a load error rather than a silent
shadow.

## Events

`event:` subscribes a function to the session event stream. It **observes**:
its stdout is ignored, and it can neither refuse nor rewrite anything. That is
the whole difference from `gate:` and `note:`.

```sh
#---
# event: [main]
# on: [turn_done, error]
#---
ev_log() {
  cat >> ~/.shell3/events.jsonl
}
```

`on:` is **mandatory** and names the kinds this subscriber receives:
`session_end`, `user_message`, `assistant_message`, `assistant_token`,
`assistant_reasoning`, `tool_call`, `tool_result`, `error`, `usage`,
`turn_done`, `system_reminder`, `retry`, `compacted`. It is not a convenience —
`assistant_token` fires once per streamed token, so an unfiltered subscriber
would fork a shell thousands of times per turn. An unknown kind is a load
error.

The event arrives as one JSON object on stdin: `event`, `agent`, `session`,
`time`, plus whatever that kind carries (`text`, `role`, `tool`, `tool_input`,
`output`, `tool_error`, `tool_call_id`, `usage`, `meta`). Text and tool output
are capped at 4 KB — the full content is in the runs store, which is where a
hook that needs it should look.

Delivery is off the turn's critical path: events queue and a single worker runs
the subscriber one at a time, so a slow hook never stalls a turn. If the queue
fills, the **oldest** pending events are dropped and counted in the app log —
gaps in an observer's view are recoverable, a stalled turn is not.

Since a failed observer refuses nothing, it is silent at runtime (a warning in
the app log). `shell3 health` is where a broken one surfaces: it checks that
each declared command and subscriber function is defined, without running it —
dry-running a command would post the message it exists to post.

## See also

- [tools.md](tools.md) — writing a tool, its manifest, and its tests
- [cli.md](cli.md) — the command tree
- [security.md](security.md) — what the gate refuses, and what it cannot
