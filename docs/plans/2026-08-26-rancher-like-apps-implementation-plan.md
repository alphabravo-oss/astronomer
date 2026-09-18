# Rancher-like Apps and Flux Delivery: Full Implementation Plan

- **Status:** In progress; feature construction and qualification are incomplete
- **Date:** 2026-08-26
- **Parent plan:** [Rancher-like Flux Delivery Expansion Plan](2026-08-26-rancher-like-flux-delivery-plan.md)
- **Execution style:** Fast vertical implementation first; comprehensive tests and qualification only after feature construction
- **Compatibility posture:** Greenfield; no legacy API, database, UI, or delivery-engine compatibility

## Objective

Build a complete Astronomer Apps/Catalog and multi-cluster delivery experience
using the existing Astronomer control plane, outbound agent, and local Flux
controllers. The result should provide Rancher Apps/Fleet-like usability without
installing Rancher Fleet or exposing native Flux as the product model.

The implementation includes:

- new catalog and examples repositories;
- catalog schema, signing, publication, synchronization, and trust;
- application, version, dependency, configuration, target, and rollout models;
- Git, Helm, OCI, and Kustomize application workflows;
- cluster groups, override layers, previews, staged rollout, and rollback;
- rich Apps/Catalog and system-component UI surfaces;
- agent/Flux materialization, observation, inventory, and drift reporting;
- RBAC, audit, secret handling, operational tooling, and documentation; and
- final automated, integration, end-to-end, scale, security, and failure testing.

## Execution rules

This plan deliberately separates construction from verification.

### During implementation

- Write complete vertical slices quickly.
- Do not stop each task to add unit tests, snapshots, fixtures, golden files, or
  broad regression tests.
- Do not run the full Go, frontend, Helm, integration, or end-to-end suites
  between tasks.
- Use only minimal developer checks needed to keep implementation moving:
  formatting, code generation, schema generation, compilation, starting the
  affected service, and one manual happy-path attempt.
- Record known defects and incomplete edge behavior in the implementation
  ledger rather than interrupting every slice for hardening.
- Keep destructive behavior restricted to disposable development clusters and
  test repositories.
- Commit at coherent vertical milestones so failed experiments remain easy to
  isolate or revert.

### Final hardening phase

- Freeze feature construction.
- Write all missing automated tests.
- Run the complete test and qualification matrix.
- Fix failures without expanding product scope.
- Repeat the full matrix until clean.
- Do not publish a production release before this phase passes.

This is “YOLO mode” for implementation speed, not permission to bypass secret
handling, authorization boundaries, immutable artifact resolution, or safe
destructive-action confirmation.

## Repository topology

### Existing `astronomer` repository

Owns the API, database, worker, agent protocol, Flux materializer, CLI, UI,
primary Helm chart, system inventory, and product documentation.

### New `astronomer-catalog` repository

Owns catalog schemas, entry metadata, supplemental configuration schemas,
recommended values, presentation assets, qualification definitions, generated
indexes, signatures, and catalog releases.

Initial layout:

```text
astronomer-catalog/
├── .github/workflows/
├── schemas/
│   ├── catalog-index-v1.schema.json
│   ├── catalog-entry-v1.schema.json
│   └── presentation-v1.schema.json
├── entries/
│   └── <application>/
│       ├── application.yaml
│       ├── versions/<version>.yaml
│       ├── schemas/values.schema.json
│       ├── values/recommended.yaml
│       └── assets/
├── policies/
├── scripts/
├── generated/
└── README.md
```

### New `astronomer-examples` repository

Owns documented and qualification-ready example applications.

Initial layout:

```text
astronomer-examples/
├── helm/basic-web/
├── helm/stateful-web/
├── kustomize/basic-web/
├── manifests/basic-web/
├── multi-environment/
├── dependencies/
└── README.md
```

### Deferred repository

Do not create `astronomer-system-bundles` initially. Create it only if system
artifact permissions, signing roots, or release cadence must diverge from
`astronomer-catalog`.

## Implementation ledger

Create a lightweight ledger at the start of execution containing:

- task identifier;
- implementation status;
- affected repository and files;
- manual happy-path result;
- known defects or deferred hardening;
- migrations or generated artifacts added; and
- final-test coverage still required.

