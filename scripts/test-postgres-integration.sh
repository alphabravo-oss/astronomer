#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

for tool in docker go openssl python3; do
  command -v "$tool" >/dev/null 2>&1 || {
    printf 'test-postgres-integration: missing required tool %s\n' "$tool" >&2
    exit 2
  }
done

suffix="$$-$(date +%s)"
container="astronomer-pg-integration-$suffix"
temporary="$(mktemp -d)"
credential="$(openssl rand -hex 24)"
cleanup() {
  docker rm -f "$container" >/dev/null 2>&1 || true
  rm -rf -- "$temporary"
}
trap cleanup EXIT

docker run -d --rm --name "$container" \
  -e POSTGRES_USER=integration \
  -e POSTGRES_PASSWORD="$credential" \
  -e POSTGRES_DB=integration \
  -p 127.0.0.1::5432 \
  pgvector/pgvector:pg16 >/dev/null

for _ in $(seq 1 60); do
  docker exec "$container" pg_isready -U integration -d integration >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$container" pg_isready -U integration -d integration >/dev/null
port="$(docker port "$container" 5432/tcp | awk -F: 'NR == 1 { print $NF }')"
database_url="postgres://integration:${credential}@127.0.0.1:${port}/integration?sslmode=disable"

go build -trimpath -o "$temporary/migrator" ./cmd/migrator
"$temporary/migrator" -database "$database_url" -path internal/db/migrations up >/dev/null

export AUDIT_OUTBOX_TEST_DATABASE_URL="$database_url"
export CHARLIE_ALERT_POLICY_TEST_DATABASE_URL="$database_url"
export DEX_CONCURRENCY_TEST_DATABASE_URL="$database_url"
export AGENT_UPGRADE_MATCH_TEST_DATABASE_URL="$database_url"
export CHARLIE_VISIBILITY_TEST_DATABASE_URL="$database_url"
export BUILTIN_PROVISIONER_TEST_DATABASE_URL="$database_url"
export DELIVERY_ROLLOUT_TEST_DATABASE_URL="$database_url"

expected=(
  TestAuditOutboxDeliveryDurablyFansOutToMatchingSIEMForwarders
  TestLoggingPipelineOutputsAreClusterScopedTransactionalAndDeleteRestricted
  TestUpsertCharlieAlertPolicyRevisionSemantics
  TestCharlieAlertReconcileCandidatesRequireFindingScope
  TestDexLifecycleAdvisoryLockRejectsStaleEnableAndRestore
  TestMarkRunningAgentUpgradeSucceededByVersion_NormalizesTheMatch
  TestMarkRunningAgentUpgradeSucceededByVersion_MatchesAcrossVersionSpellings
  TestAdminVisibilityQueriesAgainstMigratedPostgres
  TestProvisionerPostgresMultiSourceAssets
  TestPostgresPlanningTransactionAndHAFencing
  TestCharlieAlertDispatchDistributedFenceBlocksDisableAcrossProcesses
  TestDistributedFenceHoldBlocksQueuedCrossReplicaAdmissionUntilTransitionRelease
)
pattern="^($(IFS='|'; printf '%s' "${expected[*]}"))$"
race_args=()
if [[ "${POSTGRES_INTEGRATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi

report="$temporary/go-test.json"
set +e
go test "${race_args[@]}" -json -p=1 -count=1 -timeout=10m \
  -run "$pattern" \
  ./internal/db/sqlc ./internal/charlie ./internal/delivery/builtin \
  ./internal/delivery/rollout ./internal/worker/tasks | tee "$report"
test_status=${PIPESTATUS[0]}
set -e
(( test_status == 0 )) || exit "$test_status"

python3 - "$report" "${expected[@]}" <<'PY'
import json
import sys

path, *expected = sys.argv[1:]
terminal = {}
with open(path, encoding="utf-8") as handle:
    for line in handle:
        event = json.loads(line)
        test = event.get("Test")
        action = event.get("Action")
        if test in expected and action in {"pass", "fail", "skip"}:
            terminal[test] = action
missing = sorted(set(expected) - set(terminal))
skipped = sorted(test for test, action in terminal.items() if action == "skip")
failed = sorted(test for test, action in terminal.items() if action == "fail")
if missing or skipped or failed:
    raise SystemExit(f"integration contract incomplete: missing={missing} skipped={skipped} failed={failed}")
print(f"postgres integration contract passed: {len(expected)}/{len(expected)} tests executed")
PY
