# Kubernetes-native management API

Astronomer optionally exposes three generic management resources in
`management.astronomer.io/v1alpha1`: `Cluster`, `Project`, and `AgentProfile`.
They provide a Kubernetes-native inlet for cluster adoption and governance.
They are not a second product database and they are not a delivery API.

Delivery sources, immutable bundles, targets, placement snapshots, rollouts,
deployments, approvals, and status are owned by PostgreSQL and the first-party
`/api/v1/delivery/` API. The cluster agent materializes that accepted intent as
a closed set of Flux resources in each managed cluster. Astronomer does not
install delivery CRDs in the management cluster.

## Enable the API

The chart installs and watches the management resources only when explicitly
enabled:

```yaml
crds:
  enabled: true
  watchNamespace: astronomer-mgmt
```

Create `watchNamespace` before installing the chart. The embedded controller
runs in the server process, uses leader election when configured, and watches
only that namespace. Disable chart-owned CRD installation only when the
platform team installs the exact CRDs from the same release independently.

## Ownership

| Kind | Durable owner | Reconciled fields |
| --- | --- | --- |
| `Cluster` | PostgreSQL `clusters` and related ownership records | identity, display metadata, environment/provider/distribution, labels, annotations, project references, agent profile, adoption policy |
| `Project` | PostgreSQL `projects` and membership/policy records | identity, display metadata, pod-security profile, quota, network-policy mode, cluster membership |
| `AgentProfile` | Kubernetes object validated by the embedded controller | privilege profile, namespace scope, bounded RBAC rules, capabilities, host access, network egress, install metadata |

`Cluster` and `Project` specs converge through the same persistence boundary as
the REST API. Ownership conflicts fail closed and appear in `.status.conditions`.
The controller never treats the status subresource as durable intent.

`AgentProfile` is same-namespace only. A `Cluster` may reference it with
`spec.agent.profileRef`; cross-namespace and slash-containing references are
rejected. The controller validates the profile and publishes the effective
RBAC summary before using its privilege profile.

## Example

```yaml
apiVersion: management.astronomer.io/v1alpha1
kind: AgentProfile
metadata:
  name: production-operator
  namespace: astronomer-mgmt
spec:
  privilegeProfile: namespace-operator
  namespaceScope:
    - applications
---
apiVersion: management.astronomer.io/v1alpha1
kind: Cluster
metadata:
  name: payments-production
  namespace: astronomer-mgmt
spec:
  name: payments-production
  displayName: Payments Production
  environment: production
  provider: eks
  distribution: eks
  projectRefs:
    - payments
  agent:
    profileRef: production-operator
  adoptionPolicy:
    mode: adopt
    allowedManagementModes:
      - observe
      - operate
```

Check generation-current conditions instead of assuming that an accepted
`kubectl apply` has reached PostgreSQL:

```bash
kubectl -n astronomer-mgmt get cluster payments-production \
  -o jsonpath='{.metadata.generation}{" "}{.status.conditions[?(@.type=="Ready")].observedGeneration}{" "}{.status.conditions[?(@.type=="Ready")].status}{"\n"}'
```

## Finalizers and deletion

The active finalizers are:

- `management.astronomer.io/decommission` for `Cluster`;
- `management.astronomer.io/cleanup` for `Project`;
- `management.astronomer.io/agentprofile-cleanup` for `AgentProfile`.

Cluster deletion delegates to the normal audited decommission workflow and may
remain pending while that operation runs. Project deletion removes its durable
product object through the ownership adapter. Agent profiles have no downstream
delivery children; their finalizer only lets the controller finish its own
status lifecycle. Follow
[CRD finalizer recovery](runbooks/crd-finalizer-recovery.md) before considering
a manual finalizer patch.

## Delivery workflow

Use the delivery API or dashboard to create sources, bundle versions, targets,
and rollouts. Preview the target's resolved placement before starting a
rollout, use `If-Match` for mutable lifecycle commands, and inspect normalized
cluster inventory through `/api/v1/delivery/clusters/{clusterId}/inventory/`.
See [the delivery control-plane runbook](runbooks/delivery-control-plane.md) and
[the Flux-native delivery ADR](architecture/decisions/flux-native-delivery.md).