The ledger is not an alternate backlog. Every unresolved entry must be fixed,
explicitly deferred by product decision, or converted into final test coverage
before release.

## Workstream 0: execution setup

- [x] Create an implementation branch in `astronomer`.
- [x] Create the implementation ledger.
- [x] Capture the current Delivery API, schema, route, and UI inventories.
- [x] Record current database migration and generated-client baselines.
- [x] Record current Flux distribution and agent protocol versions.
- [x] Identify the disposable local cluster used for implementation attempts.
- [x] Confirm GitHub authentication and organization repository permissions.
- [x] Reserve repository names and package namespaces.
- [x] Define milestone commit names and expected vertical demonstrations.
- [x] Freeze unrelated Delivery refactors during implementation.

**Milestone:** implementation workspace and external repository authority are
ready.

## Workstream 1: create supporting repositories

### `astronomer-catalog`

- [x] Create the GitHub repository with description, topics, visibility, and
  default branch.
- [x] Add branch protection and required-review configuration appropriate to
  the current development phase.
- [x] Add CODEOWNERS, license, security policy, contribution guide, and README.
- [x] Create the initial directory structure.
- [x] Add formatting and schema-generation commands.
- [x] Add OCI registry package naming conventions.
- [x] Add development and release version conventions.
- [x] Add placeholder signing/trust configuration without committing secrets.
- [x] Add CI workflow skeletons for validation, generation, publication, and
  release.
- [x] Add a machine-readable repository metadata file consumed by Astronomer.

### `astronomer-examples`

- [x] Create the GitHub repository with description, topics, visibility, and
  default branch.
- [x] Add CODEOWNERS, license, README, and contribution guidance.
- [x] Add a minimal Helm application.
- [x] Add a stateful Helm application with a PVC.
- [x] Add a minimal Kustomize application.
- [x] Add a bounded raw-manifest application.
- [x] Add a multi-environment override example.
- [x] Add a dependency example.
- [x] Add version tags that support immutable-resolution demonstrations.

**Manual try:** clone both repositories, generate an empty catalog index, and
resolve one tagged example artifact.

**Milestone:** external repositories exist and can provide development inputs.

## Workstream 2: catalog specification and tooling

### Versioned schemas

- [x] Define catalog index v1.
- [x] Define catalog entry v1.
- [x] Define catalog entry version v1.
- [x] Define presentation metadata v1.
- [x] Define configuration presentation annotations v1.
- [x] Define compatibility constraints.
- [x] Define prerequisite and conflict declarations.
- [x] Define dependency ownership and removal policy.
- [x] Define CPU, memory, replica, ephemeral-storage, and PVC estimates.
- [x] Define lifecycle capabilities: install, upgrade, rollback, and uninstall.
- [x] Define support tier, deprecation, replacement, and revocation states.
- [x] Define signature, provenance, SBOM, chart, artifact, and image identities.
- [x] Define post-install notes and safe dashboard links.
- [x] Define schema-version negotiation and unknown-field behavior.

### Catalog builder

- [x] Implement entry discovery.
- [x] Implement YAML/JSON parsing and canonicalization.
- [x] Implement deterministic index ordering.
- [ ] Implement content and asset digest generation.
- [x] Implement upstream Helm HTTP artifact resolution.
- [ ] Implement Helm OCI artifact resolution.
- [ ] Implement generic OCI artifact resolution.
- [ ] Implement immutable Git revision resolution.
- [x] Implement enabled-image inventory extraction.
- [x] Implement dependency graph construction.
- [x] Implement compatibility summary generation.
- [x] Implement release payload assembly.
- [x] Implement OCI index publication.
- [x] Implement optional static HTTPS mirror generation.
- [x] Implement signature-envelope generation and verification hooks.
- [x] Implement revocation metadata publication.

### Initial entries

- [x] Move or reproduce kube-state-metrics as a required catalog entry.
- [x] Move or reproduce prometheus-node-exporter as a required catalog entry.
- [x] Add one simple optional stateless application.
- [x] Add one optional stateful application.
- [ ] Add one application with a dependency.
- [x] Add one entry with a complete `values.schema.json` form experience.
- [x] Add one YAML-only entry to preserve expert-mode behavior.
- [x] Add icons, descriptions, maintainers, release notes, resource estimates,
  storage behavior, and lifecycle notes.

