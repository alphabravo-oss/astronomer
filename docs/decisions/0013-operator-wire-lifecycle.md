# 0013 — Operator split into wire (composition) and lifecycle (reconcile engine)

- Status: accepted
- Date: 2026-10-20
- Guide: Technology Selection Guide, operator placement (`internal/operator/{wire,lifecycle}`, controller-runtime)

## Context

The management CRD controller (Cluster, Project, AgentProfile) lived in
`internal/crd` next to the agent mirror ingest, with manager construction,
scheme registration and three monolithic `Reconcile` functions in one file.
Nothing serialized reconciles of one object beyond the controller-runtime
workqueue (one controller, one worker), there was no per-step visibility, and
a failed sync patched `Ready=False` and returned `nil`, so it was not retried
until some unrelated event arrived.

## Decision

- `internal/operator/wire` is the composition root: `wire.New(Config)` builds
  the scheme (client-go + `management.astronomer.io`), the manager (namespaced
  informer cache, leases leader election, metrics/probes off) and registers the
  controllers listed in `Config.Controllers` (empty = all) with
  `MaxConcurrentReconciles` (default 4).
- `internal/operator/lifecycle` is the reconcile engine and the CRD Go types:
  - `KeyedLock`: per-object lock keyed by `namespace/name`; same key
    serializes, different keys run concurrently; waiting honours the context;
    the unlock is deferred so it is released on return, panic and
    cancellation; idle keys are dropped.
  - `Pipeline[S]`: ordered, named stages registered once at construction
    (names unique, validated). Each stage's outcome (`continue`, `done`,
    `error`, `panic`) is logged, counted in
    `astronomer_operator_stage_total{controller,stage,outcome}`, timed in
    `astronomer_operator_stage_duration_seconds`, and reported to an optional
    `Observer`. A stage error (or recovered panic) stops the chain, records the
    stage's `FailureReason` on the `Ready` condition, and is returned so
    controller-runtime requeues with backoff.
  - Cluster: fetch → finalize → ensure-finalizer → resolve-spec
    (InvalidAgentProfile) → validate-ownership (OwnershipConflict) → sync
    (SyncFailed) → record-ownership (OwnershipWriteFailed) → publish-status.
    Project: the same chain without the AgentProfile failure reason.
    AgentProfile: fetch → finalize → ensure-finalizer → publish-status.
- The agent mirror ingest (`ingest_v2`, `mirror_router`, `trivy_watcher`) is
  not reconcile logic: it moved to `internal/operator/mirror` (router renamed
  `mirror.Router`, querier `mirror.Querier`).
- The DB-backed `ClusterSync`/`ProjectSync` adapters stay in the server
  composition (`internal/server/operator_wiring.go`). `internal/crd` is
  deleted; no aliases.

## Consequences

- Behaviour change, intentional: a failing stage now returns its error after
  recording the condition, so it is retried with backoff instead of waiting for
  the next event.
- `internal/operator/{wire,lifecycle,mirror}` are under the complexity linters
  (not in the legacy exemption list).
- Proof: `internal/operator/wire/wire_integration_test.go` runs the composed
  operator against envtest (kube-apiserver 1.37 + etcd, CRDs rendered from the
  chart) and asserts that two concurrent reconciles of one Cluster never
  overlap in the instrumented sync stage while two Clusters do.
  `make envtest` fetches the binaries; `make test-integration` sets
  `KUBEBUILDER_ASSETS` itself.
- Known, unchanged: every status patch (fresh `lastReconciled`) produces a
  watch event that re-enqueues the object; adding a generation/annotation
  predicate is a separate change.
