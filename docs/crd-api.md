# CRD-based management API

When `crds.enabled=true`, Astronomer installs Kubernetes-native management APIs
under `management.astronomer.io/v1alpha1`. `Cluster` and `Project` are actively
reconciled to Postgres today. `ClusterBaseline`, `ComponentBundle`,
`AgentProfile`, and `GitOpsTarget` run validation/status reconcilers.
`ClusterBaseline` and `GitOpsTarget` also materialize Argo CD `ApplicationSet`
resources for supported specs, refuse to overwrite same-name ApplicationSets
without matching CRD ownership markers, and report first-pass aggregate
sync/health plus child Application/resource details from generated Argo CD
`Application` resources. Generated ApplicationSets are stamped with a desired
spec hash and report a repaired drift condition when an owned spec is changed
out of band. Durable operation rows are still roadmap work.

This is opt-in. The chart defaults `crds.enabled=false`, so existing installs
see no change.

## Architecture

Six CustomResourceDefinitions ship under the `management.astronomer.io/v1alpha1`
group:

| Kind | Backing owner today | Authoritative subset |
| --- | --- | --- |
| `Cluster` | Postgres `clusters` via controller | name, displayName, description, environment, region, provider, distribution, labels, annotations, projectRefs, ArgoCD adoption intent, baseline profile, agent privilege profile or AgentProfile reference, adoption policy |
| `Project` | Postgres `projects` via controller | name, displayName, description, podSecurityProfile, resourceQuota (cpuLimit / memoryLimit / podCount), networkPolicyMode, clusters |
| `ClusterBaseline` | Kubernetes API validation/status controller plus per-bundle ApplicationSet fanout | cluster selector, profile name, bundle list, version pins, sync policy, status conditions |
| `ComponentBundle` | Kubernetes API validation/status controller | default namespace, source repo/chart/path, values schema reference, secret refs, health checks, required capabilities, upgrade policy |
| `AgentProfile` | Kubernetes API validation/status controller | privilege profile, namespace scope, capability flags, allowed rules, host access flags, network egress profile, install metadata, effective RBAC status |
| `GitOpsTarget` | Kubernetes API validation/status controller plus supported ApplicationSet writer | cluster selector, project selector, bundle reference, ApplicationSet generation policy, parameters, sync policy, sync windows, status conditions |

A controller-runtime manager runs **inside the existing server pod** — there
is no second deployment to operate. The controller:

1. Watches the management namespace (`crds.watchNamespace`, default
   `astronomer-mgmt`).
2. For each reconciled `Cluster` / `Project` CR, calls the DB-side `EnsureFromCRD`
   adapter (`internal/server/crd_wiring.go`) which mirrors the spec to the
   row via the same sqlc queries the REST handler uses.
3. Patches `.status` with the resulting database state (cluster UUID, phase,
   agent version, last reconciled timestamp).
4. For `ClusterBaseline`, `ComponentBundle`, `AgentProfile`, and
   `GitOpsTarget`, validates the currently supported spec shape and patches
   standard status conditions.
5. For supported `ClusterBaseline` and `GitOpsTarget` specs, creates or
   repairs Argo CD `ApplicationSet` resources in `crds.argoNamespace` /
   `CRD_ARGO_NAMESPACE` (default `argocd`) using the standardized
   `astronomer.io/managed-by=astronomer` cluster selector boundary.
6. Reads generated Argo CD `Application` resources by the labels it stamps into
   ApplicationSet templates and rolls aggregate sync/health/count back into CRD
   status, including generated Application name, sync status, health, revision,
   operation message, and bounded resource status details. Existing same-name
   ApplicationSets are updated only when their CRD ownership labels and
   annotations match the source CR.
7. Requeues every 60s so DB-side drift from the REST API is reflected back to
   the CR.

Direction depends on the kind: `Cluster` / `Project` specs reconcile into
Postgres product rows, while supported `GitOpsTarget` specs reconcile into Argo
CD ApplicationSets. The controller does not mutate any CR's spec. Operators
using REST still work; their Cluster/Project changes appear on `.status` on
the next reconcile pass.

### Finalizers

The controller installs a finalizer on every CR it actively manages:

- `management.astronomer.io/decommission` (Cluster)
- `management.astronomer.io/cleanup` (Project)