**Manual try:** publish a development OCI catalog, retrieve it by digest, and
inspect the generated index and entry payloads.

**Milestone:** a real catalog artifact exists independently of the product.

## Workstream 3: Astronomer database foundation

Create one greenfield schema migration rather than compatibility shims.

### Catalog persistence

- [ ] Add `delivery_catalogs`.
- [ ] Add catalog visibility, trust root, source, sync status, and verified index
  digest fields.
- [ ] Add `delivery_catalog_entries`.
- [ ] Add `delivery_catalog_entry_versions`.
- [ ] Add catalog categories, keywords, maintainers, assets, support tier,
  compatibility, prerequisites, estimates, and lifecycle capabilities.
- [ ] Add catalog synchronization events and error state.
- [ ] Add deprecation, replacement, revocation, and hidden states.

### Application persistence

- [ ] Add `delivery_applications`.
- [ ] Add `delivery_application_versions`.
- [ ] Link application versions to immutable source and catalog identities.
- [ ] Add application lifecycle and ownership state.
- [ ] Add installation notes and dashboard-link metadata.

### Targeting and configuration persistence

- [ ] Add `delivery_cluster_groups`.
- [ ] Add explicit membership and label-selector definitions.
- [ ] Add group generations and membership snapshots.
- [x] Add `delivery_override_sets`.
- [ ] Add organization, project, environment, group, cluster, and rollout scope.
- [x] Add deterministic precedence and conflict fields.
- [x] Add `delivery_configuration_templates`.
- [ ] Add redacted effective-configuration digest records.
- [ ] Add `delivery_application_dependencies` and consumer references.

### Observation persistence

- [ ] Add or extend `delivery_resource_inventory`.
- [ ] Add resource counts, readiness, revision, namespace, and bounded identity.
- [ ] Add `delivery_drift_events`.
- [ ] Add `delivery_system_components`.
- [ ] Add lifecycle owner, management method, version, resource posture, storage
  posture, health evidence, supported actions, and stale timestamps.

### Query layer

- [ ] Write SQL queries for every list/detail/mutation workflow.
- [ ] Add stable cursor pagination.
- [ ] Add server-side filtering and sorting fields.
- [ ] Add project and organization scoping to every query.
- [ ] Add transactional catalog synchronization upserts.
- [ ] Add immutable-version conflict handling.
- [ ] Add dependency reference-count queries.
- [ ] Generate the database access layer once the schema and queries stabilize.

**Manual try:** apply the migration to a disposable database and insert one
catalog, application, version, group, template, and system component.

**Milestone:** the product can persist the complete model.

## Workstream 4: catalog synchronization and trust service

- [ ] Add catalog source configuration.
- [x] Add OCI catalog retrieval by immutable digest.
- [x] Add HTTPS mirror retrieval with digest verification.
- [ ] Add organization trust-root configuration.
- [ ] Add index schema negotiation.
- [x] Add signature and digest verification.
- [ ] Add asset retrieval and safe caching.
- [ ] Add transactional catalog import.
- [ ] Add incremental synchronization using verified index identity.
- [ ] Add revocation handling.
- [ ] Add deprecation and replacement propagation.
- [ ] Add stale and unavailable catalog states without deleting last-known-good
  entries.
- [ ] Add bounded synchronization errors and operator remediation text.
- [ ] Add scheduled and manually triggered synchronization.
- [ ] Add synchronization task deduplication and locking.
- [ ] Add metrics and structured logs.

**Manual try:** register the development catalog, synchronize it, update the
index, synchronize again, and display both success and a deliberately invalid
signature error.

**Milestone:** Astronomer can safely consume external catalog releases.

## Workstream 5: source and artifact workflows

- [ ] Finish Git repository create, verify, update, rotate, revoke, and delete.
- [ ] Finish Helm HTTP repository workflows.
- [ ] Finish Helm OCI repository workflows.
- [ ] Finish generic OCI artifact workflows.
- [ ] Add credential references for token, basic, SSH, and workload identity
  modes supported by policy.
