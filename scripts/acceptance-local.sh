#!/bin/sh
set -eu

if [ "$#" -ne 1 ]; then
  echo "usage: $0 /path/to/shell3" >&2
  exit 2
fi

case "$1" in
  /*) shell3_bin=$1 ;;
  *) shell3_bin=$(cd "$(dirname "$1")" && pwd)/$(basename "$1") ;;
esac

repo=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
fixture=$repo/internal/wrk/testdata/e2e
state=$(mktemp -d "${TMPDIR:-/tmp}/shell3-acceptance.XXXXXX")
trap 'rm -rf "$state"' EXIT HUP INT TERM

config=$fixture/shell3.lisp
workflow=$fixture/demo.wrk.lisp

"$shell3_bin" config check "$config"
"$shell3_bin" wrk check --config "$config" "$workflow"
"$shell3_bin" wrk compile --config "$config" --output "$state/workflow.sh" "$workflow"
sh -n "$state/workflow.sh"

"$shell3_bin" wrk run \
  --config "$config" \
  --state "$state/wrk" \
  --notify-state "$state/control" \
  --run-id acceptance \
  --shell3-bin "$shell3_bin" \
  "$workflow" "deterministic acceptance request"

"$shell3_bin" wrk status --state "$state/wrk" e2e/acceptance > "$state/status.json"
grep -q '"status": "completed"' "$state/status.json"
test -f "$state/wrk/e2e/acceptance/artifacts/done"

mkdir -p "$state/schedule"
"$shell3_bin" schedule run \
  --config "$config" \
  --workdir "$state/schedule" \
  acceptance > "$state/schedule-run.json"
grep -q '"status":"done"' "$state/schedule-run.json"
"$shell3_bin" schedule history \
  --workdir "$state/schedule" \
  --status done \
  acceptance > "$state/schedule-history.jsonl"
grep -q '"schedule":"acceptance"' "$state/schedule-history.jsonl"
test -f "$state/schedule/.shell3_project/wrk/e2e/"*/artifacts/done
test "$(find "$state/schedule/.shell3_project/inbox" -type f -name '*.json' | wc -l | tr -d ' ')" = 1
grep -q '"event": "wrk.completed"' "$state/schedule/.shell3_project/inbox"/*/new/*.json
grep -q '"event":"schedule.started"' "$state/schedule/.shell3_project/errors.jsonl"
grep -q '"event":"schedule.done"' "$state/schedule/.shell3_project/errors.jsonl"

echo "local acceptance: ok"
