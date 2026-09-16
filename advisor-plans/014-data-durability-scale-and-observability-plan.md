# Data durability, scale, and observability plan

**Status:** IN PROGRESS — priority 4 of 5

**Planned at:** `32100314e081e635e89f43fcf14673a25449ef63` on 2026-09-16, against the current dirty working tree

**Branch:** `advisor/014-data-scale-observability`

**Depends on:** Plan 011 green/lifecycle foundation; coordinate error and crypto boundaries with Plan 012

## Implementation progress (2026-09-16)

- The PostgreSQL integration harness now explicitly runs and rejects skips for
  the heartbeat atomic-write and inactive-user retention contracts.
- Cluster-template recovery now uses bounded unique tasks, counts only accepted
  enqueue operations, records failures, and returns queue/list failures for
  retry instead of reporting success.
- Database pool saturation observation is per pool rather than process-global.
- Durable notification outboxes, cursor migration, SQL-side collection paging,
  estate evidence, and end-to-end agent telemetry remain outstanding.

## Objective

Make asynchronous work durable, database access bounded and generated, JSON/deletion rules explicit, and scale/observability claims measurable. Completion requires a recorded estate benchmark, lossless notification/recovery dispatch, stateful integration tests that cannot silently skip, and end-to-end trace/log/metric coverage through adopted-cluster operations.

## Findings covered (global rank 31–40)

| Rank | ID | Finding | Primary evidence |
|---:|---|---|---|
| 31 | DUR-01 | Alert notification enqueue failures are discarded and reconstructed tasks lose queue/retry options | `internal/handler/control_plane.go:530-541`; `internal/worker/tasks/notification_dispatch.go:88-97`; `internal/worker/tasks/alert_evaluation.go:158-219` |
| 32 | DUR-02 | Template recovery ignores enqueue errors and increments success anyway | `internal/worker/tasks/cluster_template_apply.go:583-617,627-647` |
| 33 | TEST-01 | Two PostgreSQL integration contracts are omitted and silently skip | `scripts/test-postgres-integration.sh:42-63`; `internal/db/sqlc/heartbeats_integration_test.go:19`; `internal/db/sqlc/users_inactive_retention_integration_test.go:14` |
| 34 | PAGE-01 | Deep offset pagination permits pathological scans and overflow-scale offsets | `internal/handler/response.go:72-93`; numerous `OFFSET` queries including `clusters.sql:55-93` |
| 35 | PAGE-02 | GitOps, Vault, SCIM and other lists fetch all rows then paginate in handlers | `internal/handler/gitops_sources.go:25-40`; `internal/handler/vault.go:252-267`; `internal/handler/scim_token_admin.go:159-175`; `internal/db/queries/gitops_registration.sql:10-17`; `internal/db/queries/vault_connections.sql:9-15`; `internal/db/queries/scim.sql:19-20` |
| 36 | DATA-02 | JSONB has no domain schema/writer-validation program, while hard deletes remain for durable tenant data | `internal/db/migrations/001_initial.up.sql`; `internal/db/queries/users.sql:169`; `internal/db/queries/projects.sql:132`; `internal/db/queries/rbac.sql` |
| 37 | DB-02 | Critical handwritten SQL lives beside generated sqlc output and is not rejected by the generation check | `task_outbox_ext.sql.go:11-85`; `operation_idempotency_ext.sql.go:10-89`; `scripts/check-sqlc-generated.sh:21-29` |
| 38 | ARCH-01 | Domain/control-plane code couples concrete peer handlers and some internal packages read environment directly | `internal/handler/control_plane.go:48-55,102-112,360-391`; `internal/agent/config.go:126-128,212` |
| 39 | SCALE-01 | No passing estate-100/500/2000 qualification is recorded; the last estate-100 run failed | `docs/scale-baseline.md:7-9,116,122,136`; `docs/assurance/rancher-benchmark/README.md:54-56` |
| 40 | OBS-01 | Agent tracing, local telemetry stack, logging stream policy, and sampling/environment identity are incomplete | `cmd/agent/main.go:29`; `internal/observability/otel_tracing.go:35,58,122,154`; `deploy/docker-compose.yml`; command log setup |

## Out of scope

- Cluster provisioning, Fleet, or a return to retired Argo delivery.
- Replacing PostgreSQL, pgx, sqlc, or Flux.
- Treating the sibling Rancher source tree as a valid product benchmark. Use the paired benchmark protocol and current released product.
- Migrating every offset endpoint in one unsafe flag day. Prioritize fleet-scaled/default UI paths and provide compatibility.
- Deleting historical records until retention, audit naming, and recovery rules are proven.