- [x] Add custom CA and trust-policy configuration.
- [ ] Resolve Git refs to commits.
- [x] Resolve Helm versions to exact archives and digests.
- [ ] Resolve OCI tags to manifests and digests.
- [x] Record immutable resolution evidence.
- [ ] Add bounded source discovery for charts, versions, and repository paths.
- [ ] Add source status, last verification, last resolution, and remediation.
- [ ] Add safe credential rotation without exposing plaintext.

**Manual try:** connect one source of each type and publish an immutable
application version from each.

**Milestone:** every promised source type produces an immutable version.

## Workstream 6: application and catalog APIs

### Catalog APIs

- [ ] List catalogs.
- [ ] Register an organization catalog.
- [ ] Update trust and synchronization policy.
- [ ] Synchronize a catalog.
- [ ] Remove or revoke a catalog.
- [ ] List and search entries.
- [ ] Get entry and version details.
- [ ] List categories, support tiers, maintainers, and compatibility facets.
- [ ] Evaluate entry compatibility against selected targets.

### Application APIs

- [ ] Create an application from a catalog entry.
- [ ] Create an application from a repository source.
- [ ] Publish an immutable application version.
- [ ] List and get applications.
- [ ] List versions and release notes.
- [ ] Deprecate or archive an application.
- [ ] Resolve dependencies.

### Configuration APIs

- [ ] Return chart defaults and schema.
- [ ] Return catalog-recommended values.
- [x] Create, update, list, and delete configuration templates.
- [x] Merge override layers deterministically.
- [x] Validate form or YAML values.
- [x] Return a redacted effective-values document and digest.
- [ ] Return a values and rendered-resource diff.
- [x] Prevent secret values from appearing in responses.

### Prerequisite APIs

- [ ] Evaluate Kubernetes and Flux compatibility.
- [ ] Discover required APIs and CRDs.
- [ ] Discover storage classes and expansion capabilities.
- [ ] Discover Gateway, ingress, CSI, operator, and dependency availability.
- [ ] Perform bounded server-side dry-run through the agent.
- [ ] Estimate resource and PVC demand by target.
- [ ] Classify blocking, approval-required, and advisory findings.
- [ ] Return explicit external prerequisites.

### Lifecycle APIs

- [ ] Preview installation.
- [ ] Start installation rollout.
- [ ] Preview upgrade.
- [ ] Start upgrade rollout.
- [ ] Preview rollback.
- [ ] Start rollback.
- [ ] Preview uninstall and retained data.
- [ ] Start fenced uninstall.
- [ ] Return installation notes and post-install links.

**Manual try:** drive one complete catalog installation using only API calls.

**Milestone:** the frontend has complete typed backend workflows.

## Workstream 7: cluster groups, placement, and rollout engine

- [ ] Implement explicit-member groups.
- [ ] Implement label-selector groups.
- [ ] Implement membership preview and explanation.
- [ ] Snapshot group membership at rollout creation.
- [x] Implement deterministic override precedence.
- [x] Detect conflicts before plan creation.
- [ ] Add all-at-once strategy.
- [ ] Add rolling strategy.
- [ ] Add canary strategy.
- [ ] Add named cohorts.
- [ ] Add maximum concurrent and maximum unavailable.
- [ ] Add minimum-ready soak.
- [ ] Add maintenance-window evaluation.
- [ ] Add manual approval gates.
- [ ] Add failure thresholds and pause-on-failure.
- [ ] Add resume, retry, abort, rollback, and removal transitions.
- [ ] Add dependency ordering and shared-dependency ownership.
- [ ] Add immutable frozen plans and placement explanations.
- [ ] Add rollout progress and actionable failure summaries.
- [ ] Repair rollout aggregate counters when terminal transitions occur.

**Manual try:** deploy an example to three clusters in canary plus later cohort,
pause it, resume it, and roll it back.

**Milestone:** multi-cluster application rollout is feature-complete.

## Workstream 8: agent protocol and Flux materialization

- [x] Version the assignment protocol for application identity, dependency,
  configuration digest, and inventory additions.
