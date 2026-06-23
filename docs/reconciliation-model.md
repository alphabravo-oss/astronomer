# Day-2 Reconciliation Model

_For platform teams operating Astronomer._

Astronomer is a **day-2 operations** platform: it adopts and operates clusters
that already exist. It does not provision infrastructure. Everything Astronomer
manages on those clusters — monitoring stacks, network policies, project
enforcement, backups, allow-lists, catalog tools — is kept converged by a
**hybrid reconciliation model**, not by Kubernetes controllers.

This document explains that model so on-call and platform engineers can reason
about *when* a change takes effect, *how* drift is caught, and *why* it behaves
differently from Rancher's controller-everywhere design.

---

## The three layers

Desired state and convergence are split across three layers. No single layer is
authoritative for everything; each owns one job.

| Layer | Technology | Role |
|---|---|---|
| **Durable intent** | Postgres | Source of truth for *what should be true*. Every managed object has a row (e.g. `cluster_monitoring_configs`, `network_policy_templates`, `project_namespaces`, `apiserver_allowlist`). The row carries the desired spec, a `status` column, and convergence bookkeeping (last applied, last reconciled, attempt count, backoff). |
| **Transient convergence** | asynq (Redis-backed task queue) | Stateless workers that read intent rows, apply them to the target cluster, and stamp the result back. Sweeps are periodic (cron via the asynq scheduler) and event-triggered (enqueued on the mutating API call). Workers hold no long-lived lease and no in-memory desired state. |
| **Declarative surface** | Kubernetes CRDs | A GitOps-friendly *intent inlet*. A `Cluster` CRD (and peers) lets operators express intent declaratively; a controller-runtime adapter upserts the Postgres row from the CRD spec (`internal/server/crd_wiring.go`). The CRD is a front door to layer 1, not a parallel reconcile loop. |

The key consequence: **intent is durable and audited in Postgres; convergence is
a stateless, retryable, periodic sweep over that intent.** This is what gives
Astronomer history, audit trails, and per-row backoff that a pure controller
model does not naturally produce.

---

## How convergence runs: enqueue + sweep

Each managed subsystem follows the same two-path pattern:

1. **Event path (fast).** The mutating API handler (or an agent-connect event)
   enqueues the per-row reconcile task immediately, so an operator's click
   reaches steady state inside one screen refresh.
2. **Sweep path (safety net).** A periodic cron entry re-walks every row in a
   non-converged state (`pending`, `failed`, `drifting`, or `running` left
   stranded by a crashed worker). This covers missed enqueues, worker restarts
   mid-apply, and out-of-band deletion of the in-cluster object.

Applies are written with **Server-Side Apply**, so re-running a converged row is
a near-no-op (no wire write when live state already matches). That is why the
sweep can afford to walk every row on every tick.

All periodic tasks are registered in `internal/worker/scheduler.go`
(`RegisterPeriodicTasks`), the asynq replacement for Celery Beat.

### Sweep cadences

Cadence is chosen per subsystem from the cost of one tick and the SLA the
subsystem owes. The common bands:

| Cadence | What runs at it | Why this band |
|---|---|---|
| **60s** | cluster health check; cluster-condition remediation context; maintenance-window deferred-op dispatch; GitOps registration sync; kubectl-shell session reaper; CRD mirror gauge | Liveness and operator-facing state machines. 60s is the heartbeat/condition window — fast enough that a disconnect or a queued op is reflected within a minute. (Sub-minute outliers exist where the SLA is tighter: condition remediation 30s, fleet orchestrate 10s, webhook dispatch 15s, SIEM dispatch 2s.) |
| **5m** | project enforcement sweep; ArgoCD auto-adoption; NetworkPolicy apply; CRD-ownership drift check; anomaly baseline recompute; group-sync gauge; metrics aggregation | Apply/converge sweeps for objects that change rarely. 5m balances drift-detection latency against per-tick DB + cluster load. This is the workhorse band. |
| **15m** | apiserver allow-list reconcile; task-outbox / webhook drains (15s, the fast outbox variant) | Heavier per-cluster reconcile (GetEffective → diff → optional Apply → snapshot). 15m keeps the live-cluster GET volume down for objects whose blast radius is high but churn is low. |
| **30m** | NetworkPolicy drift sweep; cluster-registry drift reconcile; cloud-credential drift reconcile; CRD-mirror stale-row prune | **Drift detection** sweeps — GET the live object and compare to intent. Pricier than an idempotent apply (a read per row, no SSA short-circuit), so they run least often. The next apply tick (5m) does the actual re-stamp. |

(Daily cron entries — `0 2 * * *` style — handle retention/cleanup, not
convergence; they are out of scope here.)

