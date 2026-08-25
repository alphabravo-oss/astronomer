# Audit Schema `audit-v1`

`audit-v1` is the target contract for compliance-oriented audit events emitted
by the Astronomer control plane. The backing table is the partitioned
Postgres table `audit_log`, partitioned monthly by `created_at`.

Fresh installs create `audit_log` directly in migration `001_initial`.

Legacy `audit_logs` history is backfilled into `audit_log` by migration `026`.
Imported rows are tagged `actor_auth_method = legacy_backfill` so operators can
distinguish them from native v1 writes and the migration stays reversible.

Canonical control-plane audit events target `audit_log`. High-risk domain
mutations that have been migrated to the transactional path first write the
same sanitized envelope to `audit_outbox` in the domain transaction; a durable
dispatcher then copies it into `audit_log` idempotently. This document defines
the active schema and delivery contract for writers and downstream readers.
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

## Transactional delivery

Migration `015_transactional_audit_outbox` adds the durable intent boundary.
For a migrated high-risk mutation:

1. the domain row and sanitized `audit_outbox` intent commit in the same
   PostgreSQL transaction;
2. `audit:outbox_dispatch` claims due intents with a bounded lease and
   `FOR UPDATE SKIP LOCKED`;
3. one PostgreSQL statement inserts the stable `(id, event_created_at)` into
   partitioned `audit_log`, inserts a stable-UUID queue receipt for every
   enabled SIEM forwarder whose filter matches, and marks the intent delivered;
4. replay after a process crash uses the canonical audit key and the partial
   `(forwarder_id, dedupe_key)` queue index, so neither local nor destination
   evidence is duplicated;
5. failed delivery retries with bounded backoff and becomes operator-visible
   `dead` after the configured attempt budget.

`dedupe_key` identifies one mutation/request/action/resource intent without
including secrets or request bodies. `detail` is sanitized before insertion and
is constrained to 64 KiB. Migration 017 makes matching SIEM fan-out part of the
audit delivery transaction and uses `audit:<event UUID>` as its destination
dedupe key. These mandatory receipts are exempt from queue-cap eviction, retry
exhaustion deletion, and age retention; an unavailable external archive can
raise storage/queue alerts but cannot silently discard transactional audit
evidence. The support bundle exposes lifecycle counts and timestamps only,
never outbox event content.

Transactional coverage is intentionally tracked per mutation family. The
presence of the outbox does not imply that an un-migrated handler's legacy
service audit call is atomic with its domain write.

