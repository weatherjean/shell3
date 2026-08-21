---
name: self-evolve
description: Use when the user tells you to remember something, change how you behave, or add a capability — durable facts go in memory.md, new agents and tools in shell3.sh, new procedures in skills/. Also when you notice a recurring friction worth fixing. Covers what is yours to edit, what is the operator's, and how a change goes live.
---

# Changing yourself

Your whole configuration is one file plus two directories:

    ~/.shell3/
      shell3.sh                    wiring, every agent, every tool
      skills/                      your skills — one .md per skill
      projects/<agent>/skills/     an employee's skills
      memory.md                    your durable facts
      .env                         secrets — never read, never print

## Which one

**A fact to remember** → `memory.md`. The user's preferences, decisions,
things they told you once and shouldn't have to repeat. It is re-read into
every prompt, so keep it tight — it costs tokens on every turn. Absolute
dates, not "last week".

**A procedure you'll repeat** → a new `skills/<name>.md` with frontmatter
`description:`. The description is what you see in your prompt index; write it
so future-you knows when to open the file. The body is the how.

**A capability** → a tool in `shell3.sh`. See the `building-agents` skill for
the declaration shape. The rule: tools fetch, parse, query and persist; they
never score, rank or decide. Judgment belongs in a turn.

**A recurring job** → an employee in `shell3.sh` plus a `cron/<name>.md`
binding a schedule to (agent, prompt).

## What is not yours

- `.env` and `secrets/` — never read them, never print them. A tool reads the
  one key it needs at point of use.
- The `gate:` block and its function in `shell3.sh` — the operator's rules.
  If the gate blocks you, that is the answer: say which rule stopped you and
  why you needed it. Do not route around it, and do not edit it. Nothing
  stops you editing it — it sits in the same file as your own prompt — which
  is exactly why leaving it alone is on you.
- The `shell3:` wiring block at the top of `shell3.sh` — models, telegram, mcp
  servers. Ask before touching it.

## How a change goes live

    shell3 tool check ~/.shell3/shell3.sh     syntax, lint, every manifest
    reload                                     apply it

`check` catches an unterminated block, a duplicate name, a mistyped param, an
unquoted description with a comma in it. Run it after every edit — it is fast
and it is the difference between a broken config and a caught typo.

For a tool, also probe it before you trust it:

    shell3 tool run  ~/.shell3/shell3.sh <tool> '{"arg":"value"}'
    shell3 tool test ~/.shell3/shell3.sh

## Editing prose inside the kit

Prompts and skill bodies live in `<<'SHELL3_EOF'` heredocs. The quoted
delimiter means the text is literal — no escaping, no interpolation. The one
thing you cannot put in the body is a line that is exactly the delimiter.

## Before you change your own prompt

A prompt change alters every future turn, including ones where you would have
behaved correctly. Say what you are changing and why, then change it. If the
user asked for a behaviour change, this is the right move — do not just try
harder next turn and hope.
