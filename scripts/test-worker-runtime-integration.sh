#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for the worker runtime integration test." >&2
  exit 1
fi

suffix="$$-$(date +%s)"
postgres_container="astronomer-worker-int-pg-${suffix}"
redis_container="astronomer-worker-int-redis-${suffix}"
credential="$(openssl rand -hex 18 2>/dev/null || printf 'worker-int-%s' "$suffix")"

cleanup() {
  docker rm -f "$postgres_container" "$redis_container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker run -d --rm --name "$postgres_container" \
  -e POSTGRES_USER=worker_test \
  -e POSTGRES_PASSWORD="$credential" \
  -e POSTGRES_DB=worker_test \
  -p 127.0.0.1::5432 pgvector/pgvector:pg16 >/dev/null
docker run -d --rm --name "$redis_container" \
  -p 127.0.0.1::6379 redis:7-alpine >/dev/null

for _ in $(seq 1 60); do
  if docker exec "$postgres_container" pg_isready -U worker_test -d worker_test >/dev/null 2>&1 && \
     docker exec "$redis_container" redis-cli ping >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "$postgres_container" pg_isready -U worker_test -d worker_test >/dev/null
docker exec "$redis_container" redis-cli ping >/dev/null

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')"
export ASTRONOMER_WORKER_INTEGRATION_DATABASE_URL="postgres://worker_test:${credential}@127.0.0.1:${postgres_port}/worker_test?sslmode=disable"
export ASTRONOMER_WORKER_INTEGRATION_REDIS_URL="redis://127.0.0.1:${redis_port}/0"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"; cleanup' EXIT
go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_WORKER_INTEGRATION_DATABASE_URL" -path internal/db/migrations up >/dev/null

race_args=()
if [[ "${WORKER_INTEGRATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
go test "${race_args[@]}" ./internal/worker \
	-run '^TestWorkerPostgresRedisIntegration$' -count=1 -timeout=5m
