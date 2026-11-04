#!/usr/bin/env bash
# Full quality gate for MyAI (run after every stage).
# Usage: scripts/gate.sh   — prints each command and its result.
#
# از ۱.۸.۰: gofmt باید «خروجی خالی» بدهد؛ پیش‌تر فقط exit code بررسی می‌شد
# و gofmt با فایلِ غیرفرمت هم صفر برمی‌گرداند (گیت سبزِ دروغین).
set -u
cd "$(dirname "$0")/.."
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

run_expect_empty() {
  echo "=== $* ==="
  if out=$("$@" 2>&1); then
    if [ -n "$out" ]; then
      echo "$out"
      echo "[FAIL] $* (must print nothing)"
      FAIL=1
      return
    fi
    echo "[OK] $*"
  else
    echo "$out"
    echo "[FAIL] $*"
    FAIL=1
  fi
}

run_expect_empty gofmt -l .
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
