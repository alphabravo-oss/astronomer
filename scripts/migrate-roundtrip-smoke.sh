#!/usr/bin/env bash
# Apply the canonical greenfield schema, reverse it completely, and re-apply it.
# With Docker, the same contract runs on PostgreSQL 16 and 17 and compares a
# normalized catalog + seed signature across both supported majors.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

mapfile -t UP_FILES < <(find internal/db/migrations -maxdepth 1 -name '*.up.sql' -type f | sort)
mapfile -t DOWN_FILES < <(find internal/db/migrations -maxdepth 1 -name '*.down.sql' -type f | sort)
if [[ ${#UP_FILES[@]} -ne 1 || ${#DOWN_FILES[@]} -ne 1 || ${UP_FILES[0]##*/} != 001_initial.up.sql || ${DOWN_FILES[0]##*/} != 001_initial.down.sql ]]; then
  echo "expected exactly 001_initial.up.sql and 001_initial.down.sql" >&2
  exit 1
fi

if ! command -v migrate >/dev/null 2>&1 && [[ -x "$(go env GOPATH)/bin/migrate" ]]; then
  export PATH="$(go env GOPATH)/bin:$PATH"
fi
if ! command -v migrate >/dev/null 2>&1; then
  echo "installing golang-migrate CLI via go install..."
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1
  export PATH="$(go env GOPATH)/bin:$PATH"
fi

if [[ -n "${DATABASE_URL:-}" ]]; then
  echo "migrate up/down/up against supplied DATABASE_URL"
  migrate -database "$DATABASE_URL" -path internal/db/migrations up
  migrate -database "$DATABASE_URL" -path internal/db/migrations down 1
  migrate -database "$DATABASE_URL" -path internal/db/migrations up
  echo "migrate-roundtrip-smoke: supplied database OK"
  exit 0
fi

if ! command -v docker >/dev/null 2>&1; then
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
  SELECT 'global_roles|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) AS signature FROM global_roles t
  UNION ALL SELECT 'cluster_roles|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) FROM cluster_roles t
  UNION ALL SELECT 'project_roles|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) FROM project_roles t
  UNION ALL SELECT 'cluster_tools|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) FROM cluster_tools t
  UNION ALL SELECT 'platform_settings|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY key), '')) FROM platform_settings t
  UNION ALL SELECT 'helm_repositories|' || count(*) || '|' || md5(COALESCE(string_agg(row_to_json(t)::text, '' ORDER BY id), '')) FROM helm_repositories t
) signatures;"

run_major() {
  local major="$1"
  local container="astronomer-migration-pg${major}-$$"
  local database_url port first_schema first_seeds remaining_tables

  docker run -d --rm --name "$container" \
    -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=astro \
    -p 127.0.0.1::5432 "pgvector/pgvector:pg${major}" >/dev/null
  ACTIVE_CONTAINERS+=("$container")
  for _ in $(seq 1 60); do
    if docker exec "$container" pg_isready -U astro -d astro >/dev/null 2>&1; then
      break
    fi
    sleep 1
  done
  docker exec "$container" pg_isready -U astro -d astro >/dev/null
  port="$(docker port "$container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
  database_url="postgres://astro:astro@127.0.0.1:${port}/astro?sslmode=disable"

  echo "PostgreSQL ${major}: up"
  migrate -database "$database_url" -path internal/db/migrations up
  [[ "$(docker exec "$container" psql -X -U astro -d astro -Atc 'SELECT version || '"'"'|'"'"' || CASE WHEN dirty THEN '"'"'t'"'"' ELSE '"'"'f'"'"' END FROM schema_migrations')" == "1|f" ]]
  first_schema="$(docker exec "$container" psql -X -U astro -d astro -Atc "$schema_signature_sql")"
  first_seeds="$(docker exec "$container" psql -X -U astro -d astro -Atc "$seed_signature_sql")"

  echo "PostgreSQL ${major}: down"
  migrate -database "$database_url" -path internal/db/migrations down 1
  remaining_tables="$(docker exec "$container" psql -X -U astro -d astro -Atc "SELECT count(*) FROM pg_tables WHERE schemaname='public' AND tablename <> 'schema_migrations'")"
  [[ "$remaining_tables" == "0" ]]

  echo "PostgreSQL ${major}: re-apply"
  migrate -database "$database_url" -path internal/db/migrations up
  [[ "$first_schema" == "$(docker exec "$container" psql -X -U astro -d astro -Atc "$schema_signature_sql")" ]]
  [[ "$first_seeds" == "$(docker exec "$container" psql -X -U astro -d astro -Atc "$seed_signature_sql")" ]]
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
