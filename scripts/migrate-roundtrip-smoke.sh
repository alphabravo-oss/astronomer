#!/usr/bin/env bash
# Apply every ordered migration, exercise every reversible down/up edge, reverse
# the disposable schema completely, and re-apply it. With Docker, the same
# contract runs on PostgreSQL 16 and 17 and compares a normalized catalog + seed
# signature across both supported majors.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
. scripts/lib/docker-test-endpoint.sh

mapfile -t UP_FILES < <(find internal/db/migrations -maxdepth 1 -name '*.up.sql' -type f | sort)
mapfile -t DOWN_FILES < <(find internal/db/migrations -maxdepth 1 -name '*.down.sql' -type f | sort)
if [[ ${#UP_FILES[@]} -eq 0 || ${#UP_FILES[@]} -ne ${#DOWN_FILES[@]} ]]; then
  echo "expected a non-empty, paired set of .up.sql and .down.sql migrations" >&2
  exit 1
fi
for up_file in "${UP_FILES[@]}"; do
  down_file="${up_file%.up.sql}.down.sql"
  if [[ ! -f "$down_file" ]]; then
    echo "missing down migration paired with ${up_file##*/}" >&2
    exit 1
  fi
done
latest_name="${UP_FILES[${#UP_FILES[@]}-1]##*/}"
EXPECTED_VERSION="$((10#${latest_name%%_*}))"

if [[ -z "${DATABASE_URL:-}" ]] && ! command -v docker >/dev/null 2>&1; then
  echo "SKIP: Docker or DATABASE_URL is required for the live round-trip" >&2
  exit 0
fi

ARTIFACT_DIR="$(mktemp -d)"
ACTIVE_CONTAINERS=()
cleanup() {
  for container in "${ACTIVE_CONTAINERS[@]}"; do
    docker stop "$container" >/dev/null 2>&1 || true
  done
  rm -rf "$ARTIFACT_DIR"
}
trap cleanup EXIT

# Exercise the shipped, dependency-locked migrator, including its advisory-lock
# semantics. Never select a different CLI from PATH or install an external one.
MIGRATE_BIN="$ARTIFACT_DIR/astronomer-migrate"
go build -trimpath -o "$MIGRATE_BIN" ./cmd/migrator

if [[ -n "${DATABASE_URL:-}" ]]; then
  echo "migrate all up/latest down/up against supplied DATABASE_URL"
  "$MIGRATE_BIN" -database "$DATABASE_URL" -path internal/db/migrations up
  [[ "$("$MIGRATE_BIN" -database "$DATABASE_URL" -path internal/db/migrations version 2>/dev/null | awk '{print $1}')" == "$EXPECTED_VERSION" ]]
  "$MIGRATE_BIN" -database "$DATABASE_URL" -path internal/db/migrations down 1
  "$MIGRATE_BIN" -database "$DATABASE_URL" -path internal/db/migrations up 1
  echo "migrate-roundtrip-smoke: supplied database OK"
  exit 0
fi

schema_signature_sql="
WITH catalog AS (
  SELECT 'column|' || table_name || '|' || ordinal_position || '|' || column_name || '|' || data_type || '|' || is_nullable || '|' || COALESCE(column_default, '') AS item
  FROM information_schema.columns
  WHERE table_schema = 'public' AND table_name <> 'schema_migrations'
  UNION ALL
  SELECT 'constraint|' || c.relname || '|' || con.conname || '|' || pg_get_constraintdef(con.oid, true)
  FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
  UNION ALL
  SELECT 'index|' || tablename || '|' || indexname || '|' || indexdef
  FROM pg_indexes WHERE schemaname = 'public'
)
SELECT md5(string_agg(item, E'\\n' ORDER BY item)) FROM catalog;"

seed_signature_sql="
SELECT string_agg(signature, E'\\n' ORDER BY signature) FROM (
  SELECT 'global_roles|' || count(*) || '|' || md5(COALESCE(string_agg((to_jsonb(t) - 'created_at' - 'updated_at')::text, '' ORDER BY id), '')) AS signature FROM global_roles t
  UNION ALL SELECT 'cluster_roles|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) FROM cluster_roles t
  UNION ALL SELECT 'project_roles|' || count(*) || '|' || md5(COALESCE(string_agg((to_jsonb(t) - 'created_at' - 'updated_at')::text, '' ORDER BY id), '')) FROM project_roles t
  -- Role, curated-tool, and corrective Helm-repository migrations use
  -- clock-based created/updated audit timestamps. Those timestamps are
  -- expected to change on a destructive rebuild; hash the durable seed
  -- contract, not wall-clock time.
  UNION ALL SELECT 'cluster_tools|' || count(*) || '|' || md5(COALESCE(string_agg((to_jsonb(t) - 'created_at' - 'updated_at')::text, '' ORDER BY id), '')) FROM cluster_tools t
  UNION ALL SELECT 'platform_settings|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY key), '')) FROM platform_settings t
  UNION ALL SELECT 'helm_repositories|' || count(*) || '|' || md5(COALESCE(string_agg((to_jsonb(t) - 'created_at' - 'updated_at')::text, '' ORDER BY id), '')) FROM helm_repositories t
) signatures;"

run_major() {
  local major="$1"
  local container="astronomer-migration-pg${major}-$$"
  local database_url port first_schema first_seeds second_schema second_seeds remaining_tables version

  docker run -d --rm --name "$container" \
    -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=astro \
    -p "${DOCKER_TEST_BIND_HOST}::5432" "pgvector/pgvector:pg${major}" >/dev/null
  ACTIVE_CONTAINERS+=("$container")
  for _ in $(seq 1 60); do
    if docker exec "$container" pg_isready -U astro -d astro >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  docker exec "$container" pg_isready -U astro -d astro >/dev/null
  port="$(docker port "$container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
  database_url="postgres://astro:astro@${DOCKER_TEST_CONNECT_HOST}:${port}/astro?sslmode=disable"

  echo "PostgreSQL ${major}: up"
  "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations up
  [[ "$(docker exec "$container" psql -X -U astro -d astro -Atc 'SELECT version || '"'"'|'"'"' || CASE WHEN dirty THEN '"'"'t'"'"' ELSE '"'"'f'"'"' END FROM schema_migrations')" == "${EXPECTED_VERSION}|f" ]]
  echo "PostgreSQL ${major}: latest-schema durable JSON governance"
  docker exec -i "$container" psql -X -q -U astro -d astro \
    < scripts/testdata/durable-json-governance-postgres-smoke.sql
  echo "PostgreSQL ${major}: transactional audit outbox rollback/replay smoke"
  docker exec -i "$container" psql -X -q -U astro -d astro \
    < scripts/testdata/audit-outbox-postgres-smoke.sql
  echo "PostgreSQL ${major}: durable audit-to-SIEM fan-out/replay smoke"
  AUDIT_OUTBOX_TEST_DATABASE_URL="$database_url" \
    go test ./internal/db/sqlc -run 'Test(AuditOutboxDeliveryDurablyFansOutToMatchingSIEMForwarders|LoggingPipelineOutputsAreClusterScopedTransactionalAndDeleteRestricted)$' -count=1
  first_schema="$(docker exec "$container" psql -X -U astro -d astro -Atc "$schema_signature_sql")"
  first_seeds="$(docker exec "$container" psql -X -U astro -d astro -Atc "$seed_signature_sql")"

  echo "PostgreSQL ${major}: every reversible down/up edge"
  for ((version=EXPECTED_VERSION; version>=1; version--)); do
    "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations down 1
    if (( version > 1 )); then
      [[ "$(docker exec "$container" psql -X -U astro -d astro -Atc 'SELECT version || '"'"'|'"'"' || CASE WHEN dirty THEN '"'"'t'"'"' ELSE '"'"'f'"'"' END FROM schema_migrations')" == "$((version-1))|f" ]]
    fi
    "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations up 1
    [[ "$(docker exec "$container" psql -X -U astro -d astro -Atc 'SELECT version || '"'"'|'"'"' || CASE WHEN dirty THEN '"'"'t'"'"' ELSE '"'"'f'"'"' END FROM schema_migrations')" == "${version}|f" ]]
    "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations down 1
  done
  remaining_tables="$(docker exec "$container" psql -X -U astro -d astro -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename <> 'schema_migrations'")"
  [[ "$remaining_tables" == "0" ]]

  echo "PostgreSQL ${major}: re-apply"
  "$MIGRATE_BIN" -database "$database_url" -path internal/db/migrations up
  docker exec -i "$container" psql -X -q -U astro -d astro \
    < scripts/testdata/durable-json-governance-postgres-smoke.sql
  second_schema="$(docker exec "$container" psql -X -U astro -d astro -Atc "$schema_signature_sql")"
  second_seeds="$(docker exec "$container" psql -X -U astro -d astro -Atc "$seed_signature_sql")"
  if [[ "$first_schema" != "$second_schema" ]]; then
    echo "PostgreSQL ${major}: schema signature changed after full down/up" >&2
    diff -u <(printf '%s\n' "$first_schema") <(printf '%s\n' "$second_schema") >&2 || true
    return 1
  fi
  if [[ "$first_seeds" != "$second_seeds" ]]; then
    echo "PostgreSQL ${major}: seed signature changed after full down/up" >&2
    diff -u <(printf '%s\n' "$first_seeds") <(printf '%s\n' "$second_seeds") >&2 || true
    return 1
  fi
  printf '%s\n' "$first_schema" >"$ARTIFACT_DIR/schema-${major}"
  printf '%s\n' "$first_seeds" >"$ARTIFACT_DIR/seeds-${major}"

  docker stop "$container" >/dev/null
  ACTIVE_CONTAINERS=("${ACTIVE_CONTAINERS[@]/$container}")
}

run_major 16
run_major 17
diff -u "$ARTIFACT_DIR/schema-16" "$ARTIFACT_DIR/schema-17"
diff -u "$ARTIFACT_DIR/seeds-16" "$ARTIFACT_DIR/seeds-17"
echo "migrate-roundtrip-smoke: PostgreSQL 16/17 schema and seed signatures match"
