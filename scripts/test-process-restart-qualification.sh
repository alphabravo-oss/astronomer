#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

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

docker run -d --rm --name "$postgres_container" \
  -e POSTGRES_USER=process_restart \
  -e POSTGRES_PASSWORD="$credential" \
  -e POSTGRES_DB=process_restart \
  -p 127.0.0.1::5432 pgvector/pgvector:pg16 >/dev/null
docker run -d --rm --name "$redis_container" \
  -p 127.0.0.1::6379 redis:7-alpine >/dev/null

for attempt in $(seq 1 60); do
  if docker exec "$postgres_container" pg_isready -U process_restart -d process_restart >/dev/null 2>&1 && \
     docker exec "$redis_container" redis-cli ping >/dev/null 2>&1; then
    break
  fi
  if [[ "$attempt" == 60 ]]; then
    docker logs "$postgres_container" >&2 || true
    docker logs "$redis_container" >&2 || true
    exit 1
  fi
  sleep 0.5
done

postgres_port="$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
redis_port="$(docker port "$redis_container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')"
export ASTRONOMER_PROCESS_RESTART_DATABASE_URL="postgres://process_restart:${credential}@127.0.0.1:${postgres_port}/process_restart?sslmode=disable"
export ASTRONOMER_PROCESS_RESTART_REDIS_URL="redis://127.0.0.1:${redis_port}/0"
export ASTRONOMER_PROCESS_RESTART_DEDICATED=1

go build -trimpath -o "$work_dir/migrator" ./cmd/migrator
"$work_dir/migrator" -database "$ASTRONOMER_PROCESS_RESTART_DATABASE_URL" -path internal/db/migrations up >/dev/null

race_args=()
if [[ "${PROCESS_RESTART_QUALIFICATION_RACE:-0}" == "1" ]]; then
  race_args=(-race)
fi
go test "${race_args[@]}" ./internal/worker \
  -run '^TestProcessRestartQualification$' -count=1 -timeout=3m -v
