#!/usr/bin/env bash
# Full quality gate for MyAI (run after every stage).
# Usage: scripts/gate.sh [note]   — prints each command and its result.
set -u
cd "$(dirname "$0")/.."
export PATH=/home/z/sdk/go/bin:$PATH
FAIL=0

run() {
  echo "=== $* ==="
  if out=$("$@" 2>&1); then
    if [ -n "$out" ]; then echo "$out"; fi
    echo "[OK] $*"
  else
    echo "$out"
    echo "[FAIL] $*"
    FAIL=1
  fi
}

run gofmt -l .
run go vet ./...
run go build ./...
run go test -race -count=1 ./...
run env GOOS=windows GOARCH=amd64 go vet ./...
run env GOOS=linux GOARCH=amd64 go vet ./...

if [ "$FAIL" -eq 0 ]; then
  echo "GATE: GREEN"
else
  echo "GATE: RED"
fi
exit $FAIL
