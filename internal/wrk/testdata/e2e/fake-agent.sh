#!/bin/sh
set -eu

result=$1
artifacts=$2
attempt=$3
prompt=$(cat)

mkdir -p "$artifacts"
printf 'attempt=%s\nprompt=%s\n' "$attempt" "$prompt" > "$result"
if [ "$attempt" = 2 ]; then
  printf 'verified\n' > "$artifacts/done"
fi
