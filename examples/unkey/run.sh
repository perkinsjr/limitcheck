#!/usr/bin/env bash
# Run limitcheck against a correctly configured Unkey-backed local target.
# Expected exit: 0 (all default + isolation checks pass).
#
# Env:
#   UNKEY_ROOT_KEY   required — Unkey root key with ratelimit.*.limit
#   UNKEY_NAMESPACE  optional — default limitcheck.examples
#   UNKEY_LIMIT      optional — default 5
#   UNKEY_WINDOW_MS  optional — default 2000 (>= 400)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

if [[ -z "${UNKEY_ROOT_KEY:-}" ]]; then
  cat >&2 <<'EOF'
examples/unkey: blocked — UNKEY_ROOT_KEY is not set.

Export a Unkey root key with permission ratelimit.*.limit, then re-run:

  export UNKEY_ROOT_KEY=unkey_...
  # optional:
  # export UNKEY_NAMESPACE=limitcheck.examples
  # export UNKEY_LIMIT=5
  # export UNKEY_WINDOW_MS=2000
  ./examples/unkey/run.sh

Do not commit the key. Create one at https://app.unkey.com
EOF
  exit 2
fi

LIMIT="${UNKEY_LIMIT:-5}"
WINDOW_MS="${UNKEY_WINDOW_MS:-2000}"
# limitcheck wants count/duration, e.g. 5/2s
if (( WINDOW_MS % 1000 == 0 )); then
  LIMIT_FLAG="${LIMIT}/$((WINDOW_MS / 1000))s"
else
  LIMIT_FLAG="${LIMIT}/${WINDOW_MS}ms"
fi

go build -o /tmp/limitcheck-bin ./cmd/limitcheck

go run ./examples/unkey > /tmp/limitcheck-unkey-url.txt 2>/tmp/limitcheck-unkey-err.txt &
PID=$!
cleanup() {
  kill "$PID" 2>/dev/null || true
  wait "$PID" 2>/dev/null || true
}
trap cleanup EXIT

URL=""
for _ in $(seq 1 50); do
  if [[ -s /tmp/limitcheck-unkey-url.txt ]]; then
    URL="$(head -n1 /tmp/limitcheck-unkey-url.txt | tr -d '\r')"
    break
  fi
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "examples/unkey: server exited before printing a URL:" >&2
    cat /tmp/limitcheck-unkey-err.txt >&2 || true
    exit 2
  fi
  sleep 0.1
done
if [[ -z "$URL" ]]; then
  echo "examples/unkey: timed out waiting for listen URL" >&2
  cat /tmp/limitcheck-unkey-err.txt >&2 || true
  exit 2
fi

echo "target $URL"
echo "limit  $LIMIT_FLAG"
echo "expected: exit 0 (headers, accuracy, boundary, burst, recovery, isolation pass)"

set +e
/tmp/limitcheck-bin "$URL" \
  --limit "$LIMIT_FLAG" \
  --identity 'name=a; X-Identity: a' \
  --identity 'name=b; X-Identity: b' \
  --max-requests 500
CODE=$?
set -e

echo "exit $CODE"
if [[ "$CODE" -ne 0 ]]; then
  echo "examples/unkey: expected exit 0, got $CODE" >&2
  exit "$CODE"
fi
echo "unkey example passed"
