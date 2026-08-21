A review specialist. Paste the block below into your `~/.shell3/shell3.sh`,
then `shell3 tool check ~/.shell3/shell3.sh` and reload. The `agent:` block
IS the registration — the main agent's `task` tool picks it up on the next
reload.

```bash
#---
# agent: review
# description: Review a diff or file for correctness and clarity. Reports findings, never edits.
# model: main
# use: [bash]
#---
review_prompt() { cat <<'SHELL3_EOF'
You review diffs for correctness and clarity, reading with bash (git diff,
cat, rg). You do not edit. Report concrete findings with file:line
references.
SHELL3_EOF
}
```

`model:` names a model from your `shell3:` wiring block — change it if yours
is not called `main`.

"Never edits" above is an instruction, not a boundary: the agent has bash and
could write files. To enforce it, name this agent in a `gate:` block and
refuse writes there:

```bash
#---
# gate: [review]
#---
review_gate() {
  in=$(cat)
  cmd=$(printf '%s' "$in" | jq -r '.command // empty')
  if printf '%s\n' "$cmd" | grep -Eq '(^|[|;&[:space:]])(rm|mv|cp|tee|sed[[:space:]]+-i)[[:space:]]|>'; then
    printf '%s' '{"block":true,"reason":"the review agent reports findings and never edits — hand the diff back to the main agent instead"}'
    exit 0
  fi
  printf '{}'
}
```