## Preflight

1. Preserve the dirty tree and capture `git status --short` plus HEAD.
2. Re-run Plan 011's green gate. Stop if it is not green.
3. Run the PostgreSQL integration harness and record which tests execute/skip. Do not accept an overall zero exit if required tests skipped.
4. Inventory every task enqueue site, every `OFFSET` query, every non-generated Go file under `internal/db/sqlc`, every JSONB column, and every destructive `DELETE` query. Check the inventories into test fixtures or generated reports where maintainable.
5. Record the existing scale environment/hardware/commit/config before any benchmark. A number without a reproducible environment is not qualification evidence.

## Implementation steps

### 1. Route alert notifications through one durable outbox contract

- Move rule evaluation and notification creation into a transaction that persists a uniquely keyed outbox row with the complete task payload/options.
- Preserve `MaxRetry`, critical queue selection, idempotency key, tenant/resource identity, and trace/audit correlation. Do not reconstruct a reduced task in the handler.
- Treat enqueue/persist failure as a failed evaluation transition with retry, not success. Surface queue depth, oldest age, dispatch failures, and dead letters.
- Add crash-window tests: before commit, after commit/before dispatch, duplicate evaluator, duplicate worker, transient queue failure, permanent failure, and recovery.

### 2. Make template recovery accounting truthful and durable

- Check every enqueue result. Increment scheduled/success counters only after durable persistence.
- Use a unique recovery-operation key so a retry cannot create duplicate remediation.
- Store per-cluster failure reason and expose partial failure in operation status/audit output.
- Add tests for mixed success, all failure, queue outage, duplicate retry, and process crash.

### 3. Make stateful integration coverage explicit

- Export the required test variables for `TestRecordAgentHeartbeatAtomicWrite` and `TestDeactivateInactiveUsersPostgresSemantics` in `scripts/test-postgres-integration.sh`.
- Add both to the harness's expected/no-hidden-skips list and ensure their packages run.
- Prefer build tags plus testcontainers/isolated PostgreSQL provisioning over bespoke opt-in environment variables in follow-up work.
- Emit JUnit through `gotestsum` for the ordinary and stateful Go lanes; preserve `-race` and `-count=1` for the authoritative gate.

### 4. Introduce opaque/keyset pagination for fleet-scale collections

- Define one versioned opaque cursor contract with stable sort key plus unique tie-breaker. Invalid/expired cursors return a typed 400.
- Add cursor variants for clusters, audit, operations, alerts/events, workloads, users/RBAC, and other default large collections. Keep bounded offset compatibility only where public clients require it.
- Clamp legacy offset and limit before integer conversion; reject negative/overflow/deep values with a stable response rather than allowing pathological scans.
- Add indexes matching each cursor order and test no duplicates/gaps under concurrent inserts/deletes.

### 5. Push filtering/count/pagination into SQL

- Replace load-all-then-slice flows for GitOps sources, Vault connections, SCIM tokens, and any other identified list with `LIMIT`, cursor/offset, filters, and a matching count/has-next query.
- Always apply tenant/project/cluster predicates in SQL, not only after retrieval.
- Select response projections rather than wide secret-bearing rows. Never load encrypted material for list views.
- Add query plans/benchmarks at 2,001 clusters and representative per-tenant row counts.

### 6. Establish JSON and deletion governance

- Create versioned JSON schemas for every externally supplied or durable domain JSONB shape. Validate in service/writer code and add database shape/size constraints where PostgreSQL can express them safely.
- Preserve forward compatibility deliberately (`additionalProperties` policy per schema) and test migrations between versions.
- Classify each hard delete: ephemeral, explicitly purgeable after retention, or durable/audit-referenced. Convert durable user/project/RBAC data to tombstones plus retention jobs where audit/history depends on identity.
- Apply tenant scoping in every store query and add cross-tenant negative tests. Backfill denormalized audit identity before any purge, consistent with Plan 005.

### 7. Restore sqlc as the SQL source of truth

- Move handwritten statements from `internal/db/sqlc/*_ext.sql.go` into query files and regenerate through sqlc, or document a narrowly isolated exception outside the generated directory.
- Make the generation check build in a clean temporary tree and fail on extra `.go` files lacking the sqlc generated header, missing outputs, or diffs.
- Add query tests for leases/CAS/idempotency/outbox semantics before migration.

