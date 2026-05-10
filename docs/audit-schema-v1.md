# Audit Schema `audit-v1`

`audit-v1` is the target contract for compliance-oriented audit events emitted
by the Astronomer control plane. The backing table is the partitioned
Postgres table `audit_log`, partitioned monthly by `created_at`.

Fresh installs create `audit_log` directly in migration `001_initial`.

Legacy `audit_logs` history is backfilled into `audit_log` by migration `026`.
Imported rows are tagged `actor_auth_method = legacy_backfill` so operators can
distinguish them from native v1 writes and the migration stays reversible.

New control-plane audit writes now target `audit_log` directly. This document
defines the active schema contract for those writes and for downstream readers.
Migration `027` removes the legacy `audit_logs` table from the runtime schema.

## Goals

- append-only control-plane audit stream
- monthly partitions for retention and export performance
- stable contract for future federation / forwarding
- explicit actor/request metadata
- room for redacted before/after payloads in `detail`

## Table

Primary key:

- `id`
- `created_at`

Core columns:

- `created_at TIMESTAMPTZ`
- `schema_version VARCHAR(32)` default `audit-v1`
- `source VARCHAR(16)` default `service`
- `correlation_id VARCHAR(64)` default `''`
- `user_id UUID NULL`
- `actor_auth_method VARCHAR(32)`
- `action VARCHAR(64)`
- `resource_type VARCHAR(64)`
- `resource_id VARCHAR(255)`
- `resource_name VARCHAR(255)`
- `http_method VARCHAR(16)`
- `path TEXT`
- `status_code INTEGER`
- `duration_ms BIGINT`
- `request_id VARCHAR(64)`
- `ip_address INET NULL`
- `user_agent TEXT`
- `detail JSONB`

## Semantics

- `schema_version` is always `audit-v1` for rows in this contract.
- `user_id` may be null for anonymous or pre-auth actions.
- `source` distinguishes router-emitted HTTP audit rows (`http`) from
  application/service-level writes (`service`).
- `correlation_id` is the stable cursor and cross-system trace key for an
  audit event chain. HTTP handlers populate it from `X-Correlation-Id` when
  present, otherwise `X-Request-ID`, otherwise a generated UUID.
- `actor_auth_method` is values like `jwt`, `api_token`, or empty when unknown.
- `action` is a stable verb-like identifier such as `request.post`,
  `auth.login_failed`, or `project.add_namespace`.
- `resource_type` and `resource_id` identify the primary object affected.
- `detail` is a redacted JSON payload for request metadata, before/after state,
  or subsystem-specific context.
- service-layer events may embed `before`, `after`, and `tags` inside
  `detail` so a single correlation chain can carry both HTTP envelope metadata
  and domain-specific state transitions.

Action naming contract:

- use dotted, verb-oriented identifiers
- keep existing identifiers stable once shipped
- adding fields is compatible within `audit-v1`
- changing field meaning or removing fields requires a new schema contract
  (`audit-v2`)

## Redaction

The following classes of values must not appear in cleartext in `detail`:

- passwords
- API tokens
- OAuth client secrets
- kubeconfigs
- private keys
- cloud secret access keys

Redaction is writer-side responsibility.

## Partitioning

- table name: `audit_log`
- partitioning: `RANGE (created_at)`
- cadence: monthly partitions
- safety net: default partition `audit_log_default`

The migration creates the current and next month partitions plus the default
partition. A future background task can call `create_audit_log_partition()` to
materialize additional months ahead of time.

## Retention

Retention is not enforced by the current migration. Operational policy should
be implemented as:

1. create the next month partition ahead of time
2. archive or export old partitions if required
3. drop partitions older than the chosen retention window

Current worker behavior:

- `audit_log:ensure_partitions` creates the current and next month partitions
- `audit_log:enforce_retention` drops monthly partitions older than
  `AUDIT_LOG_RETENTION_MONTHS`
- default retention is `13` months when the env var is unset or invalid

## Read API

- `GET /api/v1/audit?limit=N&offset=M` returns the default reverse-chronological
  view
- `GET /api/v1/audit?since=<audit_id>&limit=N` returns rows strictly after the
  referenced row in ascending `(created_at, id)` order for cursor-style export
  and replication
- `GET /api/v1/audit/export?format=csv` exports the offset-based view as CSV
