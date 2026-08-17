# ADR: Flux-native multi-cluster delivery

- **Status:** Accepted
- **Date:** 2026-08-17
- **Target:** Astronomer v1.0.0
- **Owners:** Product, platform engineering, security, and SRE
- **Machine contract:** [`deploy/release/compatibility.yaml`](../../../deploy/release/compatibility.yaml)

## Context

Astronomer v0.3.x contains two overlapping deployment mechanisms: a centrally
hosted Argo CD integration and an imperative `fleet_operations` fan-out engine.
Both expose execution-engine concepts through product APIs and duplicate
placement, progress, and lifecycle behavior. Neither provides the required
Rancher-like operating property: an outbound cluster agent plus a local
reconciler that continues enforcing already-released intent during a management
plane outage.

The desired product remains an Astronomer product. Customers should reason
about sources, bundles, targets, placement, rollouts, and cluster deployments;
they should not have to operate or learn a generic GitOps dashboard.

## Decision

Astronomer will use **local Flux controllers as a private execution substrate**
on every managed cluster. Astronomer owns the product API, UI, CLI, placement,
approval, rollout state machine, audit trail, and normalized status. Flux owns
source acquisition and in-cluster Helm/Kustomize convergence after an assignment
has been released to that cluster.

Rancher Fleet is not installed, imported, vendored, called, wrapped, or exposed.
In particular, no `fleet.cattle.io` API, Rancher Fleet controller, or Rancher
Fleet compatibility surface is permitted. The unqualified word “fleet” is not
a new product or code name; new code uses `delivery`, `deployment`, `rollout`, or
`cluster agents`.

Argo CD is not a provider behind a new abstraction. The v1 runtime has one
delivery path and no dual-engine mode. The existing Argo chart, proxy, API,
database, controller, UI, CLI, documentation, and self-management surfaces are
deleted before v1 ships. The imperative `fleet_operations` engine is deleted
for the same reason.

## Ownership and topology

```text
user / Charlie / CLI
        |
        v
Astronomer API -- PostgreSQL (authoritative product state and history)
        |
        | authenticated, versioned assignment snapshots
        v
outbound Astronomer agent (authenticated cluster identity and policy boundary)
        |
        | validates and materializes a closed set of namespaced Flux objects
        v
local Flux: source-controller + kustomize-controller + helm-controller
        |
        v
downstream workloads
```

PostgreSQL is authoritative for delivery sources, encrypted credential records,
component bundles and immutable versions, delivery targets, placement snapshots,
rollouts, cluster deployments, assignment generations, normalized status,
operations, audit events, and transactional outbox rows. Kubernetes objects are
reconciliation state, not the product database. A lost downstream Flux object
can be recreated from an assignment; a lost PostgreSQL product row cannot be
reconstructed authoritatively from Flux.

The management plane never receives a general-purpose downstream kubeconfig.
The existing outbound, mutually authenticated agent tunnel remains the only
management path. Each agent:

1. negotiates a typed protocol and capabilities with the server;
2. requests a monotonic assignment snapshot bound to its authenticated cluster;
3. rejects unknown fields, stale generations, invalid digests, disallowed
   scope, cross-namespace references, and unbounded payloads before mutation;
4. materializes deterministic, Astronomer-labelled Flux resources and the
   minimum namespace-scoped RBAC/credentials they require;
5. observes Flux Conditions, revision, inventory, drift, and events; and
6. reports a bounded, redacted, normalized status snapshot.

Flux continues reconciling the last accepted generation when the tunnel or
management plane is unavailable. Disconnect never means delete. Deletion
requires an explicit, generation-fenced tombstone and completion report.

## Product and Flux boundary

| Astronomer owns | Flux owns | Neither may do |
| --- | --- | --- |
| Source policy and immutable resolution | Fetching the exact released Git/OCI/Helm source | Follow a moving branch/tag after approval |
| Cluster/group selection and placement snapshot | No placement logic | Infer target clusters from Flux resources |
| Approval, windows, rollout cohorts, failure budgets, pause/abort/rollback | Reconcile a released per-cluster generation | Advance a rollout from an untrusted downstream event |
| Credential encryption, rotation intent, and audited projection | Reading only the projected namespaced Secret | Return secret values in status, events, logs, or audit |
| Normalized deployment health and user-facing diagnostics | Native Conditions and inventory | Require a generic Flux UI for normal operation |
| System distribution cohorts for agent/Flux lifecycle | Running the pinned controllers | Let workload assignments mutate Flux or agent system objects |

Only `source-controller`, `kustomize-controller`, and `helm-controller` are in
the v1 distribution. Notification and image-automation controllers are omitted:
the agent already owns status transport, and source revisions are resolved to
immutable identifiers before rollout approval.

## Security invariants

- The agent validates a closed assignment schema and derives object names,
  namespaces, labels, and ownership annotations. User-supplied arbitrary Flux
  YAML is never transported or applied.
- Workload delivery is namespace-scoped and uses a generated service account.
  Cluster-scoped objects require an explicit privileged bundle policy and
  separate approval; an ordinary assignment cannot create CRDs, ClusterRoles,
  webhooks, or controller Deployments.
