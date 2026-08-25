---
name: using-llms
description: Use when you are about to make a model call from anywhere that is not a turn — a tool, a script, a pipeline over many rows — or when debugging LLM output that arrives truncated, empty, duplicated, or with thinking/reasoning text mixed into the answer.
---

# Where a model call is allowed to happen

Two rules, in order. The first one settles most cases.

## 1. A `tool:` NEVER calls a model

Not `curl`, not `urllib`, and **not `shell3 ask --agent` either**. A tool that
reaches for a model is doing judgment, and judgment belongs in the turn that
called the tool.

You are a model. You are already running. Write the thing yourself and pass it
to the tool as a parameter:

```sh
#---
# tool: tip-generate
# description: Store YOUR tip - you write the body, in your words. This tool
#   only persists it.
# params:
#   body: {type: string, required: true, description: "the full post YOU wrote"}
#---
```

That is the `lead-save` / `draft-save` / `tip-generate` shape: the tool
validates and persists, the agent decides. A tool whose description promises a
score, a tier, a ranking, a draft or a summary has taken a decision out of a
turn.

Swapping `urllib` for `shell3 ask --agent` inside a tool does **not** fix this.
The key stops leaking, and everything else stays wrong.

### If the work is too big for one turn

Delegate it. You have `task` — dispatch an employee, or several, and let each
one do its share in its own turn. Two levels are available. A batch of 500 rows
is batches of rows handed to agents, not a script with a model client in it.

## 2. A standalone operator script MAY call `shell3 ask --agent`

The exception is a script that is **not wired to any tool** — a batch job the
operator runs by hand, a migration, a one-off sweep over a table:

```bash
shell3 ask --agent <employee> -p "$prompt"   # stdout = the reply, nothing else
```

Diagnostics go to stderr, so stdout is safe to consume. A failed or empty run
exits non-zero.

**It is fast.** Measured on a live install: about 1s for a trivial turn, 4s for a
200-word prose draft. It fits inside the 120s foreground bash cap and inside
any sane subprocess timeout. If you were about to hand-roll a client because
"the agent call would be too slow", you were guessing — go and measure.

### Why this and not `curl`

Shelling out runs the same adapter the chat agent runs on:

- **Reasoning stays on its own channel.** Thinking never lands in the answer
  text, so there is nothing to strip and no `<think>` regex to write.
- **Truncation is detected and reported** as a non-zero exit, instead of
  silently yielding an empty or half-written result.
- **The gate still runs**, and the turn can use tools.
- **The API key never enters your script.** shell3 passes a tool only
  `PATH HOME TMPDIR LANG TZ` — a hand-rolled client cannot read `.env`, and a
  script that "works around" that is defeating the point of the restriction.

## If you are debugging bad model output

These symptoms mean you are on a hand-rolled client and should not be:

- `finish_reason=length` on a large fraction of calls
- "empty response" after stripping thinking/reasoning tokens
- the model's reasoning appearing inside the answer, or the answer duplicated
- output that parses one run and fails the next with the same prompt

**Do not fix these by tuning `max_tokens`.** On a reasoning model, thinking and
answer share one budget and the model decides how much to think, so any cap is
a guess against a moving target: raise it and the model thinks more, lower it
and it is cut off mid-thought. A stripper that then finds an opening tag with
no closing tag deletes the answer along with the reasoning — which reads as
"empty response" and sends you back to tuning the cap. That loop does not
terminate.

## What this rule is made of

On 2026-08-20 a script under a tool drafted LinkedIn prose with a `urllib`
client against a model API, from a copy of a skill the calling agent already
held. It never worked: the key it wanted was in `.env`, which tools do not
receive. It was found on 2026-08-25, four days after the skill saying "never
write an HTTP client against a model API" had already shipped — because that
skill did not say what to do instead, and "call `ask --agent` from the script"
would have kept the judgment exactly where it did not belong.
