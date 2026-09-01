#!/usr/bin/env bash
# deepcheck — the analyses `make lint` doesn't run. lint (gofmt + go vet +
# golangci-lint: staticcheck, gocritic, unused, unparam, ineffassign,
# errcheck, bodyclose) is the fast per-commit gate; this is the slower
# whole-program pass worth running before a release:
#
#   deadcode    unreachable functions via call-graph analysis, both with tests
#               and from production entry points only
#   dupl        token-based clone detection (report-only: duplication is a
#               judgment call, not a build-breaker)
#   govulncheck known vulnerabilities actually REACHABLE from this code
#
# Install the tools once:
#   go install golang.org/x/tools/cmd/deadcode@latest
#   go install github.com/mibk/dupl@latest
#   go install golang.org/x/vuln/cmd/govulncheck@latest
set -uo pipefail
cd "$(dirname "$0")/.."

fail=0
have() { command -v "$1" >/dev/null 2>&1 || { echo "· $1 not installed — go install $2@latest"; return 1; }; }

echo "== make lint (the per-commit gate) =="
make lint || fail=1

echo
echo "== deadcode (unreachable functions) =="
if have deadcode golang.org/x/tools/cmd/deadcode; then
  out=$(deadcode -test ./... 2>&1)
  if [ -n "$out" ]; then echo "$out"; fail=1; else echo "clean"; fi

  echo "-- production entry points --"
  # These packages/files are explicit cross-package test fixtures. Everything
  # else must be reachable without tests; otherwise tests are propping up dead
  # production surface and `deadcode -test` alone would hide it.
  out=$(deadcode ./... 2>&1 | sed \
    -e '/internal\/llm\/fakellm\//d' \
    -e '/internal\/shell3\/shell3test\//d' \
    -e '/internal\/shell3\/testsupport.go:/d')
  if [ -n "$out" ]; then echo "$out"; fail=1; else echo "clean"; fi
fi

echo
echo "== dupl (clones ≥ 100 tokens; report-only) =="
if have dupl github.com/mibk/dupl; then
  # Only the repo's own production code: skip tests, hidden dirs (a stale
  # .claude/worktrees copy would report every file as its own clone), and
  # anything untracked-vendored.
  out=$(find . -path './.*' -prune -o -name '*_test.go' -prune -o -name '*.go' -print | xargs dupl -t 100 2>&1)
  if [ -n "$out" ]; then echo "$out"; else echo "clean"; fi
fi

echo
echo "== govulncheck (reachable known vulnerabilities) =="
if have govulncheck golang.org/x/vuln/cmd/govulncheck; then
  govulncheck ./... || fail=1
fi

exit $fail
