#!/usr/bin/env bash
# Prove every built-in limiter mode limitcheck claims to catch (or pass).
# Uses `limitcheck demo` / `--self-test`. Exit 0 only if every expectation holds.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BIN="${LIMITCHECK_BIN:-}"
if [[ -z "$BIN" ]]; then
  go build -o /tmp/limitcheck-bin ./cmd/limitcheck
  BIN=/tmp/limitcheck-bin
fi

pass=0
fail=0

# name | expected_exit | note
# demo scenarios return the report exit code (fail → 1), not the self-test matcher.
run_demo() {
  local name="$1" want="$2" note="$3"
  echo
  echo "=== demo --scenario $name  (expect exit $want: $note) ==="
  set +e
  out="$("$BIN" demo --scenario "$name" 2>&1)"
  code=$?
  set -e
  printf '%s\n' "$out"
  echo "exit $code"
  if [[ "$code" -eq "$want" ]]; then
    echo "OK"
    pass=$((pass + 1))
  else
    echo "FAIL: want exit $want, got $code" >&2
    fail=$((fail + 1))
  fi
}

echo "=== --self-test  (expect exit 0: every scenario matches its claim) ==="
set +e
out="$("$BIN" --self-test 2>&1)"
code=$?
set -e
printf '%s\n' "$out"
echo "exit $code"
if [[ "$code" -eq 0 ]]; then
  echo "OK"
  pass=$((pass + 1))
else
  echo "FAIL: self-test want exit 0, got $code" >&2
  fail=$((fail + 1))
fi

# success: sliding / token bucket holds
run_demo token 0 "pass — sliding window holds (incl. isolation)"

# failures that match each check claim
run_demo fixed 1 "fail — fixed-window boundary leak (~2x)"
run_demo race 1 "fail — concurrent burst race"
run_demo open 1 "fail — accuracy (allows more than stated)"
run_demo bad-headers 1 "fail — wrong status / missing rate-limit headers"
run_demo early 1 "fail — recovery before advertised reset"
run_demo rotate 1 "fail — isolation (request-id rotation bypass)"
run_demo per-instance 1 "fail — multi_instance (each origin keeps own limit)"
run_demo shared 1 "fail — isolation across identities on a shared counter"

echo
echo "passed $pass, failed $fail"
if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
echo "all local examples matched expected exit codes"
