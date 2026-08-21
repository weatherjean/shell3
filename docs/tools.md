# Tools

A tool is a declared verb: a name and description the model reads, typed
parameters, and a shell function that does the work. It is a real tool — it
appears in the model's tool list, it is called with structured arguments, and it
goes through the hook gate — not a bash string the agent has to remember from
prose.

## Declaring one

```sh
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
  ...
}
```

- `description` is required. It is the only thing telling the model when to
  reach for this.
- Param types are `string`, `int`, or `bool`.
- A param name becomes an environment variable, so it must be a valid
  identifier and must not shadow `PATH`, `HOME`, `TMPDIR`, `LANG`, or `TZ`.
- Stdout is the result. A nonzero exit is a tool error, and stderr comes back
  with it — a failing tool says why rather than returning silence.

Tool bodies default to bash. A body starting with `#!` uses that interpreter
instead, so genuinely awkward work (HTML parsing, scoring maths) can be another
language without dragging LLM plumbing back into scripts.

## The author's loop

```
shell3 tool check <kit>                    bash -n + shellcheck + manifest validation
shell3 tool run   <kit> <tool> '<json>'    one invocation, no session, no tokens
shell3 tool test  <kit> [tool]             run the declared tests
```

Write the tool, `check` it parses, `run` it once against something real, write a
test, `test` that it keeps working. Only the first step needs a model.

**check** validates that the file is valid bash, lints it with `shellcheck` when
installed, and validates every manifest: no top-level statements, every
declaration followed by a function, no duplicate names, params typed,
descriptions present. Errors carry the kit file's own line numbers.

Because a kit is one `.sh` file, `bash -n shell3.sh` and `shellcheck shell3.sh`
cover the whole thing in one command — no extraction step, no line remapping.

**run** invokes a single tool with a JSON payload outside any session:

```
$ shell3 tool run shell3.sh page-kind '{"url":"https://example.com/post"}'
article
```

## Tests

A `test:` block sits under the tool it exercises. The body is bash.

```sh
#---
# test: page-kind — classifies each kind
#---
bm_test_page_kind() {
  stub curl <<'STUB'
<article><h1>a post</h1></article>
STUB
  assert_eq "$(tool page-kind url=https://x.test)" article

  stub curl <<'STUB'
<html>domain for sale</html>
STUB
  assert_eq "$(tool page-kind url=https://x.test)" dead
}
```

The harness gives you six names and nothing else:

| | |
|---|---|
| `tool <name> k=v …` | invoke a declared tool through the gate, as a real call |
| `stub <cmd>` | read stdin; install `<cmd>` at the front of `PATH` printing exactly that |
| `assert_eq a b` | fail if unequal |
| `assert_contains hay needle` | substring assert |
| `fail <msg>` | print and exit nonzero |
| `$KIT_TMP` | fresh scratch dir per test, removed afterwards |

`stub` is what makes network tools testable with no fixtures on disk. You are
not testing that `curl` works; you are testing what your tool does with its
output. Same trick for a database tool: build a schema in `$KIT_TMP` and point
the tool at it.

A test may also call the shell function directly (`url=… bm_page_kind`),
which bypasses param validation and the gate. Use that for unit-testing the body
and for debugging after `source shell3.sh`; use `tool` for everything else,
because it exercises what a real call exercises.

## Health

`shell3 health` runs `check` across the kit, so a broken tool or a malformed
manifest is caught at startup rather than at 3am. Tests are not run by default —
they can be slow. `shell3 health --tests` opts in, and is what to schedule if
you want regressions caught.

## Shared tools

A `shared:` block holds tools and skills any agent can import:

```sh
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

An agent gets them with `use: [web]`. A shared tool colliding with a local one
is a load error, not a silent shadow.

## What is out of scope

These tests pin the deterministic half. Nothing here asserts "given this prompt
the agent should have called `page-kind` before `lead-db`" — the tools are the
part worth pinning, and the judgment is the part deliberately left to the model.
