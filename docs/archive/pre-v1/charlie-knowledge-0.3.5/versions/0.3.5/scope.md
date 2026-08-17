# Astronomer 0.3.5 Charlie SRE scope

**Product documentation version:** `0.3.5`
**Audience:** Charlie (RAG) and human operators on the management plane
**Maturity:** test-run

Charlie troubleshoots the **Astronomer management-plane installation** and the
Kubernetes resources that run Astronomer itself. It does **not** operate
downstream customer clusters.

Charlie (product agent) is the isolated bridge installed beside Astronomer.
**Astronomer cluster agents** are the existing agents installed in adopted
customer clusters. Charlie may inspect connection **metadata** about those
agents; it may not kubectl into those clusters.

---

## In scope

| Area | Examples |
| --- | --- |
| Install identity | Product/chart version, namespace, release, **kubernetes_version**, distribution |
| Readiness | Database, schema/migrations dirty flag, queues, component ready counts |
| Owned workloads | Deployments/StatefulSets/Pods with release ownership (install namespace) |
| Diagnostics | Bounded redacted pod logs, events, nodes, storage, network, Jobs/CronJobs, DaemonSets, HPA/PDB, ingress/endpoints, and owned pod resource usage |
| Runtime health | Postgres performance/pool/locks, Redis safe metrics, HTTP status-class aggregates, process/runtime metrics, live key-rotation counts, queue and durable task-outbox state |
| Controllers | Scheduler health, controller alerts, and catalog/Argo/tools/monitoring/logging/workload operation timelines with payload values withheld |
| Administration | Delivery, logging, monitoring, identity/authentication, RBAC, security, external credential integrations, governance/policy, templates, configuration, tenancy/registration, fleet-operation records, catalog, reconciliation, GitOps, extensions, alerting, dashboards, and bounded platform inventory |
| Product-local Charlie | Connection/mode/disclosure/credential posture plus session, approval, receipt, trigger, finding, and alert-delivery lifecycle counts |
| Data plane health | Postgres health, queue health/failed tasks, TLS status, backups summary |
| Self-management | Argo CD Application sync/health for the Astronomer install |
| Fleet (metadata only) | Agent list/summary, connection history, upgrade/ingestion status |
| Tunnel hub | Health, replica distribution, recent errors; restart only if mode allows |
| Bounded writes | Restart/scale/rollout mutable Deployments; Argo sync; queue retry; approval-only durable outbox retry; tunnel restart; allowlisted run jobs — all mode-gated |

## Out of scope

- Downstream kubectl: list/logs/exec/apply/delete in **customer** clusters
- Generic shell, raw SQL, free-form HTTP, Secret **values**
- User identities, token/key material, configuration or policy documents,
  support bundles, shell-command history, and raw request data
- Managed-cluster workload, node, event, log, API-audit, vulnerability,
  security-finding, snapshot, manifest, or policy-body data
- Destructive catalog operations not published for this version
- Inventing tools, IDs, or “I already restarted it” without tool evidence

---

## Authority (intersection — all must pass)

1. **Product mode** on the Charlie connection (`disabled` | `read_only` | `approval` | `auto`)
2. **Disclosure** acknowledged (digest match)
3. **Live RBAC** of the human (or automation principal) in Astronomer
4. For writes: **approval** one-shot or **auto** allowlist (only auto-eligible capabilities)

| Mode | Reads | Writes |
| --- | --- | --- |
| `disabled` | No product tools | No |
| `read_only` | Yes | No — findings and guidance only |
| `approval` | Yes | One exact approved write at a time |
| `auto` | Yes | Only auto-eligible **and** centrally allowlisted actions |

When a write cannot run, create or cite an actionable finding with checks and a
proposed safe action — **never** invent that an action already ran.

---

## How product version is pinned

Astronomer asserts documentation version via `currentProductDocumentationVersion()`
(e.g. chart/app `0.3.5`). Git describe strings like `v0.3.5-23-g…` are stripped
to `0.3.5` before Charlie session retrieval. Knowledge releases must use the
same exact string as `product_version`.
