# CRD-based management API

When `crds.enabled=true`, every astronomer-go cluster + project can be managed
via `kubectl`, GitOps tooling (ArgoCD / Flux), and the rest of the standard
Kubernetes ecosystem. The REST API keeps working unchanged — both surfaces
eventually-converge to the same DB row.

This is opt-in. The chart defaults `crds.enabled=false`, so existing installs
see no change.

## Architecture

Two CustomResourceDefinitions ship under the `management.astronomer.io/v1alpha1`
group:

| Kind      | DB table   | Authoritative subset                                                    |
| --------- | ---------- | ----------------------------------------------------------------------- |
| `Cluster` | `clusters` | name, displayName, description, environment, region, provider, distribution, labels, annotations, projectRefs |
| `Project` | `projects` | name, displayName, description, podSecurityProfile, resourceQuota (cpuLimit / memoryLimit / podCount), networkPolicyMode, clusters |

A controller-runtime manager runs **inside the existing server pod** — there
is no second deployment to operate. The controller:

1. Watches the management namespace (`crds.watchNamespace`, default
   `astronomer-mgmt`).
2. For each `Cluster` / `Project` CR, calls the DB-side `EnsureFromCRD`
   adapter (`internal/server/crd_wiring.go`) which mirrors the spec to the
   row via the same sqlc queries the REST handler uses.
3. Patches `.status` with the resulting database state (cluster UUID, phase,
   agent version, last reconciled timestamp).
4. Requeues every 60s so DB-side drift from the REST API is reflected back to
   the CR.

Direction: **CRD spec → DB**. The controller does not mutate the CR's spec.
Operators using REST still work; their changes appear on `.status` on the
next reconcile pass.

### Finalizers

The controller installs a finalizer on every CR it manages:

- `management.astronomer.io/decommission` (Cluster)
- `management.astronomer.io/cleanup` (Project)

`kubectl delete cluster prod-us-east` enqueues the same decommission flow the
REST `DELETE /api/v1/clusters/{id}/` endpoint uses; the finalizer drops only
after `cluster_decommissions` reports `succeeded` and the row is tombstoned.

## Example — provision a project + cluster via kubectl

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: management.astronomer.io/v1alpha1
kind: Cluster
metadata:
  name: prod-us-east
  namespace: astronomer-mgmt
spec:
  name: prod-us-east
  environment: production
  labels:
    tier: prod
    region: us-east-1
  projectRefs: [platform]
---
apiVersion: management.astronomer.io/v1alpha1
kind: Project
metadata:
  name: platform
  namespace: astronomer-mgmt
spec:
  name: platform
  description: Platform team
  podSecurityProfile: baseline
  resourceQuota:
    cpuLimit: "16"
    memoryLimit: "32Gi"
    podCount: 100
  networkPolicyMode: isolated
  clusters: [prod-us-east]
EOF
```

```sh
kubectl get clusters.management.astronomer.io
# NAME           ENVIRONMENT   PHASE        AGE
# prod-us-east   production    registered   42s

kubectl describe cluster prod-us-east
# … shows status.clusterId, status.phase, status.observedProjectRefs
```

## Operational notes

- The CR's `spec.name` is the value written to `clusters.name` /
  `projects.name`. In practice operators set it to the same value as
  `metadata.name`; the OpenAPI schema enforces RFC-1123 on both.
- `spec.clusters` on a Project is a list, but only the first entry is
  authoritative today — `projects.cluster_id` is single-valued. Extra entries
  surface on `status.observedClusters` for visibility.
- Cluster decommission via CR delete cannot remove the local management
  cluster — same guard as the REST API. The controller surfaces the refusal
  as a delete-in-progress retry, so `kubectl delete` will appear to hang
  until the operator force-removes the finalizer (don't).
- The CRD path runs in the server pod alongside REST. There is no separate
  agent / sidecar / second deployment.
- Polling cadence is 60s per CR. Driven from `crd.ControllerConfig.PollPeriod`
  — wired in `internal/server/crd_wiring.go`.

## Disabling

Set `crds.enabled=false` (the default). The chart omits the CRD manifests, the
RBAC, and the `CRD_ENABLED` env on the server pod. Existing CRs are left
untouched (the CRD is kept across uninstall via the `helm.sh/resource-policy:
keep` annotation) but no controller observes them.

To completely remove the CRDs:

```sh
kubectl delete crd clusters.management.astronomer.io projects.management.astronomer.io
```

This is permanent; CR data backed by the DB stays intact.

## Deferred

- Conversion webhook for a future `v1beta1` schema. Today only `v1alpha1` is
  served.
- Conditions are exposed as a free-form `array of object` in the schema so
  the controller can populate them without dragging the apimachinery
  validation into the chart. A future iteration will narrow this to the
  standard `metav1.Condition` shape.
- Mirroring beyond `Cluster` + `Project`. Other tables (`monitoring_*`,
  `argocd_*`, `cluster_templates`, `cluster_registries`, etc.) stay
  REST-only for now.
- DB → CRD watch via Postgres `LISTEN`. The current implementation polls
  every 60s; LISTEN/NOTIFY would tighten the loop to near-zero latency.
