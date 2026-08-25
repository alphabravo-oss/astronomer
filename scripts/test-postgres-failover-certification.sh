#!/usr/bin/env bash
set -Eeuo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

for tool in docker go openssl python3 tee; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

suffix="$$-$(date +%s)"
run_id="postgres-failover-$suffix"
started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
bootstrap_stage="preflight"
primary_container="astronomer-postgres-failover-primary-$suffix"
replica_container="astronomer-postgres-failover-replica-$suffix"
network="astronomer-postgres-failover-$suffix"
primary_volume="astronomer-postgres-failover-primary-$suffix"
replica_volume="astronomer-postgres-failover-replica-$suffix"
credential="$(openssl rand -hex 18)"
postgres_image="${POSTGRES_FAILOVER_IMAGE:-pgvector/pgvector:pg16}"
artifact_dir="${POSTGRES_FAILOVER_ARTIFACT_DIR:-${TMPDIR:-/tmp}/astronomer-postgres-failover-$suffix}"
work_dir="$(mktemp -d)"
test_status=1

mkdir -p "$artifact_dir"

cleanup() {
  local cleanup_status=$?
  if [[ ! -s "$artifact_dir/evidence.json" ]]; then
    local completed_at source_commit_cleanup image_id_cleanup
    completed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    source_commit_cleanup="$(git rev-parse HEAD 2>/dev/null || printf unknown)"
    image_id_cleanup="$(docker image inspect "$postgres_image" --format '{{.Id}}' 2>/dev/null || printf unavailable)"
    python3 - "$artifact_dir/evidence.json" "$run_id" "$started_at" "$completed_at" \
      "$source_commit_cleanup" "$postgres_image" "$image_id_cleanup" "$bootstrap_stage" \
      "${POSTGRES_FAILOVER_RPO_MAX_ROWS:-0}" "${POSTGRES_FAILOVER_RTO_MAX_SECONDS:-30}" <<'PY'
import json
import os
import sys

(path, run_id, started_at, completed_at, commit, image, image_id, stage,
 rpo_max_rows, rto_max_seconds) = sys.argv[1:]
document = {
    "schema_version": "astronomer-postgres-failover-certification/v1",
    "run_id": run_id,
    "status": "fail",
    "timestamps": {
        "started_at": started_at,
        "completed_at": completed_at,
        "generated_at": completed_at,
    },
    "provenance": {
        "source_commit": commit,
        "source_repository": os.environ.get("GITHUB_REPOSITORY", "local"),
        "source_ref": os.environ.get("GITHUB_REF", "local"),
        "workflow": os.environ.get("GITHUB_WORKFLOW", "local"),
        "workflow_run_id": os.environ.get("GITHUB_RUN_ID", "local"),
        "workflow_run_attempt": os.environ.get("GITHUB_RUN_ATTEMPT", "local"),
        "postgres_image": image,
        "postgres_image_id": image_id,
    },
    "topology": {
        "replication": "postgresql_physical_streaming",
        "commit_mode": "synchronous_commit_remote_flush",
        "application_endpoint": "stable_tcp_writer_endpoint",
        "failure_injection": "primary_sigkill",
        "promotion": "pg_ctl_promote",
    },
    "thresholds": {
        "rpo_max_rows": int(rpo_max_rows),
        "rto_max_ms": int(rto_max_seconds) * 1000,
    },
    "measurements": {},
    "checks": [{"id": "bootstrap", "status": "fail", "detail": stage}],
    "events": [],
    "residual_scope": [
        "Failover measurements are unavailable because the disposable topology did not complete bootstrap."
    ],
}
temporary = path + ".tmp"
with open(temporary, "w", encoding="utf-8") as handle:
    json.dump(document, handle, indent=2)
    handle.write("\n")
os.chmod(temporary, 0o600)
os.replace(temporary, path)
PY
  fi
  docker logs "$primary_container" >"$artifact_dir/postgres-primary.log" 2>&1 || true
  docker logs "$replica_container" >"$artifact_dir/postgres-replica.log" 2>&1 || true
  docker inspect "$primary_container" >"$artifact_dir/postgres-primary-inspect.json" 2>/dev/null || true
  docker inspect "$replica_container" >"$artifact_dir/postgres-replica-inspect.json" 2>/dev/null || true
  docker rm -f "$primary_container" "$replica_container" >/dev/null 2>&1 || true
  docker volume rm -f "$primary_volume" "$replica_volume" >/dev/null 2>&1 || true
  docker network rm "$network" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
  if [[ "$test_status" -ne 0 || "$cleanup_status" -ne 0 ]]; then
    printf 'test-postgres-failover-certification: FAIL\n' >&2
  fi
  printf 'test-postgres-failover-certification: artifacts: %s\n' "$artifact_dir"
}
trap cleanup EXIT

