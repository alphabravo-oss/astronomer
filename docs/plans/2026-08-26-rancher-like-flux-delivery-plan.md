# Rancher-like Flux Delivery Expansion Plan

- **Status:** Proposed for later implementation
- **Date:** 2026-08-26
- **Owners:** Product, platform engineering, security, SRE, and release engineering
- **Depends on:** [Flux-native multi-cluster delivery](../architecture/decisions/flux-native-delivery.md)
- **Comparative reference:** Rancher Fleet behavior and UX, without importing or exposing Rancher Fleet

## Executive summary

Astronomer currently installs a deliberately small Flux-managed platform
baseline: `kube-state-metrics` and `prometheus-node-exporter`. This proves the
end-to-end source, bundle, target, rollout, assignment, materialization, drift,
and health path, but it does not yet provide the breadth of a Rancher/Fleet-like
delivery product.

The future state should make Astronomer Delivery the normal place to:

1. connect Git, OCI, and Helm sources;
2. package one or more deployable units from those sources;
3. target clusters and cluster groups with explicit overrides;
4. preview, approve, stage, pause, retry, roll back, and remove rollouts;
5. observe readiness, drift, revisions, resources, events, and history; and
6. distinguish Flux-managed applications from bootstrap-owned or externally
   owned system components.

Flux remains an implementation detail. Astronomer will not install Rancher
Fleet, expose `fleet.cattle.io` resources, accept arbitrary Flux objects, or
make downstream kubeconfigs part of the management-plane data model.

## Desired operator experience

An administrator opening **Delivery** should see more than the two mandatory
metrics components. The landing page should answer:

- What is installed, where, and why?
- Who owns its lifecycle?
- What source and immutable revision produced it?
- How many target clusters are ready, progressing, drifted, or failed?
- What changed in the last rollout?
- Can Astronomer safely retry, suspend, roll back, or remove it?
- Which system components are visible but intentionally not Flux-managed?

The primary hierarchy should be:

```text
Delivery
├── Overview
├── Applications
│   ├── application
│   ├── releases and rollout history
│   └── per-cluster deployments and resources
├── Sources
│   ├── Git repositories
│   ├── OCI repositories
│   └── Helm repositories
├── Targets
│   ├── clusters
│   ├── cluster groups
│   └── placement and overrides
├── Catalog
│   ├── Astronomer built-ins
│   ├── approved organization entries
│   └── optional platform add-ons
└── System components
    ├── Astronomer Helm
    ├── Flux distribution and agent
    ├── CNPG and Valkey
    └── externally owned infrastructure
```

This hierarchy uses Astronomer terminology. Native Flux object names may be
shown in an advanced diagnostic panel, but they are not the primary product
model.

## Ownership model

Every displayed component must have exactly one lifecycle owner. Visibility
must never imply that Astronomer can mutate an externally owned component.

| Owner | Examples | Installation and upgrade path | Delivery actions |
| --- | --- | --- | --- |
| `astronomer-helm` | Astronomer server, frontend, workers, Dex, CNPG database resources, Valkey | Explicit Astronomer Helm install/upgrade | View health and version; link to Helm/runbook actions |
| `astronomer-system` | Cluster agent and pinned Flux controller distribution | Signed, cohort-based system assignment | Upgrade, pause, retry, rollback under system policy |
| `flux` | Built-ins, catalog add-ons, and user applications | Astronomer assignment materialized as local Flux resources | Full rollout, suspend, retry, rollback, drift, and delete lifecycle |
| `cluster` | Longhorn, cert-manager, Gateway API implementation, CNI, CSI | Cluster administrator | Read-only inventory unless explicitly adopted later |
| `external` | Cloud load balancers, DNS, external databases, external secret stores | External provider/operator | Read-only integration status and documentation |

Astronomer must not use Flux to manage the Helm release that installs
Astronomer itself. Flux and the agent must not self-mutate through ordinary
workload assignments. Those boundaries prevent bootstrap cycles and preserve a
known recovery path.

## Scope

### Application delivery

- Create authenticated Git, OCI, Helm HTTP, and Helm OCI sources.
- Resolve mutable user input to immutable commits, digests, or chart artifacts
  before approval.
