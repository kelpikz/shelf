#!/usr/bin/env bash
# Runs every Go check. Run from anywhere. Exit code is non-zero if any check fails.
# Needs: golangci-lint (v2), govulncheck.
set -uo pipefail

cd "$(dirname "$0")/.."

failed=()

run() {
  local name=$1
  shift
  echo "==> $name"
  if ! "$@"; then
    failed+=("$name")
  fi
  echo
}

tidy_check() {
  cp go.mod "$TMPDIR_CHECK/go.mod"
  cp go.sum "$TMPDIR_CHECK/go.sum"
  go mod tidy || return 1
  diff -q go.mod "$TMPDIR_CHECK/go.mod" >/dev/null && diff -q go.sum "$TMPDIR_CHECK/go.sum" >/dev/null || {
    cp "$TMPDIR_CHECK/go.mod" go.mod
    cp "$TMPDIR_CHECK/go.sum" go.sum
    echo "go.mod or go.sum is not tidy. Run: go mod tidy"
    return 1
  }
}

TMPDIR_CHECK=$(mktemp -d)
trap 'rm -rf "$TMPDIR_CHECK"' EXIT

# -race needs a C compiler. Turn it on with RACE=1 (CI does).
test_flags=(-cover)
if [[ "${RACE:-0}" == "1" ]]; then
  test_flags+=(-race)
fi

run "go vet" go vet ./...
run "golangci-lint" golangci-lint run ./...
run "govulncheck" govulncheck ./...
run "go test" go test "${test_flags[@]}" ./...
run "go mod tidy" tidy_check

if ((${#failed[@]})); then
  echo "FAILED: ${failed[*]}"
  exit 1
fi
echo "All checks passed."
