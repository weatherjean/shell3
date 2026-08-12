---
name: using-llms
description: Use when a script or pipeline needs a model call per item — drafting, classifying, extracting, summarising over many rows — or when debugging LLM output that arrives truncated, empty, duplicated, or with thinking/reasoning text mixed into the answer. Covers max_tokens and finish_reason=length problems.
---

# Calling a model from a script

**Never write an HTTP client against a model API.** Call the agent instead:

```bash
shell3 ask --agent <subagent> -p "$prompt"   # stdout = the reply, nothing else
```

Define the persona once as `agents/<name>.md`; the script owns only its loop —
the database walk, the queue, idempotency, retries. Diagnostics and errors go
to stderr, so stdout is safe to consume directly. A failed or empty run exits
non-zero.

## Why this and not `curl`

Shelling out here runs the same adapter the chat agent runs on, so the call
inherits things a hand-written client does not have:

- **Reasoning stays on its own channel.** Thinking never lands in the answer
  text, so there is nothing to strip and no `<think>` regex to write.
- **Truncation is detected and reported** as a non-zero exit, instead of
  silently yielding an empty or half-written result.
- **The tool-call hook still gates every tool**, and the turn can use tools.
- **The API key never enters your script** or its environment — shell3 resolves
  it at point of use.

## If you are debugging bad model output

Symptoms that mean you are on the wrong path entirely — a hand-rolled client
rather than the above:

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
terminate. Route the call through `shell3 ask --agent` and the class of bug
disappears.

## Choosing the persona

One subagent per job, not one for everything. Give it the narrowest tools it
needs — usually `tools: []`, since drafting or classifying from a prompt needs
none, and an empty tool list is both faster and safer when the prompt is built
from text you scraped rather than wrote.