- Support Helm charts, Kustomize directories, and bounded raw-manifest bundles.
- Represent an application independently from its versions and rollouts.
- Allow one application version to target multiple clusters or cluster groups.
- Support namespace, values, patches, service account, and policy overrides at
  organization, project, environment, group, and cluster scopes.
- Produce a deterministic effective-configuration preview before rollout.
- Retain immutable rollout plans, approval decisions, actor identity, and audit
  history.

### Placement and cluster groups

- Introduce durable cluster groups with label selectors and explicit members.
- Snapshot resolved membership when a rollout starts; membership changes must
  not silently alter an active rollout.
- Detect conflicting group membership or overrides before release.
- Support all-at-once, rolling, canary, and cohort strategies.
- Support maximum unavailable, maximum concurrent, minimum-ready soak,
  maintenance windows, failure thresholds, and manual approval gates.
- Show why each cluster was selected and the complete override precedence used.

### Catalog

Catalog entries should be signed product metadata that create ordinary sources,
bundles, targets, and rollouts. Catalog installation must not introduce a
second deployment path.

Catalog classes:

- **Required baseline:** only components necessary for supported Astronomer
  functionality on every adopted cluster.
- **Recommended:** supported but opt-in platform capabilities.
- **Optional:** reviewed integrations with explicit prerequisites and ownership.
- **Organization:** administrator-published internal applications.

Potential recommended or optional entries include metrics-server, Prometheus,
Grafana, Loki, Fluent Bit, Trivy Operator, Gatekeeper, cert-manager, and ingress
controllers. Inclusion is not automatic: every entry must pass artifact,
license, security, air-gap, Kubernetes-version, resource, upgrade, and uninstall
qualification.

The required baseline should remain small. A richer catalog is not a reason to
install every add-on on every cluster.

### Apps and catalog experience

The Catalog should provide a Rancher Apps-like discovery and installation
experience while retaining Astronomer's immutable, multi-cluster rollout model.
It must be more than a table of internal bundles.

The normal install flow should be:

```text
Catalog -> Application -> Version -> Targets -> Configuration
        -> Prerequisites and capacity -> Review rollout -> Install
        -> Per-cluster convergence, resources, drift, and history
```

#### Catalog types

- **Astronomer Catalog:** release-qualified entries maintained by Astronomer.
- **Organization Catalog:** private entries approved and published by an
  organization administrator.
- **Repository Applications:** applications discovered from connected Git,
  Helm HTTP, Helm OCI, or OCI artifact sources without promotion into a shared
  catalog.

Catalogs are source indexes, not deployment engines. Selecting an entry creates
the same immutable application version, target, rollout, and Flux assignment as
any other Astronomer deployment.

#### Catalog entry contract

Each versioned entry should carry signed, schema-validated metadata for:

- stable application identifier, display name, summary, description, category,
  keywords, maintainers, home page, source URL, and documentation URL;
- icon and optional screenshots with digest-pinned assets and accessible alt
  text;
- support tier, lifecycle state, deprecation or replacement notice, and support
  contact;
- chart or artifact coordinates, exact version, archive digest, provenance,
  signatures, SBOM references, and enabled workload image digests;
- supported Kubernetes versions, architectures, distributions, and required
  Kubernetes APIs;
- required and conflicting applications, CRDs, operators, storage classes,
  ingress/Gateway capabilities, and external services;
- default namespace, permitted namespace behavior, release-name constraints,
  and cluster-scoped resource requirements;
- resource estimates for minimum and recommended CPU, memory, replicas, and
  ephemeral storage;
- PVC count, access mode, minimum/default/recommended size, expansion support,
  backup expectations, and data-retention behavior on uninstall;
- supported installation, upgrade, rollback, and uninstall paths;
- pre-install, post-install, health, and removal notes; and
- links from installed resources to relevant Astronomer dashboards.

The metadata format should be versioned independently from product releases and
have a JSON Schema, deterministic canonical representation, content digest, and
signature. Unknown required fields fail closed; optional future fields remain
forward-compatible under explicit schema rules.

#### Configuration framework

- Generate safe forms from Helm `values.schema.json` when available.
- Add Astronomer presentation annotations for field groups, order, labels,
  descriptions, help links, units, sensitive values, advanced fields, and
  conditional visibility.