The validation/status reconcilers install lightweight finalizers.
`ClusterBaseline` and `GitOpsTarget` use their finalizers to delete generated
ApplicationSets before the CR is released; the other new CRDs currently have no
external child resources:

- `management.astronomer.io/clusterbaseline-cleanup`
- `management.astronomer.io/componentbundle-cleanup`
- `management.astronomer.io/agentprofile-cleanup`
- `management.astronomer.io/gitopstarget-cleanup`

`kubectl delete cluster prod-us-east` enqueues the same decommission flow the
REST `DELETE /api/v1/clusters/{id}/` endpoint uses; the finalizer drops only
after `cluster_decommissions` reports `succeeded` and the row is tombstoned.
If deletion cleanup is still blocked after 15 minutes, the controller keeps the
finalizer in place and patches status to `phase: DeletingTimedOut` with
`Reconciled=False` and `Ready=False` conditions using reason
`FinalizerTimeout`. This is an operator signal only; it does not bypass
decommission, delete child ApplicationSets, or remove finalizers automatically.
For stuck deletes, use [crd-finalizer-recovery.md](runbooks/crd-finalizer-recovery.md)
before considering manual finalizer removal.

## Example — adopt a project + cluster via kubectl

```yaml
cat <<EOF | kubectl apply -f -
apiVersion: management.astronomer.io/v1alpha1
kind: AgentProfile
metadata:
  name: team-operator
  namespace: astronomer-mgmt
spec:
  privilegeProfile: namespace-operator
  namespaceScope: [astronomer-system]
  install:
    image: ghcr.io/alphabravocompany/astronomer-agent:v0.1.0
    serviceAccountName: astronomer-agent
    podLabels:
      team: platform
---
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
  argocd:
    autoAdopt: true
  baseline:
    profile: default
  agent:
    profileRef: team-operator
  adoptionPolicy:
    mode: auto
    allowedManagementModes: [argocd]
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
- `spec.adoptionPolicy` is the cluster-level declarative policy knob for
  management-mode intent. It is stored on the cluster row annotations as
  `management.astronomer.io/adoption-policy-mode` and
  `management.astronomer.io/allowed-management-modes`; it does not create a
  separate DB ownership domain. Use it to express whether the cluster should be
  auto-adopted and whether ArgoCD, Helm, or manual management are allowed.
- The CRD path runs in the server pod alongside REST. There is no separate
  agent / sidecar / second deployment.
- `spec.agent.profileRef` on a Cluster resolves a same-namespace
  `AgentProfile`. The Cluster reconciler projects the profile's
  `privilegeProfile` into the cluster row annotation
  `astronomer.io/agent-privilege-profile`, which is the same annotation the
  registration manifest renderer already consumes. The reconciler also projects
  `spec.install.image`, `spec.install.serviceAccountName`, and
  `spec.install.podLabels` into reserved manifest annotations so the generated
  Deployment image, ServiceAccount name, RoleBinding subject, and pod template
  labels can be profile-owned.
- `AgentProfile.spec.capabilities` and `spec.allowedRules` fail closed against
  the selected `privilegeProfile`. Built-in profiles cannot claim capabilities
  or rule shapes broader than the rendered manifest supports; `custom` profiles
  can claim a capability only when their declared external RBAC rules imply it.
- `ComponentBundle.spec.source.valuesSchemaRef` resolves a same-namespace
  ConfigMap as `name` or `name/key`; when the key is omitted the controller uses
  `values.schema.json`. `ClusterBaseline` bundle value overrides are converted
  from Helm dotted parameter syntax into a JSON object and validated against the
  referenced schema before an ApplicationSet is generated.
- `ClusterBaseline.spec.bundles[].values` is the UI/API-managed literal override
  surface for Helm parameters. `ClusterBaseline.spec.bundles[].valuesFrom`
  adds governed references for external override material:
  - `type: git` requires a safe relative `path` and is emitted into generated
    Argo CD Helm `valueFiles`.
  - `type: secret` and `type: configMap` require a same-namespace Kubernetes
    object `name`, accept an optional Kubernetes-valid `key`, and are validated
    as governance references only. The controller does not inline Secret or
    ConfigMap contents into generated ApplicationSets or Postgres.
  - Effective Helm precedence is `ComponentBundle` source defaults, then Git
    `valueFiles` in declaration order, then literal `values` rendered as Helm
    parameters. Secret-bearing chart inputs should remain behind
    `ComponentBundle.spec.source.secretRefs` or an external secret manager.
- `ComponentBundle.spec.versions[]` provides an additive multi-version catalog
  without breaking the existing top-level `spec.version` / `spec.source`
  contract. The top-level entry remains the current/backward-compatible bundle
  version; nested entries carry their own source, values schema reference,
  secret refs, capability requirements, health checks, and upgrade policy.
  `defaultNamespace` on a nested version falls back to the top-level
  `defaultNamespace` when omitted. `ClusterBaseline.spec.bundles[].version` and
  `GitOpsTarget.spec.bundleRef.version` resolve against either the top-level
  version or a nested version entry. Unknown pins fail closed and degrade the
  owning CR instead of falling back to another version. Duplicate version names
  are invalid, and `status.availableVersions` lists the pins the controller can
  resolve.

  ```yaml
  apiVersion: management.astronomer.io/v1alpha1
  kind: ComponentBundle
  metadata:
    name: ingress
    namespace: astronomer-mgmt
  spec:
    version: 1.0.0
    defaultNamespace: ingress-nginx
    source:
      type: helm
      repoURL: https://kubernetes.github.io/ingress-nginx
      chart: ingress-nginx
      targetRevision: 4.12.0
    versions:
      - version: 1.1.0
        source:
          type: helm
          repoURL: https://kubernetes.github.io/ingress-nginx
          chart: ingress-nginx
          targetRevision: 4.13.0
  ```
- `GitOpsTarget.spec.applicationSet.templateRef` resolves a same-namespace
  ConfigMap only when it is labeled
  `management.astronomer.io/gitops-template=true`. The ConfigMap must carry a
  `data.template.json` object with `project`, `destinationNamespace`, and a
  constrained `source` block (`type`, `repoURL`, `path` or `chart`,
  `targetRevision`). `source.secretRefs` are rejected in ConfigMap templates so
  secret-bearing source material stays out of inline catalog data.
- `GitOpsTarget.spec.projectSelector` is enforced against same-namespace
  `Project` CRs. A target with `projectSelector` must match at least one
  Project. When `spec.selector.clusterRefs` is used, every target cluster must
  appear in the matched Projects' `spec.clusters`. Label-only cluster selectors
  must include a durable project label (`astronomer.io/project`,
  `astronomer.io/project-id`, `astronomer.io/project.<name>`, or
  `astronomer.io/project-id.<uuid>`) that matches the selected Projects;
  otherwise the target degrades instead of generating a broad cross-tenant
  ApplicationSet. Argo managed-cluster Secrets receive the singular project
  labels only for single-project clusters and receive membership labels for
  every project on the cluster.
- Polling cadence is 60s per CR. Driven from `crd.ControllerConfig.PollPeriod`
  — wired in `internal/server/crd_wiring.go`.

## Controller roadmap

The current active product reconcilers cover `Cluster` and `Project`. The newer
`ComponentBundle` and `AgentProfile` reconcilers are intentionally limited to
first-pass validation/status behavior. `ClusterBaseline` and `GitOpsTarget`
include first supported ApplicationSet writers and aggregate generated
Application sync/health status. Generated ApplicationSets also carry desired
spec fingerprints and report repaired spec drift. The remaining roadmap items
are durable operation rows.

| Kind | Purpose | Required controller behavior |
| --- | --- | --- |
| `ClusterBaseline` | Declarative baseline components for cluster selectors or project/group selectors. | Current: validate selector/bundle shape, create/repair per-bundle ArgoCD ApplicationSets, stamp desired spec hashes, report repaired ApplicationSet spec drift, refuse unowned same-name ApplicationSets, delete stale/generated ApplicationSets, enforce referenced ComponentBundle version pins, roll aggregate child Application sync/health/count plus child Application/resource details into status, and patch conditions. Required next: record operation rows. |
| `ComponentBundle` | Reusable bundle catalog entry for charts, Kustomize paths, or raw Git paths. | Current: validate source shape, same-namespace values schema references, same-namespace secret refs, health checks, capability declarations, duplicate version pins, and nested `spec.versions[]` entries; validate `ClusterBaseline` override values against the selected version's referenced schema before Argo generation; expose `status.resolvedRevision` and `status.availableVersions`; make `ClusterBaseline` / `GitOpsTarget` bundle refs fail closed when `version` does not match the top-level version or a governed nested catalog entry; use the selected version's `source.targetRevision` as the Argo source revision; never inline secret refs into generated ApplicationSet sources. Required next: durable operation rows. |
| `AgentProfile` | Declarative agent privilege/capability profile. | Current: validate privilege profile, namespace scope, allowed-rule shape, capability claims, host-access restrictions, network egress shape, and install metadata; report effective RBAC summary; can be referenced by `Cluster.spec.agent.profileRef` to project the resolved privilege profile, agent image, service account name, and pod labels into registration manifest annotations. Required next: runtime operation feature gates should consume the same profile contract. |
| `GitOpsTarget` | Declarative ApplicationSet targeting policy. | Current: validate cluster selector safety, project selector shape, bundle reference shape, sync policy, and sync-window shape; enforce project selectors against same-namespace Projects for explicit cluster refs and durable project labels; create/repair supported ArgoCD ApplicationSets from direct source, ComponentBundle refs, or governed same-namespace ConfigMap template refs; stamp desired spec hashes; report repaired ApplicationSet spec drift; refuse unowned same-name ApplicationSets; roll aggregate child Application sync/health/count plus child Application/resource details into status; clean up generated ApplicationSets on delete. Required next: durable operation rows. |

All CRDs must keep the same versioning policy as `Cluster` and
`Project`: optional additive `v1alpha1` fields are allowed, but breaking schema
or ownership changes require a second served version and conversion webhook.

## Versioning and conversion policy

The current public API version is `management.astronomer.io/v1alpha1`.
Breaking CRD schema changes must not be made in-place.

Version progression:

1. Add `v1beta1` as a second served version while keeping `v1alpha1` served
   and storage-compatible.
2. Add a conversion webhook before any field rename, field removal, enum
   narrowing, semantic ownership change, or status/spec move.
3. Store only one version at a time; prefer the newest stable served version
   as storage after the webhook has round-trip tests.
4. Keep the previous served version for at least one minor release after the
   new version is available.
5. Move to `v1` only after the CRD fields, ownership rules, finalizers,
   status conditions, and restore behavior have passed upgrade and rollback
   drills.

Allowed in-place `v1alpha1` changes:

- Add optional spec fields with safe defaults.
- Add status fields.
- Add printer columns.
- Loosen validation.

Not allowed in-place:

- Removing or renaming spec fields.
- Changing a field's meaning or source of truth.
- Narrowing enum values.
- Moving a field between spec and status.
- Changing finalizer semantics.

Every new served version needs tests that prove:

- old objects convert to the new hub version;
- new objects convert back to the previous served version while it remains
  served;
- omitted fields preserve existing default behavior;
- CRD-owned Postgres rows keep `external_ref_*`, `managed_by`, and
  `observed_generation` semantics through conversion.

## Disabling

Set `crds.enabled=false` (the default). The chart omits the CRD manifests, the
RBAC, and the `CRD_ENABLED` env on the server pod. Existing CRs are left
untouched (the CRD is kept across uninstall via the `helm.sh/resource-policy:
keep` annotation) but no controller observes them.

To completely remove the CRDs:

```sh
kubectl delete crd \
  clusters.management.astronomer.io \
  projects.management.astronomer.io \
  clusterbaselines.management.astronomer.io \
  componentbundles.management.astronomer.io \
  agentprofiles.management.astronomer.io \
  gitopstargets.management.astronomer.io
```

This is permanent; CR data backed by the DB stays intact.

## Deferred

- Conversion webhook implementation for a future `v1beta1` schema. Today only
  `v1alpha1` is served, and the policy above defines when the webhook becomes
  mandatory.
- Full product reconcilers for `ClusterBaseline`, `ComponentBundle`,
  `AgentProfile`, and the remaining `GitOpsTarget` modes. Validation/status
  controllers are installed and supported `ClusterBaseline` / `GitOpsTarget`
  specs can write ApplicationSets and report aggregate child Application
  sync/health plus child Application/resource details; durable operation rows
  are still pending.
- Mirroring beyond the shipped management CRDs. Other tables (`monitoring_*`,
  `argocd_*`, `cluster_templates`, `cluster_registries`, etc.) stay REST-only
  for now.
- DB → CRD watch via Postgres `LISTEN`. The current implementation polls
  every 60s; LISTEN/NOTIFY would tighten the loop to near-zero latency.
