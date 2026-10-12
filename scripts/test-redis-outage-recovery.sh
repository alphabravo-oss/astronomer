#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
. scripts/lib/qualification-runtime.sh

for tool in docker go openssl; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

suffix="$$-$(date +%s)"
postgres_container="astronomer-redis-outage-pg-$suffix"
redis_container="astronomer-redis-outage-redis-$suffix"
credential="$(openssl rand -hex 18)"
work_dir="$(mktemp -d)"

cleanup() {
  docker rm -f "$postgres_container" "$redis_container" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

qualification_postgres_run "$postgres_container" redis_outage_test "$credential" redis_outage_test
qualification_valkey_run "$redis_container" "$credential"
qualification_postgres_ready "$postgres_container" redis_outage_test redis_outage_test
qualification_valkey_ready "$redis_container"

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')"
export ASTRONOMER_REDIS_OUTAGE_DATABASE_URL="postgres://redis_outage_test:${credential}@${DOCKER_TEST_CONNECT_HOST}:${postgres_port}/redis_outage_test?sslmode=disable"
export ASTRONOMER_REDIS_OUTAGE_REDIS_URL="redis://:${credential}@${DOCKER_TEST_CONNECT_HOST}:${redis_port}/0"
export ASTRONOMER_REDIS_OUTAGE_CONTAINER="$redis_container"
export ASTRONOMER_REDIS_OUTAGE_DEDICATED=1

go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_REDIS_OUTAGE_DATABASE_URL" -path internal/db/migrations up >/dev/null

docker stop "$redis_container" >/dev/null

race_args=()
if [[ "${REDIS_OUTAGE_RECOVERY_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
python3 scripts/qualification-go.py --name valkey-outage --expected TestRedisOutageRecoveryDurableNotification -- \
  "${race_args[@]}" -p=1 -count=1 -timeout=2m -run '^TestRedisOutageRecoveryDurableNotification$' ./internal/worker