- Preserve an expert YAML editor with schema validation and a rendered diff;
  form and YAML modes must round-trip without silently discarding values.
- Display chart defaults, catalog recommendations, organization templates, and
  user overrides as separate layers with deterministic precedence.
- Support reusable, versioned configuration templates scoped to organization,
  project, environment, or cluster group.
- Represent secrets as references or write-only inputs. Never place secret
  plaintext in saved templates, previews, URLs, browser persistence, audit
  detail, or API responses.
- Validate quantities, enum constraints, immutable fields, namespace rules,
  storage reductions, and unsupported combinations before rollout.
- Render a final redacted effective-values preview and digest for approval.

Charts without `values.schema.json` remain installable in YAML mode, but they do
not receive a generated form until the catalog supplies a reviewed supplemental
schema. Astronomer must not guess field types from default values.

#### Prerequisite and capacity review

Before installation, Astronomer should evaluate every selected target and show:

- Kubernetes and Flux compatibility;
- missing APIs, CRDs, operators, storage classes, CSI expansion, Gateway or
  ingress support, and required dependencies;
- namespace or cluster-scoped RBAC requirements;
- admission-policy conflicts detected by server-side dry-run where supported;
- requested CPU, memory, replicas, ephemeral storage, PVC capacity, and likely
  schedulability against allocatable target capacity;
- high-availability posture and topology limitations;
- external DNS, certificate, load-balancer, object-storage, or credential steps;
  and
- whether each result blocks installation, requires approval, or is advisory.

Checks are evidence, not promises. Capacity estimates must state their basis and
must not claim successful scheduling merely because aggregate free capacity is
sufficient.

#### Install, upgrade, and uninstall behavior

- Installation defaults to a reviewed rollout strategy appropriate to the
  target count and support tier.
- Version selection shows release notes, compatibility changes, deprecated
  values, resource/storage changes, and known irreversible operations.
- Upgrade preview compares current and desired configuration and resources,
  including newly introduced cluster-scoped objects and PVC changes.
- Rollback is offered only when the catalog version declares it safe and the
  retained immutable artifact is available.
- Uninstall previews resources to be removed and separately asks how retained
  PVCs, snapshots, backups, CRDs, and shared dependencies will be handled.
- Shared dependencies use reference-aware ownership. Removing one application
  must not remove a dependency still used by another application.
- Installation notes and post-install links are stored with the immutable
  application version and rendered without executing arbitrary content.

#### Discovery and presentation

- Search by name, keyword, category, maintainer, support tier, source, and
  compatibility.
- Filter by installable on selected targets, installed, update available,
  deprecated, storage requirements, and cluster-scoped privilege.
- Offer card and TanStack table views backed by the same server-side query
  contract.
- Show installed versions and target counts directly in catalog results.
- Provide favorites and recently used entries as user preferences, not catalog
  mutations.
- Never use popularity or “recommended” labels without an explicit, auditable
  ranking policy.

#### Catalog repository and publication model

Start with one separately releasable `astronomer-catalog` repository rather
than one repository per application. It should contain entry metadata,
supplemental schemas, recommended values, tests, and asset references while
reusing pinned upstream Helm/OCI artifacts whenever practical.

An `astronomer-examples` repository may hold documentation and end-to-end Git,
Helm, and Kustomize examples. Create a separate system-bundles repository only
if system artifact access controls or release cadence demonstrably diverge from
the application catalog.

Publication should:

1. validate metadata and schemas;
2. fetch and verify immutable upstream artifacts;
3. enumerate enabled images and dependencies;
4. run render, policy, install, upgrade, rollback, and uninstall qualification;
5. generate air-gap inventories, SBOM associations, and compatibility results;
6. sign the entry index and immutable version payloads; and
7. publish an OCI-backed catalog index with an optional static HTTPS mirror.

Organization catalogs use the same format and validation rules but have their
own trust roots, visibility, support labels, and publishing permissions.

### System component inventory

Add a unified read model for important components that are not ordinary Flux
deployments. Each row should include:

- component, category, owner, namespace, and cluster;
- installed and desired version;
- health and readiness evidence;
- management method (`Helm`, `Flux`, `Operator`, `Cluster`, or `External`);
- update availability and compatibility;
- resource requests/limits and replica posture;
- persistent storage class, requested capacity, health, and replication;
- last observed time and stale state; and
- supported actions and the reason unavailable actions are disabled.

This makes Longhorn, cert-manager, CNPG, Valkey, Gateway API, Flux, the agent,
and Astronomer visible without falsely claiming Flux ownership.

## Data model changes

Preserve PostgreSQL as the authoritative product record and Flux as downstream
reconciliation state.

Add or extend these concepts:

- `delivery_applications`: stable application identity, project, description,
  ownership, labels, and lifecycle state.
- `delivery_application_versions`: immutable bundle-version composition and
  release notes.
- `delivery_cluster_groups`: selector, explicit membership, generation, and
  project boundary.
- `delivery_override_sets`: typed values/patches with scope, precedence,
  schema, secret references, and digest.
- `delivery_catalog_entries`: catalog class, support tier, prerequisites,
  compatibility, artifact identity, and deprecation state.
- `delivery_catalogs`: source, trust root, visibility, synchronization state,
  schema version, and last verified index digest.
- `delivery_catalog_entry_versions`: immutable presentation metadata,
  configuration schema, artifact identities, release notes, compatibility,
  resource/storage estimates, lifecycle capabilities, and signature evidence.
- `delivery_configuration_templates`: scoped, versioned, non-secret override
  layers with schema and content digests.
- `delivery_application_dependencies`: resolved dependency identity, ownership,
  compatible version range, consumers, and removal policy.
- `delivery_system_components`: normalized observations for non-workload
  lifecycle owners.
- `delivery_resource_inventory`: bounded resource summaries tied to assignment
  generation and Flux inventory revision.
- `delivery_drift_events`: normalized, deduplicated drift episodes and
  remediation outcome.

Do not store arbitrary Kubernetes objects, secret plaintext, unbounded Flux
status, or full rendered manifests in routine list records. Store immutable
digests and bounded/redacted diagnostic evidence; use authorized on-demand
inspection for detailed resources.

## API and CLI plan

Provide typed APIs and matching CLI commands for:

- application CRUD and version publication;
- source verification and immutable resolution;
- catalog browsing, prerequisite checks, install, upgrade, and uninstall;
- cluster-group preview and membership explanation;
- effective values/patch preview with secret values redacted;
- rollout plan, approval, start, pause, resume, retry, abort, rollback, and
  delete;
- deployment status, resource inventory, events, drift, and history;
- system-component inventory and ownership-aware actions; and
- exportable diagnostics that do not expose credentials.

All mutating APIs require project-scoped RBAC, idempotency keys, audit events,
generation fencing, and optimistic concurrency. List APIs require stable
pagination, filtering, and sorting contracts suitable for TanStack tables.

## UI plan

### Overview

- Fleet-wide cards for managed clusters, applications, active rollouts,
  failures, drift, stale inventory, and incompatible controllers.
- A prioritized attention queue with actionable reasons rather than a generic
  `Degraded` label.
- Recent release and rollback activity.
- Filters for project, environment, cluster group, cluster, namespace, owner,
  source type, phase, and drift.

### Applications

- One row per application, not one row per internal Flux resource.
- Expandable cluster and environment convergence summary.
- Current and desired revisions, ready/desired cluster counts, drift, age, and
  last actor.
- Details for versions, rollout history, cluster deployments, resources,
  values/patch precedence, events, and audit history.

### Sources and catalog

- Connection verification, credential status, artifact resolution, signature
  policy, last successful fetch, and actionable failure details.
- Catalog cards and table view with support tier, prerequisites, compatibility,
  resource estimate, storage needs, and installed targets.
- Install wizard that previews placement, namespaces, resources, storage,
  effective values, and rollout strategy before mutation.
- Application detail pages with icons, screenshots, documentation, maintainers,
  available versions, release notes, compatibility, dependencies, and current
  installations.
- Schema-generated form and expert YAML configuration modes with reliable
  round-tripping, inline validation, layered-value explanation, and a final
  redacted effective-values diff.
- Target-by-target prerequisite and capacity results with blocking, approval,
  and advisory severity.
