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
read -r -p "Allow? [y/N] " ans </dev/tty

if [[ "$ans" =~ ^[Yy]$ ]]; then
  echo '{"action":"allow"}'
else
  echo '{"action":"block","reason":"User denied bash command"}'
fi
