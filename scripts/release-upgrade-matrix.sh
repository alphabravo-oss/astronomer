#!/usr/bin/env bash
# Reconstruct each supported release-line schema fixture from the immutable
# migration history, seed representative durable data, and upgrade it to the
# current schema on PostgreSQL 16 and 17.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FIXTURE_DIR="scripts/testdata/upgrade-fixtures"
mapfile -t FIXTURES < <(find "$FIXTURE_DIR" -maxdepth 1 -name '*.json' -type f | sort)
mapfile -t UP_FILES < <(find internal/db/migrations -maxdepth 1 -name '*.up.sql' -type f | sort)
[[ ${#FIXTURES[@]} -ge 2 ]] || { echo "release-upgrade-matrix: expected at least two release-line fixtures" >&2; exit 1; }
[[ ${#UP_FILES[@]} -gt 0 ]] || { echo "release-upgrade-matrix: no migrations found" >&2; exit 1; }

latest_name="${UP_FILES[${#UP_FILES[@]}-1]##*/}"
TARGET_SCHEMA="$((10#${latest_name%%_*}))"

for tool in docker jq go; do
  command -v "$tool" >/dev/null 2>&1 || { echo "release-upgrade-matrix: missing $tool" >&2; exit 1; }
done

ACTIVE_CONTAINERS=()
ARTIFACT_DIR="$(mktemp -d)"
MIGRATE_BIN="$ARTIFACT_DIR/astronomer-migrate"
go build -trimpath -o "$MIGRATE_BIN" ./cmd/migrator
cleanup() {
  for container in "${ACTIVE_CONTAINERS[@]}"; do
    docker stop "$container" >/dev/null 2>&1 || true
  done
  rm -rf "$ARTIFACT_DIR"
}
trap cleanup EXIT

validate_fixture() {
  local fixture="$1" fixture_id release_line schema_version cluster_id
  fixture_id="$(jq -er '.fixture_id | strings' "$fixture")"
  release_line="$(jq -er '.release_line | strings' "$fixture")"
  schema_version="$(jq -er '.schema_version | numbers' "$fixture")"
  cluster_id="$(jq -er '.cluster_id | strings' "$fixture")"
  [[ "$release_line" =~ ^1\.[0-9]+\.x$ ]] || { echo "invalid release_line in $fixture" >&2; exit 1; }
  [[ "$schema_version" =~ ^[1-9][0-9]*$ ]] || { echo "invalid schema_version in $fixture" >&2; exit 1; }
  ((schema_version < TARGET_SCHEMA)) || { echo "$fixture must represent a pre-current schema" >&2; exit 1; }
  [[ "$cluster_id" =~ ^[0-9a-fA-F-]{36}$ ]] || { echo "invalid cluster_id in $fixture" >&2; exit 1; }
  [[ -n "$fixture_id" ]]
}
for fixture in "${FIXTURES[@]}"; do validate_fixture "$fixture"; done

run_major() {
  local major="$1" container="astronomer-upgrade-matrix-pg${1}-$$" port
  docker run -d --rm --name "$container" \
    -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=postgres \
    -p 127.0.0.1::5432 "pgvector/pgvector:pg${major}" >/dev/null
  ACTIVE_CONTAINERS+=("$container")
  for _ in $(seq 1 60); do
    docker exec "$container" pg_isready -U astro -d postgres >/dev/null 2>&1 && break
    sleep 1
  done
  docker exec "$container" pg_isready -U astro -d postgres >/dev/null
  port="$(docker port "$container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"

  for fixture in "${FIXTURES[@]}"; do
    local fixture_id release_line schema_version cluster_id database_name database_url state
    fixture_id="$(jq -r '.fixture_id' "$fixture")"
    release_line="$(jq -r '.release_line' "$fixture")"
    schema_version="$(jq -r '.schema_version' "$fixture")"
    cluster_id="$(jq -r '.cluster_id' "$fixture")"
    database_name="fixture_${major}_${release_line//./_}"
    docker exec "$container" createdb -U astro "$database_name"
    database_url="postgres://astro:astro@127.0.0.1:${port}/${database_name}?sslmode=disable"

    echo "PostgreSQL ${major}: ${release_line} schema ${schema_version} -> ${TARGET_SCHEMA}"
    "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations up "$schema_version"
    state="$(docker exec "$container" psql -X -U astro -d "$database_name" -Atc "SELECT version || '|' || CASE WHEN dirty THEN 't' ELSE 'f' END FROM schema_migrations")"
    [[ "$state" == "${schema_version}|f" ]]
    docker exec -i "$container" psql -X -U astro -d "$database_name" \
      -v fixture_id="$fixture_id" -v schema_version="$schema_version" -v cluster_id="$cluster_id" \
      <"$FIXTURE_DIR/seed.sql"

    "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations up
    state="$(docker exec "$container" psql -X -U astro -d "$database_name" -Atc "SELECT version || '|' || CASE WHEN dirty THEN 't' ELSE 'f' END FROM schema_migrations")"
    [[ "$state" == "${TARGET_SCHEMA}|f" ]]
    docker exec -i "$container" psql -X -U astro -d "$database_name" \
      -v fixture_id="$fixture_id" -v schema_version="$schema_version" -v cluster_id="$cluster_id" \
      <"$FIXTURE_DIR/assert-after-upgrade.sql"
  done

  local concurrent_db="concurrent_${major}" concurrent_url pid_a pid_b status_a status_b
  concurrent_db="concurrent_${major}"
  docker exec "$container" createdb -U astro "$concurrent_db"
  concurrent_url="postgres://astro:astro@127.0.0.1:${port}/${concurrent_db}?sslmode=disable"
  echo "PostgreSQL ${major}: concurrent migration installers serialize"
  "$MIGRATE_BIN" -database "$concurrent_url" -path internal/db/migrations up >"$ARTIFACT_DIR/concurrent-${major}-a.log" 2>&1 &
  pid_a=$!
  "$MIGRATE_BIN" -database "$concurrent_url" -path internal/db/migrations up >"$ARTIFACT_DIR/concurrent-${major}-b.log" 2>&1 &
  pid_b=$!
  status_a=0
  status_b=0
  wait "$pid_a" || status_a=$?
  wait "$pid_b" || status_b=$?
  if ((status_a != 0 || status_b != 0)); then
    sed -n '1,120p' "$ARTIFACT_DIR/concurrent-${major}-a.log" >&2
    sed -n '1,120p' "$ARTIFACT_DIR/concurrent-${major}-b.log" >&2
    echo "concurrent migration installers failed: ${status_a}/${status_b}" >&2
    exit 1
  fi
  state="$(docker exec "$container" psql -X -U astro -d "$concurrent_db" -Atc "SELECT version || '|' || CASE WHEN dirty THEN 't' ELSE 'f' END FROM schema_migrations")"
  [[ "$state" == "${TARGET_SCHEMA}|f" ]]

  local interrupt_db="interrupt_${major}" interrupt_url interrupt_pid migration_backend_pid interrupted_tables
  interrupt_db="interrupt_${major}"
  docker exec "$container" createdb -U astro "$interrupt_db"
  docker exec "$container" psql -X -U astro -d "$interrupt_db" -v ON_ERROR_STOP=1 \
    -c 'CREATE TABLE migration_test_control (should_sleep boolean NOT NULL); INSERT INTO migration_test_control VALUES (true);' >/dev/null
  interrupt_url="postgres://astro:astro@127.0.0.1:${port}/${interrupt_db}?sslmode=disable"
  echo "PostgreSQL ${major}: interrupted transactional migration rolls back and retries cleanly"
  "$MIGRATE_BIN" -database "$interrupt_url" -path scripts/testdata/migration-interruption up >"$ARTIFACT_DIR/interruption-${major}.log" 2>&1 &
  interrupt_pid=$!
  migration_backend_pid=""
  for _ in $(seq 1 50); do
    migration_backend_pid="$(docker exec "$container" psql -X -U astro -d postgres -Atc \
      "SELECT pid FROM pg_stat_activity WHERE datname='${interrupt_db}' AND state='active' AND query LIKE '%migration_interrupt_before_sleep%' ORDER BY query_start LIMIT 1")"
    [[ -n "$migration_backend_pid" ]] && break
    sleep 0.1
  done
  [[ -n "$migration_backend_pid" ]] || { echo "could not identify the in-flight migration backend" >&2; exit 1; }
  docker exec "$container" psql -X -U astro -d postgres -Atc \
    "SELECT pg_terminate_backend(${migration_backend_pid})" | grep -qx t
  if wait "$interrupt_pid"; then
    echo "interruption fixture succeeded after its database backend was terminated" >&2
    exit 1
  fi
  interrupted_tables="$(docker exec "$container" psql -X -U astro -d "$interrupt_db" -Atc \
    "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'migration_interrupt_%'")"
  [[ "$interrupted_tables" == 0 ]] || { echo "interrupted migration left partial DDL" >&2; exit 1; }
  # The interrupted first migration has no valid predecessor. Reset to the
  # golang-migrate NilVersion sentinel (-1), not a synthetic version 0 for
  # which no source file exists, before retrying from version 1.
  "$MIGRATE_BIN" -database "$interrupt_url" -path scripts/testdata/migration-interruption force -1
  docker exec "$container" psql -X -U astro -d "$interrupt_db" -c 'UPDATE migration_test_control SET should_sleep = false' >/dev/null
  "$MIGRATE_BIN" -database "$interrupt_url" -path scripts/testdata/migration-interruption up
  state="$(docker exec "$container" psql -X -U astro -d "$interrupt_db" -Atc "SELECT version || '|' || CASE WHEN dirty THEN 't' ELSE 'f' END FROM schema_migrations")"
  [[ "$state" == "1|f" ]]
  interrupted_tables="$(docker exec "$container" psql -X -U astro -d "$interrupt_db" -Atc \
    "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename LIKE 'migration_interrupt_%'")"
  [[ "$interrupted_tables" == 2 ]]

  docker stop "$container" >/dev/null
  ACTIVE_CONTAINERS=("${ACTIVE_CONTAINERS[@]/$container}")
}

run_major 16
run_major 17
echo "release-upgrade-matrix: all supported release-line fixtures upgraded to schema ${TARGET_SCHEMA}"
