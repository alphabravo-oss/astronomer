# Control-plane state and ownership contract

Status: normative
Release line: 1.1.x
Last reviewed: 2026-08-23

This document defines which system owns Astronomer state, how that state moves
to adopted clusters, and what must happen during conflicts and failures. The
[Flux-native delivery decision](architecture/decisions/flux-native-delivery.md)
governs implementation details; the generated
[compatibility matrix](architecture/compatibility.md) governs supported
versions and bounds.

## Product boundary

Astronomer manages clusters after they exist. It does not own infrastructure,
machines, cloud node pools, or cluster provisioning. Rancher Fleet is not a
dependency or compatibility target. Delivery uses the exact Flux distribution
declared by `deploy/release/compatibility.yaml` and installed in each adopted
cluster.

## Authoritative owners

| State class | Authoritative owner | Replicas and caches | Required behavior |
| --- | --- | --- | --- |
| Identity, sessions, tokens, RBAC, projects, cluster inventory | PostgreSQL | Redis authorization/revocation caches; CRD status | Database commits define product truth. Cache loss must not lose authorization or intent. |
| Delivery sources, encrypted credentials, bundle versions, targets, placement snapshots, rollouts, deployments, assignment generations | PostgreSQL | Worker queue and cluster-local Flux objects | Every external side effect originates from durable committed intent and an idempotent operation/outbox record. |
| Audit history and mutation intent | PostgreSQL plus transactional outbox | SIEM destinations | A protected mutation fails closed when its required audit intent cannot commit. External delivery retries independently. |
| Live Kubernetes resources | The adopted cluster's Kubernetes API | Bounded inventory/status in PostgreSQL and browser caches | Astronomer observes or mutates through authenticated agent/proxy paths. Cached state never overrides live resource truth. |
| Downstream delivery convergence | Cluster-local Flux CRDs and controllers | Normalized delivery status in PostgreSQL | Flux owns controller status and managed fields. Astronomer supplies desired objects and observes conditions; it does not forge controller status. |
| Background execution | PostgreSQL intent/outbox; Redis/asynq transport | Worker leases and metrics | Queue loss or worker restart cannot erase committed work. Replay is idempotent and bounded. |
| Secret material | Encrypted PostgreSQL column, hashed verifier, external reference, or Kubernetes Secret required by its consumer | Redacted response views only | Clear values never enter task payloads, audit details, support bundles, metrics, or API responses. |
| Optional management CRD intent | Kubernetes CRD spec, mirrored with ownership metadata in PostgreSQL | CRD status and product views | REST/UI cannot silently overwrite CRD-owned fields. Ownership transfer is explicit, authorized, and audited. |

## Delivery state machine

```text
source revision -> immutable bundle version -> target + frozen placement
      -> approved rollout -> per-cluster deployment -> assignment generation
      -> agent acceptance -> local Flux reconciliation -> normalized status
```

The following invariants are mandatory:

1. Source credentials are write-only and encrypted or externally referenced.
2. A bundle version records an immutable commit, digest, or version plus
   checksum; a mutable tag alone is not a deployable identity.
3. Placement is resolved centrally and frozen for an approved rollout.
   Subsequent label changes do not silently rewrite the cohort.
4. Each cluster receives monotonically increasing assignment generations.
   Duplicate delivery is safe; stale generations are rejected.
5. The agent accepts only assignments bound to its authenticated cluster and
   supported protocol/capability range.
6. The agent materializes only the permitted Flux object set in
   Astronomer-owned namespaces and reports bounded, secret-free status.
7. Pauses, approvals, retries, supersession, rollback, and cancellation are
   durable operations, not browser-only state.
8. The last accepted generation continues reconciling locally during a
   management-plane outage.

## Kubernetes write ownership

Ordinary Kubernetes edits use server-side apply with an Astronomer field
manager and `force=false`. The UI must perform strict dry-run and show a diff
before save. A field-ownership conflict is a safe stop.

Taking ownership requires a separate explicit action, stronger management
permission, `force=true`, and an audit record naming the resource and field
manager without recording secret contents. Force does not bypass Kubernetes
authorization, admission, policy, or namespace scope.

Astronomer-created resources carry stable ownership metadata. Deletion and
decommission reconcilers may prune only objects whose identity and ownership
proof match the durable operation. Unowned objects are surfaced as drift and
left untouched.

## CRD and REST conflict rules