### 8. Enforce handler/service/store and composition-root boundaries

- Extract interfaces for alerting, notification dispatch, operation/idempotency, and other control-plane collaborations. Handlers parse/auth/respond; services own domain transactions; stores own SQL.
- Replace concrete peer-handler fields in `ControlPlane` incrementally, one bounded domain at a time.
- Move direct environment reads to `cmd/*` composition/config parsing. Pass validated typed config into `internal/*` packages.
- Add dependency-boundary tests (or a static import rule) preventing handler-to-handler construction and new `os.Getenv` outside approved composition/config packages.

### 9. Produce passing scale and Rancher-comparison evidence

- Repair the known pool/reconnect/audit/worker omissions in the scale harness only through measured, reviewed changes.
- Run estate-100 first, then 500 and 2,000. Record hardware, topology, replicas, DB/Redis config, seed, commit, duration, workload mix, p50/p95/p99, error rate, reconnect time, audit lag, queue lag, CPU/memory, pool saturation, and top `pg_stat_statements`.
- Define pass/fail thresholds before each run. Retain raw artifacts and record failures honestly.
- After scale passes, execute the paired Rancher operator benchmark and human study in `docs/assurance/rancher-benchmark`. Do not claim equal-or-better until its automated and human gates pass.

### 10. Complete the operational telemetry path

- Initialize OTel in the adopted-cluster agent and propagate W3C context across tunnel envelopes; span Kubernetes/Helm/exec/log operations without recording secret or resource payloads.
- Add `deployment.environment` and other standard resource identity. Distinguish unset sampling from explicit zero; zero must export no sampled traces.
- Send structured JSON logs to stderr consistently across server, worker, agent, and migrator. Retain the documented `slog` choice unless an ADR selects another logger.
- Add an opt-in local Prometheus/Tempo/Loki/Grafana profile with bounded resources and documented startup, while keeping production external-service friendly.
- Add end-to-end trace contract tests and dashboards/alerts for lifecycle, outbox, audit drain, revocation, tunnel health, and scale SLOs.

## Verification

```bash
go test -race ./internal/... ./cmd/... -count=1
scripts/test-postgres-integration.sh
scripts/check-sqlc-generated.sh
go vet ./internal/... ./cmd/...
go test ./... -count=1
cd frontend && npm run type-check && npm run lint && npm test
```

Additionally:

- run `EXPLAIN (ANALYZE, BUFFERS)` against anonymized representative data for migrated lists;
- run an outbox crash/retry test with PostgreSQL and the real queue backend;
- bring up the local telemetry profile and prove one HTTP request correlates through worker, tunnel, and agent;
- complete and attach at least estate-100 passing evidence before marking this plan done. Higher scale rungs and the human parity study may remain separate release gates only if status is explicit.

## Commit sequence

1. `fix(alerting): persist complete notification tasks`
2. `fix(templates): report recovery enqueue failures`
3. `test(db): run all postgres integration contracts`
4. `feat(api): add bounded cursor pagination`
5. `perf(db): page filtered collections in sql`
6. `feat(data): validate json and retain durable identities`
7. `refactor(db): generate critical queries with sqlc`
8. `refactor(core): enforce service and store boundaries`
9. `test(scale): record reproducible estate qualification`
10. `feat(observability): trace adopted-cluster operations`

## Done criteria

- Notification and recovery work survives crashes and never reports enqueue failure as success.
- All required PostgreSQL integration tests demonstrably execute in CI.
- Default fleet-scale collections have bounded/keyset access; deep offsets cannot reach the database.
- List queries filter, scope, and paginate in SQL without loading secret material.
- Every durable JSONB writer validates a versioned schema, and destructive retention has explicit policy.
- sqlc generation detects handwritten/extra generated-directory drift.
- Domain services do not depend on concrete sibling handlers or ambient environment reads.
- A reproducible estate-100 run passes and its raw evidence is retained; higher claims match completed rungs.
- A trace can be correlated from API through worker/tunnel to agent, and explicit zero sampling is honored.
- No claim of Rancher parity appears before the paired benchmark and human study pass.

## Stop conditions

- Stop before purging or hard-deleting historical rows until audit identity backfill and recovery are proven.
- Stop if cursor rollout would silently change a public client contract; add a versioned compatibility endpoint.
- Stop a scale run when correctness/error thresholds fail; preserve evidence and diagnose rather than averaging failures away.
