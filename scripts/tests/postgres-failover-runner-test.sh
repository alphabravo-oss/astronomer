#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/../.." && pwd)"
runner="$root/scripts/test-postgres-failover-certification.sh"
integration="$root/internal/server/postgres_failover_certification_integration_test.go"

bash -n "$runner"

for required in \
  'synchronous_standby_names' \
  'pg_basebackup' \
  'POSTGRES_FAILOVER_RPO_MAX_ROWS' \
  'POSTGRES_FAILOVER_RTO_MAX_SECONDS' \
  'evidence.json' \
  '"status": "fail"' \
  'docker logs' \
  'PIPESTATUS[0]'; do
  grep -Fq "$required" "$runner" || {
    printf 'runner is missing required fail-closed contract: %s\n' "$required" >&2
    exit 1
  }
done

for required in \
  'astronomer-postgres-failover-certification/v1' \
  'pg_is_in_recovery()' \
  'rpo_rows' \
  'rto_ms' \
  'newReadinessHandler' \
  'post_failover_persistence'; do
  grep -Fq "$required" "$integration" || {
    printf 'integration test is missing required certification contract: %s\n' "$required" >&2
    exit 1
  }
done

printf 'postgres-failover-runner-test: OK\n'
