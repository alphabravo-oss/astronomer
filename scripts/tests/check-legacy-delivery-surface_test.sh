#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CHECKER="$ROOT/scripts/check-legacy-delivery-surface.sh"
FIXTURES="$ROOT/scripts/testdata/legacy-delivery"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf -- "$TMP_DIR"' EXIT

fail() {
  echo "check-legacy-delivery-surface test: FAIL: $*" >&2
  exit 1
}

"$CHECKER" --root "$FIXTURES/mixed" --report --skip-generated-surfaces >"$TMP_DIR/mixed-report.txt"
grep -Eq 'summary scope=active_runtime signature=banned_rancher_fleet files=1 matches=1' "$TMP_DIR/mixed-report.txt" \
  || fail "Rancher Fleet fixture was not detected as an active banned surface"
grep -Eq 'summary scope=active_runtime signature=legacy_argo files=1 matches=1' "$TMP_DIR/mixed-report.txt" \
  || fail "active Argo fixture was not detected"
grep -Eq 'summary scope=historical_allowlist signature=legacy_argo files=1 matches=1' "$TMP_DIR/mixed-report.txt" \
  || fail "historical Argo fixture was not separated from runtime"
grep -Fxq 'verdict=pass' "$TMP_DIR/mixed-report.txt" \
  || fail "report mode must characterize rather than reject"

if "$CHECKER" --root "$FIXTURES/mixed" --fail --skip-generated-surfaces >"$TMP_DIR/mixed-fail.txt"; then
  fail "fail mode accepted active legacy surfaces"
fi
grep -Fxq 'verdict=fail' "$TMP_DIR/mixed-fail.txt" \
  || fail "fail mode did not emit a failing verdict"

"$CHECKER" --root "$FIXTURES/historical-only" --fail --skip-generated-surfaces >"$TMP_DIR/historical.txt"
grep -Eq 'summary scope=historical_allowlist signature=legacy_fleet_operations files=1 matches=1' \
  "$TMP_DIR/historical.txt" || fail "historical legacy fleet operation was not reported"
grep -Fxq 'active_prohibited_matches=0' "$TMP_DIR/historical.txt" \
  || fail "historical context leaked into active findings"

"$CHECKER" --root "$FIXTURES/clean" --fail --skip-generated-surfaces >"$TMP_DIR/clean.txt"
grep -Fxq 'active_prohibited_matches=0' "$TMP_DIR/clean.txt" || fail "clean fixture was rejected"

echo "check-legacy-delivery-surface test: PASS"