When a `Cluster`, `Project`, or `AgentProfile` CRD owns declarative fields, the
PostgreSQL row records the external API version, kind, namespace, name,
generation, and `managed_by=crd`.

- REST/UI reads remain available.
- A REST/UI write to a CRD-owned field returns conflict.
- Non-owned operational fields may be updated when their endpoint explicitly
  permits it.
- Ownership takeover is a dedicated API operation with authorization, audit,
  and a resulting ownership record.
- Reconciliation writes status and `observedGeneration`; it never writes
  generated status back into spec.
- Cross-namespace references and ambiguous same-name adoption are refused.

## Connectivity and proxy state

An adopted cluster authenticates with its own hashed/revocable agent identity.
Tunnel ownership is leased so only one server replica routes a session at a
time. Reconnect replaces the previous generation and cannot inherit another
cluster's work.

User proxy requests are authorized at global, project, cluster, namespace,
resource, and verb scope before forwarding. The agent independently constrains
the request to its privilege profile. Impersonation identity and correlation
metadata are preserved where supported. Streaming responses remain bounded by
timeouts, frame limits, and cleanup on disconnect.

Generated kubeconfigs contain a short-lived, revocable, read-only Astronomer
credential and an Astronomer proxy endpoint. They do not disclose a direct
cluster endpoint or long-lived cluster credential.

## Failure and replay semantics

| Failure | Required outcome |
| --- | --- |
| API process exits after DB commit but before queue publish | The transactional outbox republishes the committed operation. |
| Worker exits during execution | The lease expires; a bounded idempotent retry continues or records a terminal failure. |
| Redis is unavailable | New durable intent may commit to the outbox; operators see backlog age; recovery republishes without losing intent. |
| PostgreSQL is unavailable | Mutations fail safely before external side effects. Existing cluster-local Flux state continues. |
| Agent disconnects | No central write is treated as applied. The assignment remains pending/stale and resumes on authenticated reconnect. |
| Status event is duplicated or reordered | Cluster, assignment generation, event identity, and compare-and-swap checks make ingestion idempotent. |
| Tunnel owner disappears | Lease transfer routes the next authenticated session; no operation may double-apply outside its idempotency contract. |
| Audit/SIEM destination is unavailable | Required audit intent remains durable; external forwarding retries and exposes backlog/DLQ state. |
| A controller reports an unknown future schema | Admission rejects it and records a compatibility reason; it is never guessed compatible. |

## Backup, restore, and repair

A recoverable management-plane backup contains PostgreSQL, encryption/signing
key custody references, release/compatibility identity, and externally stored
artifacts required by the configured topology. Redis is not the source of
truth and is rebuilt from PostgreSQL/outbox state.

After restore:

1. Run schema/version preflight before serving mutations.
2. Restore keys before reading encrypted columns; a lost encryption key is not
   recoverable from ciphertext.
3. Replay committed outbox work with its original idempotency keys.
4. Reconcile CRD ownership by stable external reference, never by name alone.
5. Let authenticated agents reconnect and report their accepted generations.
6. Compare local Flux inventory/status to durable deployments and surface
   orphan, stale, and drift conditions.
7. Complete a recorded restore drill before declaring the plane recovered.

## Public API contract

PostgreSQL tables, Redis payloads, Flux CRDs, and agent frames are internal
implementation contracts. The supported automation surface is the documented
Astronomer REST API, generated clients, CLI workflows, and the explicitly
documented management CRDs.

A public mutation is complete only when it has:

- an OpenAPI operation with typed request/success/error contracts;
- authentication, API-token scope, RBAC, and object-ownership policy;
- request/response/concurrency/time bounds;
- idempotency and audit semantics;
- a generated client operation and permission-aware UI consumer when exposed
  in the console;
- upgrade/deprecation behavior when its durable shape changes.

## Change review requirements

Changes to this contract require the matching code, tests, and documentation in
the same change. In particular:

- delivery state changes update migrations, assignment/status protocol tests,
  the Flux ADR, and failure-recovery tests;
- CRD changes update schemas, generated deepcopy/code, ownership tests, and
  `docs/crd-api.md`;
- compatibility changes update `deploy/release/compatibility.yaml` and
  regenerate `docs/architecture/compatibility.md`;
- proxy or credential changes update the threat model, authorization negative
  tests, audit policy, and proxy inventory;
- backup topology changes update restore automation and runbooks, then produce
  a new drill artifact.
