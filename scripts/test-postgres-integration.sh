#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
. scripts/lib/qualification-runtime.sh

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

qualification_postgres_run "$container" integration "$credential" integration
qualification_postgres_ready "$container" integration integration
port="$(docker port "$container" 5432/tcp | awk -F: 'NR == 1 { print $NF }')"
database_url="postgres://integration:${credential}@${DOCKER_TEST_CONNECT_HOST}:${port}/integration?sslmode=disable"

go build -trimpath -o "$temporary/migrator" ./cmd/migrator
"$temporary/migrator" -database "$database_url" -path internal/db/migrations up >/dev/null

export AUDIT_OUTBOX_TEST_DATABASE_URL="$database_url"
export CHARLIE_ALERT_POLICY_TEST_DATABASE_URL="$database_url"
export DEX_CONCURRENCY_TEST_DATABASE_URL="$database_url"
export AGENT_UPGRADE_MATCH_TEST_DATABASE_URL="$database_url"
export CHARLIE_VISIBILITY_TEST_DATABASE_URL="$database_url"
export BUILTIN_PROVISIONER_TEST_DATABASE_URL="$database_url"
export DELIVERY_ROLLOUT_TEST_DATABASE_URL="$database_url"
export HEARTBEAT_TEST_DATABASE_URL="$database_url"
export INACTIVE_USER_RETENTION_TEST_DATABASE_URL="$database_url"
export REFRESH_SESSION_TEST_DATABASE_URL="$database_url"
export TABLE_VIEWS_TEST_DATABASE_URL="$database_url"
export ASTRONOMER_JSON_TEST_DATABASE_URL="$database_url"
export ASTRONOMER_SYSTEM_RELEASE_TEST_DATABASE_URL="$database_url"
export ASTRONOMER_CATALOG_APPLICATION_TEST_DATABASE_URL="$database_url"
export ASTRONOMER_SYSTEM_ROLLOUT_TEST_DATABASE_URL="$database_url"

expected=(
  TestAutomaticScanningPostgres
  TestPostgresBuiltinCanceledTransactionReleasesSessionLock
  TestPostgresBuiltinRegistrationRetryPresenceAndOrdering
  TestPostgresSystemRolloutStartIdempotencyAndEligibility
  TestPostgresSystemRolloutAuditFailureRollsBackStartAndAction
  TestPostgresSystemRolloutConcurrentFenceHasOneWinner
  TestPostgresSystemRolloutCohortPromotionAndRollback
  TestPostgresSystemRolloutPromotionConflictRollsBackRetirement
  TestPostgresCatalogUninstallCommitsFencedTargetAndInstallationTogether
  TestPostgresCatalogInstallRetryAndUpgradeKeepActorAndRevisionOwnership
  TestPostgresCatalogRollbackStagesBeforePlanningAndKeepsRevisionHistory
  TestPostgresSystemReleaseBootstrapIdempotencyAndDraft
  TestPostgresSystemReleaseBootstrapImmutableAndRevoked
  TestPostgresSystemReleaseBootstrapRollbackAfterInsert
  TestPostgresSystemReleaseBootstrapSameTransactionLock
  TestPostgresSystemReleaseBootstrapConcurrentFirstCreate
  TestPostgresCatalogAssetsIdempotencyAndExplicitGeneration
  TestPostgresCatalogAssetsUncuratedChartDoesNotInventVersionLedger
  TestPostgresCatalogAssetsLateValidationRollsBackPriorWrites
  TestPostgresDurableJSONNestedNewWritesAndLegacyArrays
  TestPostgresTaskPayloadSchemaMatchesV1Adapter
  TestPostgresDurableJSONExpandPreservesCorruptHistoricalRows
  TestPostgresIntegrityAuditDedupeUsesExistingOutbox
  TestPostgresPlacementAndRolloutNestedWireShapes
  TestCatalogSearchFiltersScopeBeforePaginationAndCounts
  TestAuditOutboxDeliveryDurablyFansOutToMatchingSIEMForwarders
  TestCatalogOperationStatusReadsExactRolloutAndDeletion
  TestClusterRestoreHistoryFiltersSourceBeforePagination
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
  TestRecordAgentHeartbeatAtomicWrite
  TestDeactivateInactiveUsersPostgresSemantics
  TestRefreshSessionConcurrentRotationAndReplayRevokesFamily
  TestTableViewsPostgresConcurrentCreatesRespectTheCap
  TestTableViewsPostgresConcurrentDuplicateNameYieldsOneWinner
  TestTableViewsPostgresConcurrentDefaultSwitchKeepsOneDefault
)
pattern="^($(IFS='|'; printf '%s' "${expected[*]}"))$"
race_args=()
if [[ "${POSTGRES_INTEGRATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi

expected_args=()
for expected_test in "${expected[@]}"; do expected_args+=(--expected "$expected_test"); done
python3 scripts/qualification-go.py --name postgres-integrations "${expected_args[@]}" -- \
  "${race_args[@]}" -p=1 -count=1 -timeout=10m -run "$pattern" \
  ./internal/db/sqlc ./internal/charlie ./internal/delivery/builtin ./internal/delivery/systemrollout \
  ./internal/delivery/rollout ./internal/delivery/catalogapp ./internal/delivery/systemrelease ./internal/worker/tasks \
  ./internal/handler ./internal/jsoncontract
