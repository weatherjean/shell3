#!/usr/bin/env bash
# Default on_tool_call hook: prompts the user before potentially-dangerous
# bash / shell_interactive tool calls. Safe commands run unprompted.
#
# Demonstrates the `shell3 widget pick` JSON-in/JSON-out widget. The widget
# renders an inline list selector on /dev/tty and writes a Result JSON to
# stdout; we read .value to decide which hook action to emit.
#
# Hook contract: stdin = on_tool_call input, stdout = action JSON.
# Actions:
#   allow  — proceed
#   block  — deny THIS call only; model can pick a different approach
#   cancel — abort the entire turn

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool')

if [[ "$TOOL" != "bash" && "$TOOL" != "shell_interactive" ]]; then
  echo '{"action":"allow"}'
  exit 0
fi

CMD=$(echo "$INPUT" | jq -r '.params.command // empty')

# Single-token blacklist (matched word-bounded via grep -w).
DANGER='rm|rmdir|shred|dd|mkfs|chmod|chown|sudo|doas|curl|wget|eval'

# Pattern blacklist (multi-token / contextual). Each entry is an ERE.
DANGER_PATTERNS=(
  '(^|[^a-zA-Z0-9_])git[[:space:]]+(push|reset|rebase|clean|checkout|branch|stash|tag|merge|filter-branch)([^a-zA-Z0-9_]|$)'
  '\|[[:space:]]*(sh|bash|zsh)([[:space:]]|$)'
)

HIT=""
if echo "$CMD" | grep -qwE "$DANGER"; then
  HIT=1
fi
if [[ -z "$HIT" ]]; then
  for pat in "${DANGER_PATTERNS[@]}"; do
    if echo "$CMD" | grep -qE "$pat"; then
      HIT=1
      break
    fi
  done
fi

if [[ -z "$HIT" ]]; then
  echo '{"action":"allow"}'
  exit 0
fi

RESULT=$(jq -n --arg input "${TOOL}: $CMD" '
  {
    "input": $input,
    "default": "block",
    "choices": [
      {"value":"allow","label":"Allow","hint":"yes, run it"},
      {"value":"block","label":"Deny this call","hint":"model picks a different approach"},
      {"value":"cancel","label":"Cancel turn","hint":"stop generation entirely"}
    ]
  }' | shell3 widget pick 2>/dev/null)

CHOICE=$(echo "$RESULT" | jq -r '.value // "block"')

case "$CHOICE" in
  allow)
    echo '{"action":"allow"}'
    ;;
  cancel)
    echo '{"action":"cancel","reason":"User cancelled generation."}'
    ;;
  *)
    echo '{"action":"block","reason":"User denied this bash command. Acknowledge and try a different approach."}'
    ;;
esac
