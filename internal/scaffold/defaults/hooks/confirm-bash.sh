#!/usr/bin/env bash
# Default on_tool_call hook: prompts the user before any `bash` tool runs.
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

if [[ "$TOOL" != "bash" ]]; then
  echo '{"action":"allow"}'
  exit 0
fi

CMD=$(echo "$INPUT" | jq -r '.params.command // empty')

RESULT=$(jq -n --arg input "bash: $CMD" '
  {
    "input": $input,
    "default": "allow",
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