- [ ] Add bounded prerequisite-probe requests and responses.
- [ ] Add bounded server-side dry-run requests and responses.
- [x] Extend Helm materialization.
- [ ] Extend Git/Kustomize materialization.
- [ ] Extend OCI/Kustomize materialization.
- [ ] Add bounded raw-manifest materialization through Kustomize.
- [ ] Preserve deterministic names and Astronomer ownership labels.
- [ ] Preserve namespace-scoped service accounts by default.
- [ ] Add privileged-bundle approval enforcement for cluster-scoped resources.
- [ ] Add dependency ordering and health gates.
- [ ] Add suspend, retry, rollback, and fenced tombstone handling.
- [ ] Add resource inventory observation.
- [ ] Add normalized Flux Conditions and events.
- [ ] Add applied revision and resource-count reporting.
- [ ] Add drift observation and correction outcome.
- [ ] Add last-known-good behavior during tunnel outages.
- [ ] Add assignment garbage collection limited to exact ownership identity.
- [ ] Add payload size and rate limits.

**Manual try:** disconnect the management plane after assignment and confirm the
application remains reconciled; reconnect and confirm no duplicate resources.

**Milestone:** local Flux executes every application workflow safely.

## Workstream 9: system component inventory

- [x] Define detection adapters for Astronomer Helm components.
- [x] Detect Astronomer server, frontend, worker, Dex, CNPG resources, and
  Valkey resources.
- [x] Detect agent and Flux distribution versions and HA posture.
- [x] Detect CSI driver and default StorageClass.
- [x] Add Longhorn-specific capacity and replica observations when present.
- [x] Detect cert-manager and certificate readiness.
- [x] Detect Gateway API implementation and shared Gateway readiness.
- [x] Detect CNI and Kubernetes distribution metadata.
- [x] Represent external DNS and load balancer integrations without secret data.
- [x] Normalize owner, management method, health, version, requests/limits,
  replicas, PVCs, storage class, replication, and supported actions.
- [x] Mark observations stale independently of cluster connectivity.
- [x] Add links to workloads, namespaces, certificates, storage, logs, events,
  and runbooks.
- [x] Prevent actions for cluster- and externally owned components.

**Manual try:** inventory the current three-node k3s cluster and account for all
important components with correct lifecycle owners.

**Milestone:** the UI can be comprehensive without claiming Flux owns all
infrastructure.

## Workstream 10: RBAC, audit, and secret handling

- [ ] Add catalog read/admin permissions.
- [ ] Add application and version permissions.
- [ ] Add configuration-template permissions.
- [ ] Add cluster-group and placement-preview permissions.
- [ ] Add install, upgrade, rollback, suspend, retry, and uninstall permissions.
- [ ] Add privileged-bundle approval permission.
- [ ] Add system-component inventory and action permissions.
- [ ] Enforce organization, project, environment, group, and cluster scope.
- [ ] Add audit events for catalog trust changes and synchronization.
- [ ] Add audit events for source verification and credential rotation.
- [ ] Add audit events for application publication and configuration changes.
- [ ] Add audit events for preview, approval, rollout, rollback, and uninstall.
- [ ] Add audit events for secret projection and deletion without secret value.
- [x] Add write-only secret fields and external Secret references.
- [ ] Add redaction to logs, tasks, events, previews, support bundles, and errors.
- [ ] Add credential rotation and revocation propagation.
- [ ] Add destructive-action confirmation contracts for uninstall and retained
  data removal.

**Manual try:** use a restricted project user for a permitted install and verify
that a cross-project operation and privileged install are denied.

**Milestone:** the feature is bounded by Astronomer's security model.

## Workstream 11: generated clients and CLI

- [x] Update OpenAPI schemas and routes.
- [x] Regenerate Go clients.
- [x] Regenerate TypeScript clients and types.
- [ ] Add CLI catalog list/search/show/sync commands.
- [ ] Add CLI application create/list/show/version commands.
- [ ] Add CLI group create/preview/show commands.
- [ ] Add CLI configuration validate/effective/diff commands.
- [ ] Add CLI install/upgrade/rollback/uninstall preview commands.
- [ ] Add CLI rollout approve/start/pause/resume/retry/abort commands.
- [ ] Add CLI deployment resources/events/drift/history commands.
- [x] Add CLI system-component inventory command.
- [ ] Add structured JSON/YAML output and stable exit codes.
- [ ] Add secret-safe interactive input where required.

**Manual try:** perform the full application lifecycle using the CLI.

**Milestone:** every supported UI workflow has an automation path.

