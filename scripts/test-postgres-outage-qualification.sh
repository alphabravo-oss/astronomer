#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
. scripts/lib/docker-test-endpoint.sh

for tool in docker go openssl; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

suffix="$$-$(date +%s)"
postgres_container="astronomer-postgres-outage-$suffix"
credential="$(openssl rand -hex 18)"
work_dir="$(mktemp -d)"

cleanup() {
  docker rm -f "$postgres_container" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

# Docker reallocates an anonymous published port whenever a stopped container
# starts again. This drill keeps one long-lived pgx pool across the restart, so
# it needs an explicit host port. Retry a narrow randomized allocation instead
# of weakening the test by rebuilding the pool against a new endpoint.
postgres_port=""
for attempt in $(seq 1 20); do
  port_seed="$(openssl rand -hex 2)"
  candidate_port=$((20000 + 16#$port_seed % 40000))
  if docker run -d --name "$postgres_container" \
    --label astronomer.qualification=postgres-outage \
    -e POSTGRES_USER=postgres_outage \
    -e POSTGRES_PASSWORD="$credential" \
    -e POSTGRES_DB=postgres_outage \
    -p "${DOCKER_TEST_BIND_HOST}:${candidate_port}:5432" pgvector/pgvector:pg16 >/dev/null 2>&1; then
    postgres_port="$candidate_port"
    break
  fi
  docker rm -f "$postgres_container" >/dev/null 2>&1 || true
done
if [[ -z "$postgres_port" ]]; then
  echo "could not allocate a stable PostgreSQL qualification port" >&2
  exit 1
fi

for attempt in $(seq 1 60); do
  if docker exec "$postgres_container" pg_isready -U postgres_outage -d postgres_outage >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == 60 ]]; then
    docker logs "$postgres_container" >&2 || true
    exit 1
  fi
  sleep 0.5
done

export ASTRONOMER_POSTGRES_OUTAGE_DATABASE_URL="postgres://postgres_outage:${credential}@${DOCKER_TEST_CONNECT_HOST}:${postgres_port}/postgres_outage?sslmode=disable"
export ASTRONOMER_POSTGRES_OUTAGE_CONTAINER="$postgres_container"
export ASTRONOMER_POSTGRES_OUTAGE_DEDICATED=1

go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_POSTGRES_OUTAGE_DATABASE_URL" -path internal/db/migrations up >/dev/null

race_args=()
if [[ "${POSTGRES_OUTAGE_QUALIFICATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
go test "${race_args[@]}" ./internal/server \
  -run '^TestPostgresOutageQualification$' -count=1 -timeout=3m -v
