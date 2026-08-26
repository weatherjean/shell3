---
name: using-llms
description: Use when a model call happens outside a turn — a perception
  tool, a script, a pipeline over many rows — when an image, audio or PDF
  attachment arrives and you need to perceive it, or when debugging LLM
  output that arrives truncated, empty, duplicated, or with thinking text
  mixed into the answer.
---

# Where a model call is allowed to happen

## 1. A `tool:` converts. It never decides.

A tool may call a model to **convert between forms** — pixels, audio or PDF
into text, text into speech or an image. It never calls a model to
**decide**: no score, tier, ranking, draft or summary. Conversion returns
what was there; judgment belongs in the turn.

You are a model. You are already running. If the work is a judgment, write it
yourself and pass it to the tool as a parameter:

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

### If the work is too big for one turn

Delegate it. You have `task` — dispatch an employee, or several, and let each
one do its share in its own turn. Two levels are available. A batch of 500 rows
is batches of rows handed to agents, not a script with a model client in it.

## 2. Perception is a tool you declare

There is no built-in way to see an image or hear a voice note. A Telegram
attachment arrives as a PATH in your prompt; turning it into words is a tool
you write.

**Ask before you build.** If your own model cannot see, you do not get to pick
the one that can — ask the operator which vision model and which key, then
declare the tool, test it, and reload:

```sh
shell3 tool check ~/.shell3/shell3.sh
shell3 tool test  ~/.shell3/shell3.sh
```

The block below is the whole pattern. `transcribe` (audio), `read_pdf` (PDF),
`say` (text to speech) and image generation are the same shape with a
different endpoint and a different parameter — write them the same way when
you need them. One note if you write `read_pdf`: base64-inline PDF blows argv
limits, so write the JSON payload to a temp file and use `curl --data @file`.

Swap `model:"gpt-4o-mini"` and the `api.openai.com` URL below for whatever
the operator actually named — the block is the pattern, not a fixed
provider. Copy-pasting it unchanged ships a tool silently pointed at OpenAI
regardless of what was agreed.

```sh
#---
# tool: see
# description: Describe an image. Use on an image attachment before answering it.
# params:
#   path: {type: string, required: true, description: image file path}
#   ask:  {type: string, required: false, description: what to look for; default is a full description}
#---
main_see() {
  local key prompt payload mime
  key=$(grep -m1 '^VISION_API_KEY=' ~/.shell3/.env | cut -d= -f2-)
  [ -n "$key" ] || { echo "VISION_API_KEY missing from ~/.shell3/.env" >&2; return 1; }
  [ -f "$path" ] || { echo "no such file: $path" >&2; return 1; }
  prompt=${ask:-"Describe this image in full. Transcribe any text you see verbatim."}
  mime=$(file -b --mime-type "$path")

  payload=$(mktemp)
  trap 'rm -f "$payload"' RETURN
  jq -n --arg p "$prompt" \
        --arg u "data:$mime;base64,$(base64 < "$path" | tr -d '\n')" \
    '{model:"gpt-4o-mini", messages:[{role:"user",content:[
       {type:"text",text:$p},
       {type:"image_url",image_url:{url:$u}}]}]}' > "$payload"

  curl -sS --fail-with-body https://api.openai.com/v1/chat/completions \
    -H "Authorization: Bearer $key" -H 'Content-Type: application/json' \
    --data @"$payload" | jq -r '.choices[0].message.content'
}

#---
# test: see — surfaces a missing key
#---
main_test_see() {
  stub grep <<'STUB'
STUB
  assert_contains "$(tool see path=/tmp/x.jpg 2>&1)" "VISION_API_KEY missing"
}
```

This is a conversion, so it is allowed: it returns what the image shows. A
tool that returned "this image is safe to post" would be a decision, and that
belongs in your turn.

## 3. A standalone operator script MAY call `shell3 ask --agent`

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
- **The API key never enters your script as an environment variable.**
  shell3 passes a tool only `PATH HOME TMPDIR LANG TZ` — `.env`'s values are
  never among them, so a hand-rolled client cannot read the key off its own
  environment. Opening `.env` directly, as the `see` example in section 2
  does, is the sanctioned way to reach a secret from inside a tool; reaching
  for it as an env var instead ("works around" the restriction) is what this
  rule refuses.

## 4. If you are debugging bad model output

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
held. It never worked: the key it wanted was in `.env`, and `.env`'s values
are never among the environment variables a tool receives — reading the
file itself, as section 2's `see` example does, was the sanctioned path the
whole time. It was found on 2026-08-25, four days after the skill saying
"never write an HTTP client against a model API" had already shipped —
because that skill did not say what to do instead, and "call `ask --agent`
from the script" would have kept the judgment exactly where it did not
belong. Drafting prose is a decision, not a conversion — the rule this task
rewrote still refuses it.
