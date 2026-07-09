#!/usr/bin/env bash
# TEST-05: apply all *.up.sql migrations to an empty Postgres, then optionally
# down the last migration. Skips cleanly when no DATABASE_URL/docker/migrate.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

UP_COUNT=$(find internal/db/migrations -name '*.up.sql' | wc -l | tr -d ' ')
if [[ "$UP_COUNT" -lt 100 ]]; then
  echo "expected 100+ up migrations, found $UP_COUNT"
  exit 1
fi
echo "found $UP_COUNT up migrations"

if [[ -z "${DATABASE_URL:-}" ]]; then
  if command -v docker >/dev/null 2>&1; then
    echo "starting ephemeral postgres for migrate roundtrip..."
    CID=$(docker run -d --rm -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=astro -p 55432:5432 postgres:16-alpine)
    cleanup() { docker stop "$CID" >/dev/null 2>&1 || true; }
    trap cleanup EXIT
    for _ in $(seq 1 40); do
      if docker exec "$CID" pg_isready -U astro >/dev/null 2>&1; then break; fi
      sleep 1
    done
    export DATABASE_URL="postgres://astro:astro@127.0.0.1:55432/astro?sslmode=disable"
  else
    echo "SKIP: no DATABASE_URL and no docker — structural migration count gate only (PASS)"
    exit 0
  fi
fi

if ! command -v migrate >/dev/null 2>&1; then
  echo "installing golang-migrate CLI via go install..."
  go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@v4.18.1
  export PATH="$(go env GOPATH)/bin:$PATH"
fi

echo "migrate up against $DATABASE_URL"
migrate -database "$DATABASE_URL" -path internal/db/migrations up
echo "migrate down 1 (partial reverse check)"
migrate -database "$DATABASE_URL" -path internal/db/migrations down 1
echo "migrate up again (re-apply)"
migrate -database "$DATABASE_URL" -path internal/db/migrations up
echo "migrate-roundtrip-smoke: OK"
