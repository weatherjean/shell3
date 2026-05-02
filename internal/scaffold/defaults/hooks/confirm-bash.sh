#!/usr/bin/env bash

INPUT=$(cat)
TOOL=$(echo "$INPUT" | jq -r '.tool')

if [[ "$TOOL" != "bash" ]]; then
  echo '{"action":"allow"}'
  exit 0
fi

CMD=$(echo "$INPUT" | jq -r '.params.command // empty')
echo "" >/dev/tty
echo "🔧 bash: $CMD" >/dev/tty
read -r -p "Allow? [Y/n] " ans </dev/tty

case "$ans" in
  ""|[Yy])
    echo '{"action":"allow"}'
    ;;
  [Nn])
    echo '{"action":"block","reason":"User denied bash command"}'
    ;;
  *)
    echo '{"action":"block","reason":"Invalid response; expected y, n, or Enter"}'
    ;;
esac
