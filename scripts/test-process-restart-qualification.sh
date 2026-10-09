#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"
. scripts/lib/qualification-runtime.sh

for tool in docker go openssl; do
  command -v "$tool" >/dev/null || { echo "$tool is required" >&2; exit 1; }
done

suffix="$$-$(date +%s)"
postgres_container="astronomer-process-restart-pg-$suffix"
redis_container="astronomer-process-restart-redis-$suffix"
credential="$(openssl rand -hex 18)"
work_dir="$(mktemp -d)"

cleanup() {
  docker rm -f "$postgres_container" "$redis_container" >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT

qualification_postgres_run "$postgres_container" process_restart "$credential" process_restart
qualification_valkey_run "$redis_container" "$credential"
qualification_postgres_ready "$postgres_container" process_restart process_restart
qualification_valkey_ready "$redis_container"

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')"
export ASTRONOMER_PROCESS_RESTART_DATABASE_URL="postgres://process_restart:${credential}@${DOCKER_TEST_CONNECT_HOST}:${postgres_port}/process_restart?sslmode=disable"
export ASTRONOMER_PROCESS_RESTART_REDIS_URL="redis://:${credential}@${DOCKER_TEST_CONNECT_HOST}:${redis_port}/0"
export ASTRONOMER_PROCESS_RESTART_DEDICATED=1

go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_PROCESS_RESTART_DATABASE_URL" -path internal/db/migrations up >/dev/null

race_args=()
if [[ "${PROCESS_RESTART_QUALIFICATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
python3 scripts/qualification-go.py --name process-restart --expected TestProcessRestartQualification -- \
  "${race_args[@]}" -p=1 -count=1 -timeout=3m -run '^TestProcessRestartQualification$' ./internal/worker
