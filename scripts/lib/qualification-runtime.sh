#!/usr/bin/env bash
# Shared disposable fixtures. This file never selects an arbitrary image or DB.
qualification_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/lib/docker-test-endpoint.sh
. "$qualification_root/scripts/lib/docker-test-endpoint.sh"

qualification_runtime_value() {
  python3 - "$qualification_root/deploy/qualification/runtime.json" "$@" <<'PY'
import json, re, sys
value = json.load(open(sys.argv[1], encoding="utf-8"))
for key in sys.argv[2:]:
    value = value[key]
if not isinstance(value, str):
    raise SystemExit("qualification runtime value must be a string")
if sys.argv[-1] == "image" and not re.fullmatch(r"[a-z0-9./-]+:[A-Za-z0-9._-]+@sha256:[a-f0-9]{64}", value):
    raise SystemExit("qualification image must have an immutable digest")
print(value)
PY
}

qualification_postgres_major="${POSTGRES_QUALIFICATION_MAJOR:-$(qualification_runtime_value defaultPostgresMajor)}"
case "$qualification_postgres_major" in
  17|18) ;;
  *) echo 'qualification: only declared PostgreSQL 17/18 lanes are allowed' >&2; return 2 ;;
esac
qualification_postgres_image="$(qualification_runtime_value postgres "$qualification_postgres_major" image)"
qualification_valkey_image="$(qualification_runtime_value valkey image)"

qualification_postgres_run() {
  local container="$1" user="$2" password="$3" database="$4" port="${5:-}"
  local entrypoint
  entrypoint="$(docker_test_host_path "$qualification_root/scripts/testdata/qualification/postgres-entrypoint.sh")"
  docker run -d --name "$container" \
    --label astronomer.qualification=disposable-runtime \
    --user 0 --entrypoint /bin/bash \
    -e POSTGRES_USER="$user" -e POSTGRES_PASSWORD="$password" -e POSTGRES_DB="$database" \
    -e QUALIFICATION_POSTGRES_MAJOR="$qualification_postgres_major" \
    -v "$entrypoint:/qualification/postgres-entrypoint.sh:ro" \
    -p "${DOCKER_TEST_BIND_HOST}:${port}:5432" \
    "$qualification_postgres_image" /qualification/postgres-entrypoint.sh >/dev/null
}

qualification_postgres_ready() {
  local container="$1" user="$2" database="$3"
  for _ in $(seq 1 90); do
    if docker exec "$container" pg_isready -h 127.0.0.1 -U "$user" -d "$database" >/dev/null 2>&1; then
      # Fail before migrations if the maintained operand lacks a required extension.
      docker exec "$container" psql -X -v ON_ERROR_STOP=1 -U "$user" -d "$database" \
        -c 'CREATE EXTENSION IF NOT EXISTS citext; CREATE EXTENSION IF NOT EXISTS pgcrypto; CREATE EXTENSION IF NOT EXISTS "uuid-ossp"; CREATE EXTENSION IF NOT EXISTS vector;' >/dev/null
      return
    fi
    sleep 0.5
  done
  echo 'qualification: PostgreSQL did not become ready within 45 seconds' >&2
  return 1
}

qualification_valkey_run() {
  local container="$1" password="$2"
  docker run -d --name "$container" \
    --label astronomer.qualification=disposable-runtime \
    -e REDISCLI_AUTH="$password" -p "${DOCKER_TEST_BIND_HOST}::6379" \
    "$qualification_valkey_image" valkey-server --requirepass "$password" \
    --appendonly yes --appendfsync always >/dev/null
}

qualification_valkey_ready() {
  local container="$1"
  for _ in $(seq 1 60); do
    if docker exec "$container" valkey-cli ping 2>/dev/null | rg -q '^PONG$'; then return; fi
    sleep 0.5
  done
  echo 'qualification: authenticated Valkey did not become ready within 30 seconds' >&2
  return 1
}