docker network create \
  --label astronomer.qualification=postgres-failover "$network" >/dev/null
bootstrap_stage="primary_start"
docker volume create --label astronomer.qualification=postgres-failover "$primary_volume" >/dev/null
docker volume create --label astronomer.qualification=postgres-failover "$replica_volume" >/dev/null

docker run -d --name "$primary_container" \
  --label astronomer.qualification=postgres-failover \
  --network "$network" --network-alias primary \
  -e POSTGRES_USER=postgres_failover \
  -e POSTGRES_PASSWORD="$credential" \
  -e POSTGRES_DB=postgres_failover \
  -v "$primary_volume:/var/lib/postgresql/data" \
  -v "$root/scripts/testdata/postgres-failover/pg_hba.conf:/qualification/pg_hba.conf:ro" \
  -p 127.0.0.1::5432 \
  "$postgres_image" \
  -c wal_level=replica \
  -c max_wal_senders=8 \
  -c max_replication_slots=8 \
  -c wal_keep_size=256MB \
  -c hot_standby=on \
  -c hba_file=/qualification/pg_hba.conf >/dev/null

for attempt in $(seq 1 90); do
  if docker exec "$primary_container" pg_isready -U postgres_failover -d postgres_failover >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == 90 ]]; then
    echo "primary PostgreSQL did not become ready" >&2
    exit 1
  fi
  sleep 0.5
done

primary_port="$(docker port "$primary_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
primary_url="postgres://postgres_failover:${credential}@127.0.0.1:${primary_port}/postgres_failover?sslmode=disable"

bootstrap_stage="schema_migration"
go build -trimpath -o "$work_dir/migrator" ./cmd/migrator 2>&1 | tee "$artifact_dir/build-migrator.log"
"$work_dir/migrator" -database "$primary_url" -path internal/db/migrations up \
  >"$artifact_dir/migrations.log" 2>&1

# The named volume starts root-owned. Prepare it once, then take the physical
# base backup as the postgres OS user so the promoted server retains safe file
# ownership and permissions.
bootstrap_stage="physical_base_backup"
docker run --rm -v "$replica_volume:/var/lib/postgresql/data" \
  --entrypoint sh "$postgres_image" -ceu \
  'chown -R postgres:postgres /var/lib/postgresql/data' >/dev/null
docker run --rm --user postgres --network "$network" \
  -e PGPASSWORD="$credential" \
  -v "$replica_volume:/var/lib/postgresql/data" \
  --entrypoint pg_basebackup "$postgres_image" \
  -h primary -U postgres_failover -D /var/lib/postgresql/data \
  -Fp -Xs -P -R --checkpoint=fast >"$artifact_dir/pg-basebackup.log" 2>&1

docker run -d --name "$replica_container" \
  --label astronomer.qualification=postgres-failover \
  --network "$network" --network-alias replica \
  -e POSTGRES_USER=postgres_failover \
  -e POSTGRES_PASSWORD="$credential" \
  -e POSTGRES_DB=postgres_failover \
  -v "$replica_volume:/var/lib/postgresql/data" \
  -v "$root/scripts/testdata/postgres-failover/pg_hba.conf:/qualification/pg_hba.conf:ro" \
  -p 127.0.0.1::5432 \
  "$postgres_image" \
  -c hot_standby=on \
  -c hba_file=/qualification/pg_hba.conf >/dev/null

bootstrap_stage="replica_streaming"
for attempt in $(seq 1 90); do
  if docker exec "$replica_container" pg_isready -U postgres_failover -d postgres_failover >/dev/null 2>&1 && \
     [[ "$(docker exec "$replica_container" psql -U postgres_failover -d postgres_failover -Atqc 'SELECT pg_is_in_recovery()' 2>/dev/null)" == "t" ]]; then
    break
  fi
  if [[ "$attempt" == 90 ]]; then
    echo "replica PostgreSQL did not enter recovery" >&2
    exit 1
  fi
  sleep 0.5
done

replica_port="$(docker port "$replica_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
replica_url="postgres://postgres_failover:${credential}@127.0.0.1:${replica_port}/postgres_failover?sslmode=disable"

# Convert the already-streaming replica to synchronous acknowledgement before
# any RPO canary is committed. This makes the zero-row RPO threshold meaningful:
# every acknowledged Astronomer write must already be durable on the standby.
bootstrap_stage="synchronous_replication"
docker exec "$primary_container" psql -U postgres_failover -d postgres_failover \
  -v ON_ERROR_STOP=1 -c "ALTER SYSTEM SET synchronous_standby_names = '*';" \
  >"$artifact_dir/enable-synchronous-replication.log" 2>&1
