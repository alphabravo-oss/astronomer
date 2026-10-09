#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
. scripts/lib/qualification-runtime.sh

if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is required for the worker runtime integration test." >&2
  exit 1
fi

suffix="$$-$(date +%s)"
postgres_container="astronomer-worker-int-pg-${suffix}"
redis_container="astronomer-worker-int-redis-${suffix}"
credential="$(openssl rand -hex 24)"

cleanup() {
  docker rm -f "$postgres_container" "$redis_container" >/dev/null 2>&1 || true
}
trap cleanup EXIT

qualification_postgres_run "$postgres_container" worker_test "$credential" worker_test
qualification_valkey_run "$redis_container" "$credential"
qualification_postgres_ready "$postgres_container" worker_test worker_test
qualification_valkey_ready "$redis_container"

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')"
export ASTRONOMER_WORKER_INTEGRATION_DATABASE_URL="postgres://worker_test:${credential}@${DOCKER_TEST_CONNECT_HOST}:${postgres_port}/worker_test?sslmode=disable"
export ASTRONOMER_WORKER_INTEGRATION_REDIS_URL="redis://:${credential}@${DOCKER_TEST_CONNECT_HOST}:${redis_port}/0"

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"; cleanup' EXIT
go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_WORKER_INTEGRATION_DATABASE_URL" -path internal/db/migrations up >/dev/null

race_args=()
if [[ "${WORKER_INTEGRATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
python3 scripts/qualification-go.py --name worker-runtime --expected TestWorkerPostgresRedisIntegration -- \
  "${race_args[@]}" -p=1 -count=1 -timeout=5m -run '^TestWorkerPostgresRedisIntegration$' ./internal/worker