## Workstream 12: frontend foundation and navigation

- [ ] Replace the current Delivery landing information architecture.
- [ ] Add Overview, Applications, Sources, Targets, Catalog, and System
  Components navigation.
- [ ] Preserve one-open-section-at-a-time sidebar behavior.
- [ ] Add project and environment scope persistence.
- [ ] Add shared status, ownership, compatibility, support-tier, and severity
  badges.
- [ ] Add shared resource, storage, version, and rollout display primitives.
- [ ] Add shared empty, loading, stale, unavailable, and permission-denied states.
- [ ] Use TanStack Table for every sortable/filterable tabular surface.
- [ ] Add stable URL-backed filters, sorting, pagination, and selected targets.
- [ ] Add responsive card/table view switching where appropriate.
- [ ] Apply global typography, link, density, and interaction tokens.

**Manual try:** navigate every new top-level route with real API data and no
dead-end placeholder pages.

**Milestone:** the complete product skeleton is navigable.

## Workstream 13: Apps/Catalog UI

### Catalog discovery

- [ ] Build catalog source selector.
- [ ] Build search and category facets.
- [ ] Add support-tier, compatibility, installed, update, deprecated, storage,
  and privileged-resource filters.
- [ ] Build application card view.
- [ ] Build application TanStack table view.
- [x] Display installed versions and target counts.
- [x] Add favorites and recent entries.
- [x] Add catalog synchronization and trust status.

### Application details

- [ ] Display icon, screenshots, description, maintainers, links, and support.
- [ ] Display available versions and release notes.
- [ ] Display compatibility and lifecycle capabilities.
- [ ] Display dependencies, conflicts, resources, PVCs, and prerequisites.
- [ ] Display current installations and update availability.
- [ ] Display deprecation, replacement, and revocation banners.

### Installation wizard

- [ ] Add version selection.
- [ ] Add cluster, group, project, environment, and namespace targeting.
- [ ] Add release-name configuration where permitted.
- [ ] Generate forms from `values.schema.json`.
- [ ] Apply presentation annotations.
- [ ] Add expert YAML editor.
- [ ] Implement lossless form/YAML switching.
- [ ] Add organization/project/environment/group templates.
- [ ] Add write-only secret inputs and Secret references.
- [ ] Display layered values and precedence.
- [ ] Display prerequisite results per target.
- [ ] Display capacity, resource, replica, and PVC estimates.
- [ ] Display external DNS, certificate, load-balancer, and credential steps.
- [ ] Add rollout strategy and approval configuration.
- [ ] Add final redacted values and rendered-resource diff.
- [ ] Add immutable review digest and confirmation.

### Lifecycle workflows

- [ ] Build upgrade wizard and version diff.
- [ ] Highlight changed values, resources, permissions, dependencies, and PVCs.
- [ ] Build rollback preview and execution.
- [ ] Build uninstall preview.
- [ ] Add retained PVC, snapshot, backup, CRD, and shared-dependency decisions.
- [ ] Display installation notes and post-install links.

**Manual try:** install, upgrade, roll back, and uninstall the initial catalog
applications from the browser.

**Milestone:** Astronomer has a recognizable Apps-store experience.

## Workstream 14: Applications, targets, and rollout UI

- [x] Build application list with ready/desired clusters, revision, drift, age,
  source, and last actor.
- [x] Build application overview.
- [x] Build versions and release-history tab.
- [x] Build rollout timeline and cohort visualization.
- [x] Build per-cluster deployment table.
- [x] Build resource inventory with namespace and kind filters.
- [x] Build normalized events and failure explanations.
- [x] Build drift detail and remediation history.
- [x] Build values/patch precedence view.
- [x] Build audit-history tab.
- [x] Build cluster-group list and details.
- [x] Build selector and membership editor.
- [x] Build membership and placement explanation preview.
- [x] Build override template list and editor.
- [x] Build rollout approval, pause, resume, retry, abort, and rollback controls.
- [x] Prevent unsupported actions based on phase, RBAC, and ownership.

**Manual try:** operate the three-cluster rollout scenario entirely in the UI.

**Milestone:** multi-cluster day-two operations are usable without Flux tools.

## Workstream 15: System Components UI