docker exec "$primary_container" psql -U postgres_failover -d postgres_failover \
  -v ON_ERROR_STOP=1 -c "ALTER SYSTEM SET synchronous_commit = 'on';" \
  >>"$artifact_dir/enable-synchronous-replication.log" 2>&1
docker exec "$primary_container" psql -U postgres_failover -d postgres_failover \
  -v ON_ERROR_STOP=1 -c "SELECT pg_reload_conf();" \
  >>"$artifact_dir/enable-synchronous-replication.log" 2>&1

for attempt in $(seq 1 60); do
  if [[ "$(docker exec "$primary_container" psql -U postgres_failover -d postgres_failover -Atqc "SELECT count(*) FROM pg_stat_replication WHERE state = 'streaming' AND sync_state = 'sync'" 2>/dev/null)" == "1" ]]; then
    break
  fi
  if [[ "$attempt" == 60 ]]; then
    echo "replica did not become synchronous" >&2
    exit 1
  fi
  sleep 0.5
done

image_id="$(docker image inspect "$postgres_image" --format '{{.Id}}')"
source_commit="$(git rev-parse HEAD 2>/dev/null || printf unknown)"

export ASTRONOMER_POSTGRES_FAILOVER_PRIMARY_URL="$primary_url"
export ASTRONOMER_POSTGRES_FAILOVER_REPLICA_URL="$replica_url"
export ASTRONOMER_POSTGRES_FAILOVER_PRIMARY_CONTAINER="$primary_container"
export ASTRONOMER_POSTGRES_FAILOVER_REPLICA_CONTAINER="$replica_container"
export ASTRONOMER_POSTGRES_FAILOVER_DEDICATED=1
export ASTRONOMER_POSTGRES_FAILOVER_EVIDENCE_FILE="$artifact_dir/evidence.json"
export ASTRONOMER_POSTGRES_FAILOVER_RPO_MAX_ROWS="${POSTGRES_FAILOVER_RPO_MAX_ROWS:-0}"
export ASTRONOMER_POSTGRES_FAILOVER_RTO_MAX_SECONDS="${POSTGRES_FAILOVER_RTO_MAX_SECONDS:-30}"
export ASTRONOMER_POSTGRES_FAILOVER_RUN_ID="$run_id"
export ASTRONOMER_POSTGRES_FAILOVER_SOURCE_COMMIT="$source_commit"
export ASTRONOMER_POSTGRES_FAILOVER_SOURCE_REPOSITORY="${GITHUB_REPOSITORY:-local}"
export ASTRONOMER_POSTGRES_FAILOVER_SOURCE_REF="${GITHUB_REF:-local}"
export ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW="${GITHUB_WORKFLOW:-local}"
export ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW_RUN_ID="${GITHUB_RUN_ID:-local}"
export ASTRONOMER_POSTGRES_FAILOVER_WORKFLOW_RUN_ATTEMPT="${GITHUB_RUN_ATTEMPT:-local}"
export ASTRONOMER_POSTGRES_FAILOVER_IMAGE="$postgres_image"
export ASTRONOMER_POSTGRES_FAILOVER_IMAGE_ID="$image_id"

bootstrap_stage="astronomer_certification"
race_args=()
if [[ "${POSTGRES_FAILOVER_CERTIFICATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi

set +e
go test "${race_args[@]}" ./internal/server \
  -run '^TestPostgresFailoverRecoveryCertification$' -count=1 -timeout=3m -v \
  2>&1 | tee "$artifact_dir/certification.log"
test_status=${PIPESTATUS[0]}
set -e

if [[ ! -s "$artifact_dir/evidence.json" ]]; then
  echo "certification did not emit evidence.json" >&2
  exit 1
fi
python3 - "$artifact_dir/evidence.json" <<'PY'
import json
import sys

path = sys.argv[1]
with open(path, encoding="utf-8") as handle:
    evidence = json.load(handle)
assert evidence["schema_version"] == "astronomer-postgres-failover-certification/v1"
assert evidence["status"] in {"pass", "fail"}
assert evidence["thresholds"]["rpo_max_rows"] >= 0
assert evidence["thresholds"]["rto_max_ms"] > 0
assert evidence["provenance"]["source_commit"]
assert evidence["timestamps"]["started_at"]
assert evidence["timestamps"]["completed_at"]
if evidence["status"] == "pass":
    assert evidence["measurements"]["rpo_rows"] <= evidence["thresholds"]["rpo_max_rows"]
    assert evidence["measurements"]["rto_ms"] <= evidence["thresholds"]["rto_max_ms"]
PY

if [[ "$test_status" -ne 0 ]]; then
  exit "$test_status"
fi

test_status=0
printf 'test-postgres-failover-certification: PASS\n'
