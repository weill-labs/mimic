#!/usr/bin/env bash
# Collect test coverage (used by CI and `make coverage`).
#
# Usage:
#   scripts/coverage.sh              # local: run tests, print summary, clean up
#   scripts/coverage.sh --keep-files # local: keep coverage artifacts for reuse
#   scripts/coverage.sh --ci         # CI: also emit JSON test results, keep files
set -uo pipefail

CI_MODE=false
KEEP_FILES=false
for arg in "$@"; do
  case "$arg" in
    --ci)
      CI_MODE=true
      KEEP_FILES=true
      ;;
    --keep-files)
      KEEP_FILES=true
      ;;
    *)
      echo "usage: scripts/coverage.sh [--ci] [--keep-files]" >&2
      exit 1
      ;;
  esac
done

if [[ "$KEEP_FILES" != true ]]; then
  trap 'rm -f coverage.txt coverage-summary.txt results.json' EXIT
fi

test_rc=0

echo "=== Tests with coverage ==="
test_args=(-race -coverprofile=coverage.txt -covermode=atomic ./... -timeout 120s)
if [[ "$CI_MODE" == true ]]; then
  go test -json "${test_args[@]}" | tee results.json || test_rc=$?
else
  go test "${test_args[@]}" || test_rc=$?
fi

if [[ -f coverage.txt ]]; then
  go tool cover -func coverage.txt > coverage-summary.txt
  echo ""
  tail -1 coverage-summary.txt
fi

if (( test_rc != 0 )); then
  exit 1
fi