The exact cadence for any task is the cron string in `scheduler.go`; treat that
file as authoritative if this table drifts.

---

## How drift is detected

Astronomer does **not** watch the Kubernetes API for change events. Drift is
detected on the **drift-sweep interval** by comparison, two ways:

1. **Spec-hash comparison.** The intent row stores a hash of the spec that was
   last applied. The reconciler recomputes the hash from current intent; a
   mismatch means *intent changed* and the row is re-applied. (Monitoring uses
   this for config drift.)
2. **Live-object inspection.** The drift sweep GETs the actual in-cluster object
   and checks for divergence — most commonly a missing or mismatched
   `managed-by` label, or the object having been deleted out-of-band. On
   divergence the row is marked `drifting` and an audit event is emitted; the
   next **apply** sweep re-stamps the object. See
   `internal/worker/tasks/network_policy_apply.go` for the canonical
   apply-sweep + drift-sweep pair (5m apply / 30m drift).

CRD-owned rows additionally get a **CRD-ownership drift check** (5m) that
surfaces DB rows whose Kubernetes `external_ref` vanished after a restore or
manual delete — the case where the *intent* survived but the *object* did not.

### Failure handling

Failed applies do not silently retry forever. Rows carry an attempt count and
**per-row exponential backoff** so one stuck cluster cannot tight-loop the
queue. The cluster-condition remediation reconciler is the reference
implementation: backoff 60s → 64m with a 12-attempts / 24h cap
(`internal/worker/tasks/cluster_condition_reconcile.go`). Drift detection feeds
a UI badge in the cases where auto-correct is intentionally *not* wired (e.g.
cluster-template and tool drift) — drift is reported, the operator decides.

---

## How this differs from Rancher controllers

Rancher's management plane is itself a Kubernetes control plane: every desired
state converges via **continuous, etcd-watching, leader-elected
controller-runtime / Wrangler reconcile loops**. A change to a resource fires a
watch event and the controller reconciles essentially immediately; desired
state lives in etcd objects and in-memory informer caches.

Astronomer deliberately chose the task-driven hybrid instead. The tradeoffs:

| Dimension | Rancher (controllers) | Astronomer (hybrid Postgres + asynq + CRD) |
|---|---|---|
| Desired-state store | etcd objects | **Postgres rows** (durable, queryable, joinable, backed up with the DB) |
| Convergence trigger | Watch event — near-instant | **Periodic sweep** (60s/5m/15m/30m) plus an enqueue on the mutating call |
| Drift latency | Effectively immediate (informer resync) | Bounded by the **drift-sweep interval** (typically 30m to detect, ≤5m to re-apply) |
| Worker model | Long-lived, leader-elected, stateful in-memory caches | **Stateless** workers, no lease held across ticks, state in the DB row |
| Audit / history | Resource generation + events (ephemeral) | **First-class**: status columns, attempt counts, audit-log rows, durable task-outbox — full convergence history survives restarts |
| Backoff / rate control | Controller workqueue rate limiting | **Per-row** attempt count + exponential backoff in the DB |
| Idempotency | Reconcile is the loop | Server-Side Apply makes a converged sweep a no-op |

**The honest cost:** drift is detected on a sweep interval, not instantly. If an
operator deletes a managed NetworkPolicy out-of-band, Astronomer notices on the
next 30m drift sweep and repairs it on the following 5m apply sweep — not within
seconds as a controller would.

**The deliberate benefit:** every convergence decision is durable, queryable,
and auditable in Postgres. You can ask "why is this cluster in this state and
what did we try" with a SQL query, the history survives a full restart, and a
single misbehaving cluster is contained by per-row backoff rather than
hot-looping a shared informer.

The CRD layer bridges the two worlds: teams that want a GitOps/declarative
workflow commit a `Cluster` CRD, and the controller-runtime adapter folds that
intent into the same Postgres-backed sweep machinery — so the declarative ergonomics
of a controller sit on top of an auditable task-queue core.

---

## Quick reference for on-call

- **"I changed something, when does it apply?"** Immediately (enqueue on the API
  call), with the periodic sweep as backstop on its cadence.
- **"Something drifted in the cluster, when does Astronomer fix it?"** Detected
  on the drift sweep (≈30m), re-applied on the next apply sweep (≈5m) — unless
  the subsystem is report-only (template/tool drift), where it raises a badge
  and waits for you.
- **"A reconcile is failing."** Look at the row's `status`, attempt count, and
  backoff timestamp in Postgres; the worker will retry on the backoff schedule,
  not every tick.
- **"Where is the cadence defined?"** `internal/worker/scheduler.go`,
  `RegisterPeriodicTasks`.
