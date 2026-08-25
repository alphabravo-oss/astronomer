#!/usr/bin/env bash
# Certify representative fleet and scoped-operation list plans with real
# PostgreSQL statistics, then exercise keyset pagination under concurrent
# inserts on both sides of the last-seen tuple.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

CLUSTER_ROWS="${CLUSTER_ROWS:-100000}"
OPERATION_ROWS="${OPERATION_ROWS:-1000000}"
[[ "$CLUSTER_ROWS" =~ ^[1-9][0-9]*$ ]] || { echo "query-plan-certification: invalid CLUSTER_ROWS" >&2; exit 2; }
[[ "$OPERATION_ROWS" =~ ^[1-9][0-9]*$ ]] || { echo "query-plan-certification: invalid OPERATION_ROWS" >&2; exit 2; }

for tool in docker go grep; do
  command -v "$tool" >/dev/null 2>&1 || { echo "query-plan-certification: missing $tool" >&2; exit 2; }
done

WORK_DIR="$(mktemp -d)"
REPORT_DIR="${REPORT_DIR:-$WORK_DIR/reports}"
mkdir -p "$REPORT_DIR"
CONTAINER="astronomer-query-plan-pg17-$$"
cleanup() {
  docker stop "$CONTAINER" >/dev/null 2>&1 || true
  rm -rf "$WORK_DIR"
}
trap cleanup EXIT

MIGRATE_BIN="$WORK_DIR/astronomer-migrate"
go build -trimpath -o "$MIGRATE_BIN" ./cmd/migrator
docker run -d --rm --name "$CONTAINER" \
  -e POSTGRES_PASSWORD=astro -e POSTGRES_USER=astro -e POSTGRES_DB=astronomer \
  -p 127.0.0.1::5432 pgvector/pgvector:pg17 >/dev/null
for _ in $(seq 1 60); do
  docker exec "$CONTAINER" pg_isready -U astro -d astronomer >/dev/null 2>&1 && break
  sleep 1
done
docker exec "$CONTAINER" pg_isready -U astro -d astronomer >/dev/null
PORT="$(docker port "$CONTAINER" 5432/tcp | awk -F: 'NR == 1 {print $NF}')"
DATABASE_URL="postgres://astro:astro@127.0.0.1:${PORT}/astronomer?sslmode=disable"
"$MIGRATE_BIN" -database "$DATABASE_URL" -path internal/db/migrations up >/dev/null

echo "query-plan-certification: seeding ${CLUSTER_ROWS} clusters and ${OPERATION_ROWS} scoped operations"
docker exec -i "$CONTAINER" psql -X -v ON_ERROR_STOP=1 -U astro -d astronomer \
  -v cluster_rows="$CLUSTER_ROWS" -v operation_rows="$OPERATION_ROWS" >/dev/null <<'SQL'
SET synchronous_commit = off;
INSERT INTO clusters (id, name, display_name, status, created_at, updated_at)
SELECT md5(series::text)::uuid,
       'cluster-' || series,
       'Cluster ' || series,
       CASE WHEN series % 100 = 0 THEN 'connected' ELSE 'disconnected' END,
       timestamptz '2026-01-01 00:00:00+00' + series * interval '1 second',
       timestamptz '2026-01-01 00:00:00+00' + series * interval '1 second'
FROM generate_series(1, :cluster_rows) AS series;

INSERT INTO tool_operations (target_type, target_key, operation_type, payload, status, created_at, updated_at)
SELECT 'cluster',
       'cluster-' || (series % 100),
       'install',
       jsonb_build_object('clusterId', (md5((series % 100)::text)::uuid)::text),
       'completed',
       timestamptz '2026-01-01 00:00:00+00' + series * interval '1 millisecond',
       timestamptz '2026-01-01 00:00:00+00' + series * interval '1 millisecond'
FROM generate_series(1, :operation_rows) AS series;

ANALYZE clusters;
ANALYZE tool_operations;
SQL