- [x] Build owner and management-method summary.
- [x] Build system-component TanStack table.
- [x] Add filters for cluster, category, owner, method, health, and update state.
- [x] Add component detail page.
- [x] Display versions, replicas, requests/limits, HA, compatibility, and age.
- [x] Display PVC size, class, health, expansion, and replication.
- [x] Link to namespace, workloads, pods, storage, certificates, logs, and events.
- [x] Show supported actions only for Astronomer-owned components.
- [x] Explain disabled actions for cluster/external components.
- [x] Add stale and unavailable evidence states.

**Manual try:** verify Astronomer, Flux, agent, CNPG, Valkey, Longhorn,
cert-manager, Gateway, CNI, and external integration rows on the development
cluster.

**Milestone:** system visibility and delivery ownership are no longer conflated.

## Workstream 16: operations and release integration

- [x] Add catalog synchronization dashboards and alerts.
- [x] Add source-resolution dashboards and alerts.
- [x] Add rollout queue, latency, failure, pause, and rollback metrics.
- [x] Add agent materialization and observation metrics.
- [x] Add drift and stale-inventory metrics.
- [x] Add catalog, rollout, assignment, dependency, and system-inventory runbooks.
- [x] Add support-bundle catalog and delivery diagnostics.
- [x] Add catalog trust-root backup and restore coverage.
- [x] Add new database tables to management-plane backup/restore validation.
- [x] Add catalog and application release notes to product release generation.
- [x] Add air-gap import/export for catalog indexes, artifacts, and images.
- [x] Add feature configuration and safe disabled-state behavior until release.
- [x] Update the primary Helm chart with required configuration, resources,
  network policy, and migration behavior.
- [x] Update installation and upgrade documentation.

**Manual try:** inspect operational dashboards and perform one disposable
catalog outage and recovery.

**Milestone:** the feature can be operated and packaged.

## Workstream 17: final test implementation

Do not begin this workstream until Workstreams 0–16 are feature-complete and the
manual happy paths have been attempted.

### Unit tests

- [ ] Catalog schema and canonicalization tests.
- [ ] Digest, signature, trust, and revocation tests.
- [ ] Artifact-resolution tests.
- [ ] Dependency graph and removal-ownership tests.
- [x] Configuration merge, validation, redaction, and round-trip tests.
- [ ] Compatibility, prerequisite, capacity, and severity tests.
- [x] Placement, group snapshot, override precedence, and conflict tests.
- [x] Rollout state-machine and aggregate-counter tests.
- [x] Agent materialization, fencing, deletion, and observation tests.
- [x] RBAC, scope, audit, and secret-redaction tests.
- [x] System-component normalization and ownership tests.
- [x] Frontend hooks, forms, tables, filters, routes, and action-state tests.

### Contract and generation tests

- [x] Database migration and fresh-install contract tests.
- [x] SQL query and pagination contract tests.
- [x] OpenAPI validation and generated-client drift tests.
- [x] Agent protocol compatibility and payload-bound tests.
- [x] Catalog JSON Schema compatibility tests.
- [ ] CLI output and exit-code contract tests.
- [x] Helm schema, render, resource-bound, security-context, and image tests.
- [x] Air-gap artifact and image inventory tests.

### Integration tests

- [ ] Catalog publish, synchronize, update, revoke, and last-known-good tests.
- [ ] Git, Helm HTTP, Helm OCI, and OCI source tests.
- [ ] Application publication and immutable-resolution tests.
- [ ] Form/YAML/template/effective-values tests.
- [ ] Prerequisite and server-side dry-run tests.
- [ ] Single- and multi-cluster rollout tests.
- [ ] Canary, cohort, approval, maintenance-window, and failure-budget tests.
- [ ] Pause, retry, resume, abort, rollback, and uninstall tests.
- [ ] Dependency install, shared use, upgrade, and safe removal tests.
- [ ] Resource inventory, event, drift, and correction tests.
- [ ] Management-plane disconnect and reconnect tests.
- [ ] Credential rotation and revocation tests.

### Browser end-to-end tests

