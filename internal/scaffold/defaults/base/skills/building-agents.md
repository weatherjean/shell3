---
name: building-agents
description: Use when starting a new project, adding an employee agent, or giving an existing one a new tool — how to declare agents, tools, tests and skills in shell3.sh so the agent does the judging and the tools only do the fetching.
---

# Building an agent

Everything lives in `~/.shell3/shell3.sh`. An employee is a declaration block
plus a prompt function; its tools are declaration blocks plus shell functions.

## The rule that matters most

**Tools gather facts. The agent decides.**

A tool that returns a score, a rank, a tier, or a verdict has taken the
judgment out of the turn — and a pipeline of such tools is a shell script with
a language model bolted on. Write tools that fetch, parse, query and persist.
Leave every weighing to the agent.

    site-signals  →  {"analytics": {...}, "schema": null, "products": 60}   good
    site-score    →  {"score": 72, "tier": "warm"}                          wrong

The second one decided. That decision belonged to the agent, where it could
have weighed context, noticed a contradiction, and explained itself.

## Delegation is two levels

Every agent with a peer to dispatch gets the `task` tool — employees
included. You dispatch an employee (level 1); that employee may dispatch a
peer (level 2); a level-2 agent's `task` call is refused with an error telling
it to do the work itself or report up.

The refusal matters more than the bound. An agent that finds delegation
silently missing does not stop — it improvises, and the worst version of that
is a hand-rolled HTTP client against a model API inside a tool. If you are at
the bottom of the ladder and need work you cannot do, **finish and say so**:
name what needs delegating and why, and let your parent dispatch it.

An agent is never its own target, and the main agent is never anyone's target.
A kit with exactly one employee therefore advertises no `task` tool to it —
there is nobody to dispatch.

## A new project

```sh
#---
# agent: bookmarks
# description: what this employee is for — the main agent reads this to decide when to dispatch it
# model: main
# workdir: ~/work/bookmarks
# context: [memory.md]
# use: [bash, web]
#---
bm_prompt() { cat <<'SHELL3_EOF'
What you do, and how you decide.

Tell it where it lives — its workdir, what files are there, what is already
installed. An agent that has to ls and cat its way around burns minutes of
every dispatch rediscovering what one paragraph could have told it.

Tell it what to write down before it finishes, and where.
SHELL3_EOF
}
```

`workdir` roots its shell and is where its `memory.md` and `context:` files
resolve. Skills go in `projects/<agent>/skills/*.md` with a frontmatter
`description:` — indexed into its prompt, read on demand.

## A tool

```sh
#---
# tool: fetch-thing
# description: What it returns, phrased so the model knows when to reach for it
# params:
#   url:     {type: string, required: true, description: page to fetch}
#   timeout: {type: int, default: 20}
#---
bm_fetch_thing() {
  curl -sL --max-time "$timeout" "$url"
}
```

Params arrive as **environment variables** — the body reads `$url`, never
`$1`. Types are `string`, `int`, `bool`. A description containing a comma must
be quoted, or the inline YAML splits it into keys.

Stdout is the result; a nonzero exit is an error and stderr comes back with it.
Bodies default to bash; a `#!` first line picks another interpreter, which is
the right move when parsing HTML or doing real arithmetic.

## A test

```sh
#---
# test: fetch-thing — parses the marker
#---
bm_test_fetch_thing() {
  stub curl <<'STUB'
<meta name="generator" content="WordPress">
STUB
  assert_eq "$(tool fetch-thing url=https://x.test)" wordpress
}
```

`stub NAME` installs a command that prints exactly what you pipe in — you are
not testing that curl works, you are testing your parsing. Also available:
`tool`, `assert_eq`, `assert_contains`, `fail`, `$KIT_TMP`.

## A scheduled job

```sh
#---
# cron: morning-rounds
# schedule: "@daily"
# agent: bookmarks
#---
cron_morning_rounds() { cat <<'EOF'
Check the queue and report anything that needs a decision.
EOF
}
```

`schedule` is robfig/cron syntax: `"@every 30m"`, `"@daily"`, or a 5-field
spec like `"*/30 8-22 * * *"` (every 30 min, 8am-10pm). The function under
the block is the prompt, exactly like an agent's own.

The run's result reaches the main agent as a task report, which posts an ✉️
update only when there is something worth saying. `report: raw` skips that
judgment turn and posts the raw result instead — no tokens; `report: always`
keeps the turn and requires it to answer, for a job that must be heard from
every tick. Failures always surface whichever you pick.

Cron runs AGENT TURNS ONLY. There is no `tool:` job — a kit that names one
fails to load. A scheduled shell call has no model in the loop to judge its
result, which is exactly where judgment leaks out of a turn and into a script
nobody reviews.

Mechanical work — a sync, a rotation — is still a job; it just keeps a model
in the loop. Declare the agent, hand it the tool, and say what silence means:

```sh
#---
# cron: sync-inbox
# schedule: "@every 30m"
# agent: syncer
#---
cron_sync_inbox() { cat <<'EOF'
Run sync-inbox. It prints nothing when there was nothing to sync: if it
printed nothing, reply with exactly NO_REPLY. If it failed, say what failed.
EOF
}
```

That `NO_REPLY` line is what makes a frequent tick affordable: the completion
router drops the result before any main-agent turn starts, so a quiet tick
costs one cheap employee turn and nothing else. A prompt that always signs
off with a summary, however short, buys a full main-agent turn at live
conversation context on every single tick.

If a job has no judgment in it at all, it does not belong on the schedule:
call the tool from inside a tick that is already running, or give it a system
timer.

## The loop

    shell3 tool check <kit>                 syntax, lint, manifests
    shell3 tool run   <kit> <tool> '<json>' one call, no model, no tokens
    shell3 tool test  <kit> [tool]          the declared tests

Write, check, probe against something real, pin with a test, verify. Only the
first step needs a model.

## Then watch it work

Dispatch the agent and read what it actually did:

    history {"agent": "bookmarks"}       its runs
    history {"session": "<id>"}      the transcript; tool calls show as [tool: x]

If it did the work through bash instead of its tools, or saved results whose
reasoning would fit any input, the prompt is wrong — not the model. Fix the
prompt and dispatch again.
