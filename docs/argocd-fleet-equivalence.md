# ArgoCD Fleet Equivalence

Astronomer's answer to Rancher Fleet's "cluster groups + bundles" model is the
combination of:

1. Astronomer's per-cluster **labels** (the `clusters.labels` JSONB column,
   editable from the Cluster Settings UI or `PUT /api/v1/clusters/{id}/`).
2. Argo CD's **`ApplicationSet`** resource using a **`clusters` generator**
   with `selector.matchLabels`.

This document explains the wiring, the label conventions, and how an operator
deploys a bundle to every cluster matching a label set.

## What Rancher Fleet does

Fleet pairs a `Bundle` (a directory of K8s manifests) with a `BundleTarget`
that uses `matchLabels` against `Cluster` CRs in the Fleet namespace. New
clusters labeled correctly automatically receive the bundle.

## What Astronomer does

```
clusters.labels (DB)
        │
        │  on register / on Update
        ▼
Argo CD cluster Secret (argocd ns) labels
        │
        ▼
ApplicationSet.spec.generators[].clusters.selector.matchLabels
        │
        ▼
One Application per matched cluster → automatic deployment
```

Each Astronomer-managed cluster that's been registered with an ArgoCD instance
has a backing **Secret** of type `argocd.argoproj.io/secret-type=cluster` in
the `argocd` namespace. Astronomer stamps that Secret with a known set of
labels every time:

- The cluster is first registered into an ArgoCD instance (handler:
  `RegisterManagedCluster` in
  [`internal/handler/argocd.go`](../internal/handler/argocd.go)).
- The cluster's `labels` are mutated via `PUT /api/v1/clusters/{id}/`. An
  asynq task (`argocd:refresh_managed_cluster_labels`) re-stamps the labels on
  every Secret tied to the cluster.

## Label conventions

Every Astronomer-managed Argo cluster Secret carries:

| Label key                       | Value                                | Source                                     |
| ------------------------------- | ------------------------------------ | ------------------------------------------ |
| `astronomer.io/managed-by`      | `astronomer`                         | Always (rendezvous label for "everything") |
| `astronomer.io/cluster-id`      | the cluster UUID                     | Always                                     |
| `astronomer.io/cluster-name`    | the cluster's RFC-1123 name          | Always                                     |
| `astronomer.io/environment`     | `clusters.environment`               | When set                                   |
| `astronomer.io/label-<key>`     | the value from `clusters.labels`     | One per `(k, v)` in `clusters.labels`      |

The `<key>` portion of `astronomer.io/label-<key>` is **sanitized** to satisfy
the Kubernetes label-key rules (`[a-z0-9.-]`, max 63 chars, start/end with
alphanumeric). The sanitization rules are:

- ASCII uppercase folded to lowercase (`Tier` → `tier`)
- Anything outside `[a-z0-9.-]` replaced with `-`, runs collapsed
- Leading/trailing non-alphanumerics stripped
- Truncated to 63 characters after the prefix

**The sanitization is one-way.** Operators reading the Secret see only the
sanitized form (`astronomer.io/label-team-name`), not the original
(`Team Name`). If two cluster-label keys collide after sanitization (rare),
the first one wins.

## Worked example

Say you have three clusters in Astronomer:

| Cluster name | `labels`                                      |
| ------------ | --------------------------------------------- |
| `prod-east`  | `{"tier":"prod","environment":"us-east"}`     |
| `prod-west`  | `{"tier":"prod","environment":"us-west"}`     |
| `staging-1`  | `{"tier":"staging","environment":"us-east"}`  |

After Astronomer registers each cluster into ArgoCD, the cluster Secrets in
the `argocd` namespace carry labels like:

```yaml
# Secret for prod-east
metadata:
  labels:
    argocd.argoproj.io/secret-type: cluster
    astronomer.io/managed-by: astronomer
    astronomer.io/cluster-id: 6f3...
    astronomer.io/cluster-name: prod-east
    astronomer.io/label-tier: prod
    astronomer.io/label-environment: us-east
```