- Flux cross-namespace references and remote Kustomize bases are disabled.
  Controllers have default-deny network policy, non-root/read-only security
  contexts, resource bounds, and no externally exposed receiver.
- Mutable Git refs, OCI tags, and Helm inputs are resolved once. The approved
  bundle version stores commits and digests; downstream objects use those exact
  identifiers.
- Credential plaintext is never stored in JSON specs, protocol status, task
  payloads, audit detail, or logs. Projection is scoped, redacted, rotated, and
  deleted with its assignment.
- Assignment release and status ingestion are idempotent, cluster-bound,
  generation-fenced, size-bounded, and safe under retries or stale sessions.
- Flux controller versions and images are release-pinned. The release pipeline
  verifies upstream checksums/provenance, generates a reviewed hardened
  distribution, and publishes immutable Astronomer artifacts with SBOM and
  signatures.

The detailed supported versions, limits, and SLOs are generated from the
[compatibility contract](../compatibility.md). A release must not advertise a
Kubernetes minor or protocol version absent from that contract and its declared
qualification invocation.

## Data and upgrade contract

Astronomer v1 is a **fresh-install-only** release. It has one `001_initial`
migration representing the final v1 schema. A v0.3.x database is rejected by a
non-mutating preflight with export/reset/reinstall guidance. There is no Argo,
`fleet_operations`, or other legacy data importer, no automatic database reset,
and no compatibility schema retained after the migration squash.

Management-plane upgrades remain explicit, tagged Helm upgrades. Downstream
Flux does not manage the Astronomer management-plane release. Agent and Flux
system upgrades use signed, digest-pinned system assignments, compatibility
preflight, canary cohorts, failure budgets, and a tested previous distribution.

## Rejected alternatives

### Rancher Fleet

Rejected because it would make a direct Rancher subsystem part of a competing
product, expose another API/data model, and limit ownership of placement and UX.
Fleet’s downstream-pull architecture remains useful comparative input, but
Rancher Fleet is not a dependency or compatibility target.

### Central Argo CD

Rejected because the management plane becomes the reconciliation bottleneck,
needs downstream access/tunnels for execution, exposes substantial engine UI and
API surface, and stops converging managed clusters when the central engine is
unavailable.

### Central Flux using remote kubeconfigs

Rejected for the same topology reason. Flux supports remote targets, but this
would retain central credentials and hub-push failure characteristics. Local
Flux is the production topology.

### A generic Flux UI or direct Flux API

Rejected because it leaks implementation details and creates two control
planes. Advanced diagnostics may contain bounded, sanitized Flux condition
details, but all supported actions go through typed Astronomer APIs.

### A provider framework or dual-engine migration

Rejected because v1 is greenfield. A speculative provider abstraction would
add code without a second supported implementation and would preserve legacy
behavior. Internal package boundaries may isolate materialization logic, but
there is exactly one runtime executor.

### Building another reconciler

Rejected because source acquisition, Helm lifecycle, server-side apply, prune,
health, and drift correction are mature Flux responsibilities. Astronomer owns
only the differentiated placement, policy, rollout, protocol, and UX layers.

## Consequences

- Managed clusters run three additional, pinned controllers, but reconciliation
  survives control-plane outages and scales with cluster resources.
- The agent becomes a security-critical translator and observer. Its schema,
  RBAC, materialization golden tests, and version skew require release gates.
- v1 deliberately breaks upgrades from v0.3.x. This removes migration and dual-
  engine complexity, at the cost of requiring a new install and re-registration.
- Users receive one first-party delivery model. Flux-specific troubleshooting
  remains an operator diagnostic detail rather than a normal workflow.
- Removing Argo and the imperative fan-out engine is a release blocker, not
  optional cleanup.

## Upstream references

- [Flux components and controller APIs](https://fluxcd.io/flux/components/)
- [GitOps Toolkit design](https://fluxcd.io/flux/gitops-toolkit/)
- [Flux source-controller](https://fluxcd.io/flux/components/source/)
- [Flux Kustomization reconciliation](https://fluxcd.io/flux/components/kustomize/kustomizations/)
- [Flux HelmRelease lifecycle](https://fluxcd.io/flux/components/helm/helmreleases/)
- [Flux security best practices](https://fluxcd.io/flux/security/best-practices/)
- [Flux multi-tenancy lockdown](https://fluxcd.io/flux/installation/configuration/multitenancy/)
- [Flux release and Kubernetes support policy](https://fluxcd.io/flux/releases/)
- [Flux v2.9.3 release](https://github.com/fluxcd/flux2/releases/tag/v2.9.3)
- [Rancher Fleet architecture (comparative input only)](https://fleet.rancher.io/explanations/architecture)

## Enforcement

`scripts/check-legacy-delivery-surface.sh` inventories the v0.3.x baseline and
becomes a release-blocking zero-tolerance check at cutover. The check separates
allowlisted historical decisions/migrations from active runtime surfaces and
has an explicit Rancher Fleet dependency signature. Historical prose cannot
authorize a runtime dependency.

`scripts/compatibility-contract.py check` validates the machine contract,
regenerates its human documentation in memory, verifies declared live-matrix
commands, and rejects drift. Release and live qualification jobs must invoke it.