The currently migrated families are project namespace membership; security
templates, policies, scan creation, and cancellation; global, cluster, and
project RBAC roles and bindings; administrative user lifecycle, password, and
session invalidation; self-service password changes, API-token lifecycle,
logout revocation, and race-safe failed-login lockout transitions; cluster
create/update/ownership/decommission, registration-token, and agent-token
operations; legacy and multi-registry configuration; Dex connector/settings
and SSO stage transitions; SMTP settings; SIEM forwarder and synthetic-test
intents; webhook subscription/test/retry intents; Vault connection/default and
health-result persistence; and encrypted cloud-credential lifecycle.
Backup storage, on-demand backups, schedules, manual triggers, and restore
operations are migrated as well; remote Velero application happens after the
database commit and converges from the persisted desired-state row. New backup
credentials fail closed without encryption and never populate the legacy
plaintext access-key columns.
The per-cluster workload snapshot surface follows the same boundary: snapshot,
restore, and schedule state share a transaction with mandatory audit; create,
restore, and delete additionally persist a tunnel-owned task intent. The HTTP
path never writes a Velero CR before commit, task payloads omit snapshot specs,
and deterministic external names make replay safe. On-demand control-plane
snapshot triggers likewise commit the pending registry row, privileged-Job
task intent, and audit together; the worker stores a sanitized retry outcome
instead of upstream response bodies.
Administrative retry of a durable task-outbox row uses the same mandatory
boundary. The handler locks the row, rejects an already delivered task, resets
its attempt budget and stale delivery lease, and commits that transition with
the audit intent. Audit detail contains only the previous status, task type,
and queue name; it excludes the task payload and prior dispatcher error.
API-server allow-list updates commit their desired-state row, identifier-only
reconcile task, and CIDR-redacted audit intent together; an explicit reconcile
request commits its task and audit while holding the policy-row lock. Cloud
provider reads and writes occur only in the worker after commit. Compliance
baseline apply and revert likewise lock the active application decision and
commit settings, quotas, application history, and audit together. Their audit
detail records note presence and length, never operator-authored note content.
Catalog repository CRUD, project-owned repository plus auto-subscription,
subscription deletion, and installed-chart install/upgrade/rollback/uninstall
staging are also migrated. Installed-chart desired state, its durable catalog
operation, and audit intent share a transaction. Manual repository sync now
commits a repository-ID-only `catalog:sync` task-outbox row with the request
audit intent and returns `202`; the worker performs the bounded network fetch
after commit. Targeted sync tasks bypass periodic leader deduplication so a
non-leader consumer cannot acknowledge unique operator work without executing
it. New repository credentials fail closed without encryption, and repository
connection-test results require synchronous audit persistence before return.
Repository URLs cannot carry userinfo, query parameters, or fragments; secrets
must use the encrypted `auth_config` field. URL values are omitted from catalog
audit detail in favor of stable repository IDs.
Logging output and pipeline create/update/enable/disable/delete operations are
also migrated: desired configuration, the durable `logging_operations` intent,
and the audit-outbox intent commit together. Apply tests and operation retries
use the same boundary. Durable idempotency replays are rejected before commit,
so a restarted client cannot stage configuration behind an old operation.
Caller-owned logging saved-search create/update/delete operations use that
boundary too. Audit detail contains a SHA-256 query digest and bounded search
metadata, never the saved query text.
Hosted-Loki token rotation and one-click attach include the encrypted token,
system output, apply operation, and audit intent in that transaction; plaintext
is returned only after commit. Reconciler wakeups and SSE notifications occur
after commit, and logging mutation routes require a write-scoped API token.
Tool install, upgrade, uninstall, adopt, and operation retry now commit the
durable `tool_operations` row with audit intent before notifying the
reconciler. Reusing an idempotency key for a different target, operation, or
payload is rejected, and tool mutation routes require a write-scoped API token.
Workload scale, restart, delete, and operation retry use the same operation plus
audit transaction boundary. The local idempotency cache absorbs fast retries;
the PostgreSQL operation ledger supplies the cross-replica/restart guarantee
and rejects cross-operation key aliasing.
Monitoring backend and per-cluster configuration updates, shared
Thanos/Alertmanager/Grafana/Loki lifecycle operations, per-cluster stack
lifecycle operations, and operation retries use the same boundary. Desired
metadata, the durable monitoring operation, and mandatory audit intent commit
or roll back together. Reconciler wakeups and Loki-dependent external cleanup
run only after commit. Monitoring idempotency keys cannot alias a different
target, operation type, or payload, and replace persists the same resolved
release target that its operation will reconcile.
Alerting notification-channel, rule, event, silence, and inhibition mutations
also use the transaction boundary. Rule-to-channel association changes commit
with their parent rule and audit intent. Test notifications commit a
`notification:send` task-outbox row with the audit intent, so a Redis outage
cannot turn an acknowledged test into lost work; external notification
delivery remains an asynchronous worker effect after commit.
Cluster-group create/update/delete and bulk membership moves are migrated as
well. Subtree deletion snapshots affected clusters, deletes the group tree,
and writes the group plus per-cluster audit intents in one transaction. Bulk
moves likewise commit every successful membership change with its matching
audit intent, rather than allowing partially audited membership state.
Cluster-template CRUD and per-cluster apply/reapply/detach are migrated. An
apply or reapply commits the application desired state, its unique durable
`cluster_template:apply` task intent, and the audit intent together. Detach
commits both the binding and stamped registration-policy cleanup with its audit
intent, so the API cannot report a partially detached cluster.
Network-policy template CRUD and application lifecycle are migrated too. Bulk
application is atomic across validated namespaces, resets existing bindings to
pending idempotently, and commits one unique durable apply task per binding
with the request audit intent. Reapply follows the same task boundary. Remote
revocation happens first; its observed success or failure is then persisted
with matching audit evidence, and the periodic reconciler remains the repair
layer if the database decision cannot commit after a successful remote delete.
Dashboard-widget and Prometheus-datasource create/update/delete operations are
migrated as well. Delete prerequisite reads and datasource PUT preservation of
existing encrypted auth execute through the transaction-bound query set, so
state and audit intent cannot split. Datasource PUT now persists its documented
name field; omitted auth fields retain the prior ciphertext. Audit detail
records only the endpoint origin plus non-secret configuration flags: URL
paths, tenant selectors, query parameters, and sealed credentials are never
copied into the audit envelope, and credential-bearing URLs are rejected.
GitOps registration-source CRUD and manual/webhook sync requests use the same
boundary. A sync request commits a source-ID-only `gitops:sync` task-outbox row
with its audit intent and returns `202` plus the durable task ID; clone, fetch,
and reconciliation run in the worker after commit. Credentialed writes fail
closed without Fernet, repository URLs cannot embed credentials/query values,
and audit detail omits the repository location. The worker fails loudly when a
Fernet-shaped value cannot be decrypted while retaining the bounded legacy
plaintext compatibility path for pre-encryption rows.
Platform-setting single updates, atomic batch updates, and resets also commit
with their audit intent. The local feature/settings cache is invalidated only
after commit. Charlie's runtime lifecycle remains an external saga: runtime is
started or quiesced before the setting decision and the transition is
compensated if the setting/audit transaction fails. Setting audit detail is
content-free apart from the stable key list and change/presence metadata; raw
configuration values are never copied into the audit envelope.
Read-audit-policy create/update/delete operations use the same boundary,
including their prerequisite reads. The policy evaluator's process-local cache
is invalidated only after the policy and its mandatory audit intent commit.
Identity group-mapping create/delete and administrative user resync use the
same boundary. Resync reconciles all connector-scoped role bindings, writes one
audit intent per binding change plus an aggregate result, and invalidates the
user's RBAC cache only after the entire multi-audit transaction commits.
Notification-template override and reset operations also commit their database
change with sanitized audit evidence; candidate template preview remains a
read-only, best-effort audited action and never persists its supplied content.
Quota-plan create/update/delete operations are migrated as well. Update and
delete lock the named plan row before checking existence or project/user
references, so the authorization-visible decision, mutation, and audit intent
cannot be split by a concurrent administrator.
Maintenance-window create/update/delete, maintenance-gate deferred-operation
enqueue, and deferred-operation cancellation also commit with mandatory audit
intent. A defer request is accepted only after its encrypted, idempotent replay
record and audit intent commit; audit failure rolls the record back and safely
returns the blocked response. Window update/delete and cancellation lock the
target row before making their state-dependent decision, preventing a
concurrent controller or administrator from invalidating the audited result.
The evaluator cache is invalidated only after a window transaction commits;
audit failure leaves both the stored state and the currently valid cache
unchanged.
Control-plane policy updates, alert acknowledgement, and silence create/delete
operations use the same mandatory boundary. Silence deletion returns the
deleted row, so a nonexistent identifier is a real `404` rather than an
unaudited successful no-op. Operator-supplied silence reasons remain domain
state only; audit evidence identifies the controller, condition, and bounded
duration without copying the potentially sensitive reason text.
Native Kubernetes/CRD RBAC grant creation and deletion are transactional as
well. Delete locks and re-reads the target grant inside the transaction; the
per-user native authorization cache is invalidated only after the rule and
mandatory audit intent commit, so audit failure cannot leave an effective but
unaudited permission change.
Agent-upgrade requests commit their idempotent lifecycle-operation row and
mandatory audit intent together. The cluster-agent change event is published
only after commit, and audit detail retains version/strategy metadata without
copying the private agent image or registry location.
Cluster-registration options, confirmation, failed-step retry, and cancellation
execute phase/step state and mandatory audit through one transaction-scoped
registration service. Retry locks the failed timeline step before validating
it. The service buffers SSE and registration metrics until commit, and step
write failures are propagated, so rolled-back transitions cannot leak a false
wizard phase, timeline event, or metric.
The Flux delivery family is also migrated: encrypted sources and rotation,
source verification, immutable bundles and versions, targets, rollout planning
and controls, approvals, cluster deployment controls, and privileged system
rollouts. The rollout/deployment controllers persist the intent inside their
own serializable transaction rather than pretending an unrelated outer
connection supplies atomicity. Multi-registry, SIEM/webhook synthetic
operations, cloud-credential materialization, catalog sync requests,
bundle-version resolution, and rollout scheduling place their reconciliation
task intent in `task_outbox` in the same transaction. Sensitive results such as login/refresh credentials,
kubeconfigs, and provider/configuration test responses require synchronous
audit persistence before response bytes are written.

External effects such as severing an already-authenticated agent tunnel,
calling an identity or secret provider, delivering a webhook/SIEM record, or
applying a Secret to a member cluster occur outside PostgreSQL. Their durable
intent or final observed result is persisted where applicable, but the remote
system effect is not falsely described as part of the database atomicity
guarantee.

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
- the same nightly task deletes only `delivered` audit-outbox receipts older
  than 30 days; unresolved `pending`, `delivering`, `failed`, and `dead`
  intents are never retention-pruned

Operational metrics and alerts:

- `astronomer_audit_outbox_rows{status=...}`
- `astronomer_audit_outbox_oldest_seconds{status=...}`
- `AstronomerAuditOutboxStalled` and `AstronomerAuditOutboxDeadRows`, linked to
  `docs/runbooks/audit-outbox-stalled.md`

## Read API

- `GET /api/v1/audit?limit=N&offset=M` returns the default reverse-chronological
  view
- `GET /api/v1/audit?since=<audit_id>&limit=N` returns rows strictly after the
  referenced row in ascending `(created_at, id)` order for cursor-style export
  and replication
- `GET /api/v1/audit/export?format=csv` exports the offset-based view as CSV