- [ ] Catalog discovery and filtering.
- [ ] Application details and version selection.
- [ ] Schema form and YAML round-trip.
- [ ] Target selection and placement explanation.
- [ ] Prerequisite/capacity review.
- [ ] Install, monitor, upgrade, roll back, and uninstall.
- [ ] Application resource, event, drift, and history views.
- [ ] Cluster groups and configuration templates.
- [ ] Organization catalog administration.
- [ ] System-component ownership and disabled actions.
- [ ] RBAC-denied and stale/unavailable states.
- [ ] Keyboard, focus, screen-reader, and responsive behavior.

### Security tests

- [ ] Cross-project and cross-namespace isolation.
- [ ] Privileged-bundle enforcement.
- [ ] Secret leakage across every response and diagnostic path.
- [ ] Tampered catalog, artifact, schema, asset, and signature rejection.
- [ ] SSRF, path traversal, archive traversal, oversized artifact, and parser
  abuse resistance.
- [ ] Malicious Helm values and rendered-resource policy enforcement.
- [ ] Stale generation, replay, duplicate, and forged-cluster rejection.
- [ ] Destructive uninstall and retained-data confirmation.

### Scale and failure tests

- [ ] Hundreds of clusters and thousands of assignments.
- [ ] Large catalog indexes and high-cardinality search/filter traffic.
- [ ] Rollout queue pressure and worker restart.
- [ ] Source outage, registry outage, and catalog outage.
- [ ] Flux controller restart and node loss.
- [ ] Agent disconnect, reconnect, and version skew.
- [ ] Management-plane database and Valkey failover.
- [ ] Drift storm and event-pressure behavior.
- [ ] Backup, restore, and disaster-recovery rehearsal.

**Milestone:** all required automated coverage exists.

## Workstream 18: final qualification and remediation

- [ ] Freeze feature scope.
- [x] Run formatting and code-generation checks.
- [x] Run all backend unit and integration tests.
- [ ] Run all frontend unit and browser tests.
- [x] Run all Helm and release-contract tests.
- [ ] Run catalog repository validation and qualification.
- [x] Run examples repository qualification.
- [ ] Run security and secret-leak checks.
- [ ] Run air-gap qualification.
- [ ] Run fresh-install qualification.
- [ ] Run upgrade, rollback, and uninstall qualification.
- [ ] Run multi-cluster and management-plane-outage qualification.
- [ ] Run scale and failure-injection qualification.
- [ ] Fix every release-blocking defect.
- [ ] Repeat the entire matrix from a clean environment.
- [ ] Generate final compatibility, image, artifact, SBOM, and signature records.
- [ ] Review documentation, runbooks, and support bundles.
- [ ] Perform final product, security, SRE, and release sign-off.

**Milestone:** the feature is releasable.

## Suggested milestone commits

1. `catalog repositories and v1 format`
2. `catalog persistence and synchronization`
3. `application source and configuration APIs`
4. `groups placement and rollout engine`
5. `agent flux application execution`
6. `system component inventory`
7. `apps catalog frontend`
8. `application and rollout frontend`
9. `operations cli and release integration`
10. `complete apps and delivery test matrix`
11. `qualification remediation and release evidence`

Do not squash these milestones during active development; they provide useful
recovery points. Final repository history policy can be decided at release.

## Definition of feature-complete before tests

Workstreams 0–16 are feature-complete when:

- all promised repositories and runtime components exist;
- all database, API, protocol, CLI, and UI paths are implemented;
- all top-level pages operate against real data rather than fixtures;
- one manual happy path has been attempted for each workstream;
- install, upgrade, rollback, and uninstall work for the initial applications;
- the current three-node development cluster can be targeted;
- system components show correct lifecycle ownership;
- known defects and missing coverage are recorded in the ledger; and
- no production release or production migration has occurred.

## Definition of done

The implementation is done when:

- Workstreams 0–18 are complete;
- the final matrix passes from clean environments;
- the catalog and examples repositories publish immutable signed releases;
- Astronomer installs and operates catalog and repository applications without
  product-specific chart changes;
- multi-cluster rollout, rollback, drift, disconnect, and recovery behavior is
  qualified;
- every important system component has accurate ownership and health;
- no secret, scope, or destructive-lifecycle release blocker remains;
- online and air-gapped installation paths are documented and demonstrated;
- release evidence and compatibility contracts are generated; and
- the parent plan's acceptance scenarios and measures of success are satisfied.
