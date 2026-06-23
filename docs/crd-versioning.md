# CRD API versioning

The `management.astronomer.io` CustomResourceDefinitions are being promoted from
`v1alpha1` toward a stable `v1beta1`. As of this change the group serves **both**
versions side by side; `v1alpha1` remains the storage version so no stored
objects are rewritten by the promotion.

## What is served today

| Version    | Served | Storage | Notes |
| ---------- | ------ | ------- | ----- |
| `v1alpha1` | yes    | yes     | Original version. Stays the storage version. |
| `v1beta1`  | yes    | no      | Promoted version. Identical schema, identity conversion. |

Both versions are emitted in every management CRD's `spec.versions` list (see the
`deploy/chart/templates/crd-*.yaml` templates). At present the promotion is wired
for `Cluster`; the remaining kinds follow the same pattern and are tracked as
remaining work below.

## Identity conversion

`v1alpha1` and `v1beta1` share the **same Go types** in
`internal/crd/types.go` — there are no field renames, removals, or shape
changes between the two versions. The runtime scheme registers each kind under
both `GroupVersion` (v1alpha1) and `GroupVersionV1Beta1`, so converting an
object from one version to the other is the identity transform.

Because the two schemas are byte-for-byte identical, the Kubernetes apiserver's
built-in `None` conversion strategy is sufficient: **no conversion webhook is
required** while the schemas do not diverge. The CRD templates therefore do not
declare a `spec.conversion` block.

## Operator impact

- Existing `apiVersion: management.astronomer.io/v1alpha1` manifests keep working
  unchanged.
- New manifests may use `apiVersion: management.astronomer.io/v1beta1`. They
  describe the exact same fields.
- `kubectl get <kind>` returns objects at the storage version (`v1alpha1`)
  unless a specific version is requested.

## When v1beta1 diverges (future work)

The moment a field is added/renamed/removed only in `v1beta1`, identity
conversion no longer holds and one of the following must be wired before the
schemas are allowed to differ:

1. A second Go type set for `v1beta1` plus `runtime.Convertible`
   (`ConvertTo` / `ConvertFrom`) hub-and-spoke conversion, **and**
2. A conversion webhook declared in each CRD's `spec.conversion` (strategy
   `Webhook`), backed by a webhook server in the controller manager.

Until then the single shared type set keeps the promotion small and safe.

## Remaining work

- Multi-version serving is wired and tested for the `Cluster` CRD template.
  Promote the other five kinds (`Project`, `ClusterBaseline`, `ComponentBundle`,
  `AgentProfile`, `GitOpsTarget`) the same way.
- No conversion webhook exists; it is only needed once the v1beta1 schema
  diverges from v1alpha1.