- Ownership-aware upgrade, rollback, and uninstall previews, including explicit
  handling for retained data and shared dependencies.

### System components

- Separate ownership-aware view; do not mix bootstrap components into ordinary
  application rollout counts.
- Direct links to relevant workload, namespace, storage, certificate, logs,
  events, and runbook views.
- Clearly label read-only externally managed components.

## Agent and Flux execution changes

- Keep the closed assignment schema and deterministic object naming.
- Add bounded support for multi-resource application inventory.
- Report Flux revision, Conditions, applied resource counts, health summaries,
  drift, and sanitized failure evidence.
- Support server-side dry-run/preflight where downstream APIs are required.
- Preserve the last accepted generation during management-plane outages.
- Require explicit fenced tombstones for deletion.
- Garbage-collect only resources carrying the exact Astronomer assignment
  identity and generation.
- Maintain namespace-scoped service accounts by default; require a separately
  approved privileged policy for CRDs, ClusterRoles, webhooks, operators, or
  other cluster-scoped resources.

Do not add Flux notification or image-automation controllers merely to mimic
Fleet. Add them only if a separately approved product capability requires them.

## Security and supply-chain requirements

- Pin source revisions and workload images to immutable identities at approval.
- Verify catalog chart hashes, image digests, signatures, provenance, licenses,
  and SBOMs in release CI.
- Encrypt source credentials and project only namespace-scoped Secrets.
- Redact secret values from previews, diffs, events, logs, support bundles, and
  audit records.
- Reject cross-namespace references, remote Kustomize bases, unbounded payloads,
  and unknown assignment fields.
- Require explicit policy and approval for cluster-scoped resources.
- Enforce resource requests/limits, security contexts, network policies,
  topology posture, PDB expectations, and PVC sizing checks for supported
  catalog entries.
- Preserve air-gap image and artifact inventories for every built-in entry.

## Delivery phases

### Phase 0: contracts and terminology

- Finalize application, catalog, target, cluster-group, and system-component
  terminology.
- Add architecture diagrams and ownership rules to the accepted ADR.
- Define API schemas, state machines, error codes, audit events, and RBAC.
- Add scale targets and compatibility limits.

**Exit criteria:** approved product/API design with no Fleet API dependency and
no ambiguity between visibility and lifecycle ownership.

### Phase 1: honest system inventory

- Build the system-component observation model and UI.
- Inventory Astronomer Helm, agent, Flux, CNPG, Valkey, CSI, cert-manager,
  Gateway API, and selected external dependencies.
- Add explicit owner and available-action fields.

**Exit criteria:** an operator can account for every important platform
component without assuming all rows are Flux-managed.

### Phase 2: application-centered delivery

- Add stable applications and application versions over the existing bundle
  primitives.
- Add Git/OCI/Helm onboarding and immutable resolution UX.
- Add application details, rollout history, resource inventory, events, and
  drift views.

**Exit criteria:** a user can deploy and operate a normal Helm or Kustomize
application without understanding native Flux resources.

### Phase 3: groups, overrides, and staged rollout

- Implement cluster groups, placement explanation, typed overrides, preview,
  canaries, cohorts, maintenance windows, approvals, and rollback.
- Add concurrency and failure-budget enforcement at scale.

**Exit criteria:** multi-cluster deployment behavior reaches the useful Fleet
reference points while retaining Astronomer's own API and policy model.

### Phase 4: supported catalog expansion

- Define the versioned catalog-entry schema and create the separately releasable
  `astronomer-catalog` repository.
- Build OCI/static index synchronization, signature verification, trust-root
  management, and organization-catalog registration.
- Implement rich discovery, application details, schema-driven forms, YAML
  round-tripping, saved templates, prerequisite/capacity review, and lifecycle
  previews.
- Create catalog packaging and release qualification automation.
- Promote reviewed optional tools one at a time.
- Add organization catalog publishing and deprecation workflows.

**Exit criteria:** supported add-ons use the same delivery path as user
applications; an administrator can publish an organization catalog; and entries
pass online, air-gap, configuration, upgrade, rollback, and uninstall tests.

### Phase 5: scale and day-two hardening