To deploy a monitoring stack to **every prod cluster in us-east**, create an
ApplicationSet:

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: prod-east-monitoring
  namespace: argocd
spec:
  generators:
    - clusters:
        selector:
          matchLabels:
            astronomer.io/managed-by: astronomer
            astronomer.io/label-tier: prod
            astronomer.io/label-environment: us-east
  template:
    metadata:
      name: '{{name}}-monitoring'
    spec:
      project: default
      source:
        repoURL: https://charts.example.com/monitoring
        targetRevision: 1.2.3
        chart: stack
      destination:
        server: '{{server}}'
        namespace: monitoring
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
```

ArgoCD will produce one `Application` per matching cluster (in this case,
`prod-east-monitoring`). When a fourth cluster `prod-east-2` is registered
into Astronomer with the same labels, the ApplicationSet controller picks it
up automatically and produces `prod-east-2-monitoring` — no manual step.

Conversely, removing the `tier=prod` label from `clusters.labels` triggers
the `argocd:refresh_managed_cluster_labels` task, which re-stamps the Secret
without `astronomer.io/label-tier=prod`, and the ApplicationSet controller
deletes the corresponding Application on its next sync.

## Targeting every Astronomer-managed cluster

For a bundle that should land on *every* registered cluster, target the
rendezvous label only:

```yaml
selector:
  matchLabels:
    astronomer.io/managed-by: astronomer
```

This is the equivalent of Fleet's "all clusters" Bundle target.

## Caveats

- **Propagation latency.** The label refresh runs as an asynq task. In a
  healthy deployment it lands within seconds; under a backlog or redis
  outage it may take longer. The periodic ArgoCD reconciler does NOT
  re-stamp labels — only the explicit task does — so a label change while
  the queue is down would stay stale until you re-issue
  `PUT /api/v1/clusters/{id}/`.
- **Sanitization is one-way.** `Team Name` becomes `astronomer.io/label-team-name`
  with no reverse mapping stored. Pick label keys that are already
  Kubernetes-shaped if you care about round-tripping.
- **Label keys collide on sanitization.** `Team-Name` and `team_name` both
  sanitize to `team-name`. Two cluster labels with the same sanitized form
  is undefined behavior — the first-iterated key wins. The Astronomer UI
  should warn on label-key collision before saving.
- **Argo-reserved labels are not touched.** `argocd.argoproj.io/secret-type`
  and any user-added labels not under `astronomer.io/` are preserved through
  every refresh.
- **Decommission semantics.** When a cluster is decommissioned, the
  `argocd_managed_clusters` row is cascaded-deleted via FK. The upstream
  Argo cluster Secret is removed by the explicit unregister flow
  (`DELETE /api/v1/argocd/instances/{id}/clusters/{cluster_id}/register/`),
  not by the decommission reconciler — operators should call unregister
  before decommission for a clean shutdown.
- **Namespace constraints.** The destination namespace in the
  ApplicationSet template is up to the operator. Astronomer does not auto-
  create namespaces on managed clusters from this path — the Application
  itself does (via `CreateNamespace=true` in the Application's
  `syncOptions`) or the operator does, out-of-band.

## Code references

- Label projection: `managedClusterLabels` in
  [`internal/handler/argocd.go`](../internal/handler/argocd.go).
- Refresh task:
  [`internal/worker/tasks/argocd_refresh_managed_cluster.go`](../internal/worker/tasks/argocd_refresh_managed_cluster.go).
- Update enqueue: `enqueueArgoCDLabelRefresh` in
  [`internal/handler/clusters.go`](../internal/handler/clusters.go).
- DB query:
  [`internal/db/queries/argocd.sql`](../internal/db/queries/argocd.sql) —
  `ListArgoCDManagedClustersByCluster`.