docker exec "$CONTAINER" psql -X -U astro -d astronomer -Atc \
  "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT id, created_at FROM clusters WHERE decommissioned_at IS NULL AND status = 'connected' ORDER BY created_at DESC, id DESC LIMIT 101" \
  >"$REPORT_DIR/clusters-${CLUSTER_ROWS}.explain.json"
docker exec "$CONTAINER" psql -X -U astro -d astronomer -Atc \
  "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) SELECT id, created_at FROM tool_operations WHERE payload->>'clusterId' ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' AND (payload->>'clusterId')::uuid = ANY(ARRAY[md5('1')::uuid]) ORDER BY created_at DESC, id DESC LIMIT 101" \
  >"$REPORT_DIR/tool-operations-${OPERATION_ROWS}.explain.json"

grep -q 'clusters_active_status_created_idx' "$REPORT_DIR/clusters-${CLUSTER_ROWS}.explain.json" || {
  echo "query-plan-certification: filtered cluster list did not use clusters_active_status_created_idx" >&2
  sed -n '1,220p' "$REPORT_DIR/clusters-${CLUSTER_ROWS}.explain.json" >&2
  exit 1
}
grep -q 'tool_operations_cluster_created_idx' "$REPORT_DIR/tool-operations-${OPERATION_ROWS}.explain.json" || {
  echo "query-plan-certification: scoped operation list did not use tool_operations_cluster_created_idx" >&2
  sed -n '1,260p' "$REPORT_DIR/tool-operations-${OPERATION_ROWS}.explain.json" >&2
  exit 1
}

docker exec -i "$CONTAINER" psql -X -v ON_ERROR_STOP=1 -U astro -d astronomer >/dev/null <<'SQL'
CREATE TEMP TABLE cursor_events (
  id uuid PRIMARY KEY,
  created_at timestamptz NOT NULL,
  payload text NOT NULL
);
CREATE INDEX cursor_events_created_id_idx ON cursor_events (created_at ASC, id ASC);
INSERT INTO cursor_events
SELECT md5(series::text)::uuid,
       timestamptz '2026-01-01 00:00:00+00' + series * interval '1 millisecond',
       'base-' || series
FROM generate_series(1, 200) AS series;

CREATE TEMP TABLE cursor_seen (id uuid PRIMARY KEY, payload text NOT NULL);
INSERT INTO cursor_seen
SELECT id, payload FROM cursor_events ORDER BY created_at ASC, id ASC LIMIT 50;

DO $$
DECLARE
  cursor_time timestamptz;
  cursor_id uuid;
BEGIN
  SELECT event.created_at, event.id INTO cursor_time, cursor_id
  FROM cursor_events event
  JOIN cursor_seen seen USING (id)
  ORDER BY event.created_at DESC, event.id DESC
  LIMIT 1;

  INSERT INTO cursor_events VALUES
    ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', cursor_time - interval '500 microseconds', 'late-before-cursor'),
    ('bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', cursor_time + interval '500 microseconds', 'late-after-cursor');

  INSERT INTO cursor_seen
  SELECT id, payload
  FROM cursor_events
  WHERE (created_at, id) > (cursor_time, cursor_id)
  ORDER BY created_at ASC, id ASC
  LIMIT 1000;

  IF (SELECT count(*) FROM cursor_seen) <> 201 THEN
    RAISE EXCEPTION 'cursor traversal returned % rows, expected 201', (SELECT count(*) FROM cursor_seen);
  END IF;
  IF EXISTS (SELECT 1 FROM cursor_seen WHERE payload = 'late-before-cursor') THEN
    RAISE EXCEPTION 'row committed behind the cursor violated forward-only consistency';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM cursor_seen WHERE payload = 'late-after-cursor') THEN
    RAISE EXCEPTION 'row committed ahead of the cursor was skipped';
  END IF;
END $$;
SQL

echo "query-plan-certification: intended indexes used and cursor consistency passed"
if [[ "$REPORT_DIR" != "$WORK_DIR/reports" ]]; then
  echo "query-plan-certification: reports retained in $REPORT_DIR"
fi