- Exercise thousands of assignments and hundreds of clusters.
- Add queue pressure, stale-state, drift storm, and controller-outage testing.
- Complete backup/restore, management-plane outage, cluster disconnect,
  credential rotation, and disaster-recovery qualification.

**Exit criteria:** published SLOs and tested recovery behavior meet the release
scale contract.

## Acceptance scenarios

The program is complete only when automated qualification demonstrates:

1. A fresh local install shows its two required baseline deployments plus an
   ownership-correct system inventory.
2. A user connects Git, Helm HTTP, Helm OCI, and OCI artifact sources and sees
   immutable resolved revisions.
3. One application deploys to one cluster with no product-specific chart
   modification.
4. One application deploys to multiple groups with deterministic overrides and
   an explainable placement snapshot.
5. A canary failure pauses later cohorts and exposes an actionable cause.
6. Retry and rollback converge without duplicate resources or leaked secrets.
7. Drift is detected, displayed, and corrected according to declared policy.
8. Flux continues reconciling during a management-plane outage.
9. Reconnection does not delete or duplicate the last accepted assignment.
10. Cluster-scoped content is rejected without the privileged bundle policy.
11. Catalog installs and removals pass cleanly online and air-gapped.
12. System inventory never offers mutation for cluster- or externally owned
    components.
13. List pages retain stable server-side filtering, pagination, and sorting at
    the published scale target.
14. Audit history attributes every source, approval, rollout, rollback, secret
    projection, and destructive action.
15. A catalog entry with `values.schema.json` produces a usable form whose YAML
    and form modes round-trip without loss.
16. A chart without a schema remains installable through validated YAML and is
    never given guessed form semantics.
17. Catalog search and compatibility filters exclude or explain entries that
    cannot install on the selected targets.
18. Installation preview identifies missing APIs, storage classes, policy
    conflicts, resource estimates, PVC behavior, and external prerequisites.
19. Upgrade preview exposes value, resource, privilege, dependency, and storage
    changes before approval.
20. Uninstall cannot remove shared dependencies or retained data without an
    explicit ownership-aware decision.
21. Tampered indexes, metadata, schemas, assets, charts, or OCI artifacts fail
    signature/digest verification and cannot be released.
22. An organization administrator can publish, deprecate, and revoke a private
    entry without altering the Astronomer Catalog.

## Non-goals

- Rancher Fleet installation, API compatibility, CRDs, naming, or migration.
- A generic Flux dashboard or direct user mutation of Flux resources.
- Kubernetes cluster provisioning.
- Management-plane storage of general-purpose downstream kubeconfigs.
- Automatic adoption or mutation of arbitrary existing Helm releases.
- Installing every catalog entry by default.
- Flux self-management of the Astronomer bootstrap release.
- Moving authoritative product history from PostgreSQL into Kubernetes.

## Measures of success

- At least 95% of supported delivery failures present an actionable normalized
  reason without requiring direct Flux inspection.
- Every displayed component has an unambiguous lifecycle owner.
- Every rollout can be traced from actor and source revision to target snapshot,
  assignment generation, live resources, and final outcome.
- Management-plane loss does not interrupt convergence of released intent.
- No catalog entry can ship without immutable artifact and image identities,
  resource bounds, security review, air-gap inventory, and lifecycle tests.
- The required baseline remains minimal while the optional catalog can grow
  independently.
- At least 90% of qualified catalog chart settings intended for routine use are
  configurable through reviewed schema-driven forms; every setting remains
  available through expert YAML mode.
- Catalog install, upgrade, rollback, and uninstall previews identify all known
  privilege, dependency, and persistent-data changes before mutation.

## Reference behavior

Rancher Fleet is comparative input only. Its useful reference behaviors are the
local cluster as a normal target, bundle-oriented delivery, cluster/group
targeting, downstream reconciliation, aggregated readiness, and continued
operation through an agent. Astronomer intentionally differs by retaining its
own application, policy, approval, audit, security, and UX contracts.

- [Fleet architecture](https://fleet.rancher.io/explanations/architecture)
- [Fleet bundle lifecycle](https://fleet.rancher.io/ref-bundle-stages)
- [Fleet deployed resources](https://fleet.rancher.io/reference/ref-resources)
- [Fleet custom resource reference](https://fleet.rancher.io/reference/ref-crds)
