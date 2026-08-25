#!/usr/bin/env bash
# Prove server-owned tunnel task failover and standalone-worker isolation using
# disposable PostgreSQL and Redis containers.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

for tool in docker go; do
  command -v "$tool" >/dev/null 2>&1 || {
    echo "test-tunnel-queue-ha: missing $tool" >&2
    exit 2
  }
done

WORK_DIR="$(mktemp -d)"
POSTGRES_CONTAINER="astronomer-tunnel-ha-pg-$$"
REDIS_CONTAINER="astronomer-tunnel-ha-redis-$$"
cleanup() {
  docker stop "$POSTGRES_CONTAINER" "$REDIS_CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

docker run -d --rm --name "$POSTGRES_CONTAINER" \
  -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=astronomer \
  -p 127.0.0.1::5432 pgvector/pgvector:pg17 >/dev/null
docker run -d --rm --name "$REDIS_CONTAINER" \
  -p 127.0.0.1::6379 redis:7-alpine >/dev/null

for _ in $(seq 1 60); do
  docker exec "$POSTGRES_CONTAINER" pg_isready -U astro -d astronomer >/dev/null 2>&1 &&
    docker exec "$REDIS_CONTAINER" redis-cli ping >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$POSTGRES_CONTAINER" pg_isready -U astro -d astronomer >/dev/null
docker exec "$REDIS_CONTAINER" redis-cli ping >/dev/null

POSTGRES_PORT="$(docker port "$POSTGRES_CONTAINER" 5432/tcp | awk -F: 'NR==1 {print $NF}')"
REDIS_PORT="$(docker port "$REDIS_CONTAINER" 6379/tcp | awk -F: 'NR==1 {print $NF}')"
DATABASE_URL="postgres://astro:astro@127.0.0.1:$POSTGRES_PORT/astronomer?sslmode=disable"
REDIS_URL="redis://127.0.0.1:$REDIS_PORT/0"

go build -trimpath -o "$WORK_DIR/migrator" ./cmd/migrator
"$WORK_DIR/migrator" -database "$DATABASE_URL" -path internal/db/migrations up >/dev/null

ASTRONOMER_TUNNEL_HA_TEST_DATABASE_URL="$DATABASE_URL" \
ASTRONOMER_TUNNEL_HA_TEST_REDIS_URL="$REDIS_URL" \
ASTRONOMER_TUNNEL_HA_TEST_ALLOW_DESTRUCTIVE=1 \
go test ./internal/worker -run '^TestTunnelQueueHAIntegration$' -count=1 -timeout=60s

echo "test-tunnel-queue-ha: PASS"
