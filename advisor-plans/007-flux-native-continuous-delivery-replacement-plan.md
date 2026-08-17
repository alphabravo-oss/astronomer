# Plan 007: Replace Argo and the legacy fleet-operation engine with Astronomer-owned, Flux-native continuous delivery

> **Executor instructions**: This is the authoritative end-to-end program plan,
> not an MVP outline. Read it completely before changing code. Execute the waves
> in order, keep every public contract and generated artifact in the same commit
> as its implementation, and run every verification gate. A wave is complete
> only when its explicit exit criteria pass. Do not preserve an old behavior just
> because code already exists: this plan deliberately makes a greenfield break
> from the Argo data model and the imperative `fleet_operations` model.
>
> **Unambiguous dependency decision**: use Flux. Do **not** install, import,
> vendor, call, wrap, or expose Rancher Fleet (`fleet.cattle.io`) anywhere.
> “Fleet” below is used only in quoted legacy names or to describe a collection
> of clusters in ordinary English. New product and code names use `delivery`,
> `deployment`, `rollout`, or `cluster agents` so users cannot confuse this
> implementation with Rancher Fleet.
>
> **Drift checks (run first)**:
>
> ```bash
> cd /root/astronomer-all/astronomer
> git diff --stat 564ce9a..HEAD -- cmd deploy docs frontend internal pkg scripts Makefile go.mod go.sum
> cd /root/astronomer-all/charlie
> git diff --stat aaca50b..HEAD -- cmd docs frontend internal agent db
> ```
>
> If an in-scope file changed, compare the live code with “Current state.” If a
> schema, protocol, route, release convention, or security boundary no longer
> matches, stop and have this plan revised. Do not blindly apply stale line
> numbers or migration numbers.

## Status

- **Priority**: P0
- **Effort**: L (multi-team, multi-release program; not a single PR)
- **Risk**: HIGH — replaces the deployment engine, downstream bootstrap, public APIs, UI, CLI, database schema, and Charlie contract
- **Depends on**: none; this supersedes Argo-specific delivery work in Plans 002 and 003 for the next major release, but it does not make the currently shipped v0.3.x Argo path safe
- **Category**: direction / migration / security / tech-debt
- **Planned at**: Astronomer commit `564ce9a` (`v0.3.9`) and Charlie commit `aaca50b` (`v1.0.62`), 2026-08-17
- **Target release**: Astronomer `v1.0.0`; Charlie companion release cut from the exact contract revision qualified with that Astronomer build

## Why this matters

Astronomer currently has two overlapping multi-cluster mechanisms: a large,
central Argo integration that models ApplicationSets and proxies the Argo UI,
and a custom imperative `fleet_operations` fan-out engine. Both are expensive
to reason about, both duplicate product concepts, and neither gives the exact
Rancher-like operating model requested: an outbound agent, local reconciliation
that survives control-plane outages, centrally controlled placement and rollout,
and a single first-party user experience.

The replacement keeps the valuable topology Astronomer already owns—the
authenticated outbound agent tunnel, projects, cluster groups, audit, RBAC,
operations, SSE, and Charlie approval system—and makes Flux an implementation
detail on each managed cluster. Astronomer owns user intent and policy. Flux
owns source acquisition plus Helm/Kustomize convergence. No generic Flux UI and
no Rancher Fleet API are introduced.

## Outcome and product contract

After this plan is complete:

1. A new user installs Astronomer from one tagged Helm release, registers a
   cluster with one generated command, and receives the pinned Flux controllers
   needed by Astronomer without separately learning or bootstrapping Flux.
2. Users define reusable sources and bundles, select clusters by labels/groups,
   preview the exact placement, and launch policy-controlled rollouts in the
   Astronomer UI, CLI, or REST API.
3. The management plane resolves mutable source references to immutable commits,
   chart versions, and OCI digests before approval. Every per-cluster assignment
   is content-addressed and auditable.
4. The existing outbound agent materializes only validated Flux resources and
   credentials on its own cluster. Flux performs in-cluster Helm and Kustomize
   reconciliation and keeps doing so while Astronomer is temporarily unavailable.
5. The agent normalizes Flux Conditions, revision, drift, inventory, and events
   into Astronomer deployment status. Users never need the Flux CLI or CRDs for
   normal operations.
6. Rollouts support canaries, rolling batches, availability/failure budgets,
   approvals, maintenance windows, pause/resume/abort/retry, and rollback to a
   previously resolved revision.
7. Argo, its proxy, its chart dependency, its database and API surfaces, its
   self-management code, and the legacy imperative fleet-operation engine are
   absent from runtime code and release artifacts.
8. Charlie diagnoses delivery and cluster-agent health using bounded Astronomer
   APIs and can propose or perform only explicitly approved typed actions.
9. `v1.0.0` has one clean initial database migration and supports a fresh Helm
   install only. A v0.3.x database is intentionally incompatible: preflight
   fails with an explicit export/reset/reinstall instruction and never attempts
   an in-place conversion or destructive automatic reset.

## Terminology

| Term | Meaning | Persisted where |
|---|---|---|
| **Delivery source** | Reusable Git, OCI artifact, Helm HTTP repository, or Helm OCI repository plus authentication/trust policy. | PostgreSQL; credentials encrypted separately |
| **Component bundle** | A versioned deployable definition: source, renderer, target namespace/scope, values/patch policy, health checks, dependencies, and reconciliation policy. | PostgreSQL |
| **Delivery target** | Binds one desired bundle version to a placement selector and rollout policy. This replaces the Argo-shaped `GitOpsTarget`. | PostgreSQL |
| **Placement** | Deterministic selection of clusters from project scope, labels, explicit cluster IDs, and cluster groups. | Target spec; resolved membership snapshot in a rollout |
| **Rollout** | Immutable attempt to move a placement snapshot to one resolved bundle revision under a strategy. | PostgreSQL |
| **Cluster deployment** | Current desired and observed state for one `(target, cluster)` pair. | PostgreSQL; materialized as Flux objects downstream |
| **Assignment** | Versioned agent-protocol representation of one cluster deployment. | Tunnel snapshot; desired generation in PostgreSQL |
| **Delivery namespace** | Deterministic downstream namespace owned by Astronomer for a project’s Flux sources, Secrets, and reconcilers. | Downstream Kubernetes |
| **System delivery** | Privileged, Astronomer-owned reconciliation of the agent and Flux distribution itself. | Downstream Kubernetes, isolated from workload delivery |
| **Cluster agents** | Health, compatibility, and lifecycle of Astronomer agents. This replaces ambiguous `agent_fleet` naming. | PostgreSQL/API/UI |

## Goals

- Match the useful behavior of Rancher’s multi-cluster delivery experience
  without depending on Rancher Fleet.
- Make local pull/convergence the normal and default path, not a disabled flag.
- Provide one Astronomer data model and one rollout state machine.
- Support exact Git commit, OCI digest, HTTP Helm chart version+digest, and OCI
  Helm chart digest pinning.
- Support public/private sources, enterprise CAs, proxies, workload identity,
  key-based and keyless signature verification, and disconnected registries.
- Keep Flux APIs private to the implementation while preserving enough raw,
  sanitized diagnostics for advanced operators.
- Preserve existing project, cluster group, RBAC, audit, encryption, SSE,
  operations, maintenance-window, agent-tunnel, and Charlie guard conventions.
- Run safely with multiple server and worker replicas, agent reconnects, task
  retries, stale messages, partial outages, and large cluster counts.
- Ship a tagged, signed, SBOM-attested set of Astronomer images, chart, agent,
  Flux distribution manifest, and built-in bundle artifacts.

## Explicit non-goals

- No Rancher Fleet dependency or `fleet.cattle.io` compatibility.
- No Open Cluster Management dependency. Placement is small enough to own and
  uses Kubernetes `LabelSelector` semantics plus existing cluster groups.
- No Argo provider/compatibility layer, external Argo registration, Argo UI
  proxy, import, or data migration.
- No generic Flux dashboard or arbitrary editor for Flux CRDs.
- No direct management-plane kubeconfigs for downstream reconciliation. The
  outbound Astronomer agent remains the only management path.
- No branch-moving “continuous deployment” behind an approved rollout. Branches
  and tags are resolved to immutable references; a new upstream revision creates
  a new candidate rollout.
- No image automation controllers in v1. Users may point bundles at a new image
  or artifact revision through the Astronomer API, but Flux image-reflector and
  image-automation controllers are not installed.
- No notification-controller. The Astronomer agent already owns status/event
  transport and avoids a second callback/credential system.
- No preservation or import of a v0.3.x database, including users, projects,
  clusters, audit history, Argo state, or `fleet_operations`. An operator may
  export human-readable reference data before reinstall, but the v1 schema does
  not contain a compatibility importer.
- No self-management of the Astronomer management-plane Helm release through
  downstream Flux. Management-plane upgrades remain explicit tagged Helm upgrades.

## Current state

### Repository and release baseline

- Astronomer is clean at commit `564ce9a`, tagged `v0.3.9`.
- Charlie’s code baseline is `aaca50b`, tagged `v1.0.62`; its local `plans/`
  directory is untracked and belongs to the operator. Do not modify or remove it.
- Astronomer’s current migration chain ends at
  `159_seed_fresh_platform_configuration`. It is historical input to schema
  design only: Wave 12 replaces the entire chain with one new `001_initial`
  migration after all v1 code/schema changes are final.
- `Makefile` defines the authoritative gates: `make test`, `make lint`,
  `make verify`, `make verify-enterprise`, `make sqlc-check`, `make sdk`, and
  `make charlie-contract-check`.

### Argo is embedded throughout the product

- `deploy/chart/Chart.yaml` depends on `argo-cd` chart `9.5.21` and
  `deploy/chart/charts/argo-cd-9.5.21.tgz` is committed.
- `deploy/chart/values.yaml` enables Argo by default, configures ApplicationSet,
  and exposes the proxied UI path.
- `internal/server/routes_tools_controlplane.go` registers roughly 35 Argo
  endpoints. `internal/server/routes.go` mounts the UI proxy.
- `internal/handler/argocd.go` is a roughly 3,500-line API/operation/cache layer;
  `internal/handler/argocd/` is a typed Argo client; and
  `internal/handler/argocd_ui_proxy.go` is a custom authenticated reverse proxy.
- `internal/argosecurity/policy.go` and `internal/argolabels/labels.go` encode
  Argo-specific policy and ownership.
- `internal/server/self_manage_argocd.go`, `self_manage_migration.go`, and
  `self_manage_values.go` contain roughly 3,000 lines of self-management,
  migration, secret capture, approval, and write-barrier behavior.
- `internal/server/baseline_appsets.go`,
  `internal/worker/tasks/argocd_auto_register_cluster.go`, and the
  `ClusterBaselineReconciler`/`GitOpsTargetReconciler` in
  `internal/crd/controller.go` generate or observe ApplicationSets.
- `internal/crd/types.go` exposes `ComponentBundle` and `GitOpsTarget` shapes
  that contain Argo terms such as `ApplicationSet`, `SyncPolicy`, and
  `ArgoApplicationStatus`.
- `frontend/src/routes/dashboard/argocd/` and
  `frontend/src/components/argocd/` reproduce a substantial Argo UI.
- `cmd/astro/argocd.go`, Argo-specific docs/runbooks, live scripts, metrics,
  RBAC names, audit actions, OpenAPI entries, and generated SDK types complete
  the public surface.
- PostgreSQL has `argocd_instances`, `argocd_applications`, `argocd_operations`,
  `argocd_operation_events`, `argocd_managed_clusters`,
  `argocd_cluster_proxy_tokens`, and `argocd_baseline_ownership_decisions`.

### The existing “fleet” code is Astronomer code, not Rancher Fleet

`internal/handler/fleet_operations.go` describes an imperative operation fan-out
API. `internal/worker/tasks/fleet_orchestrate.go` snapshots a selector and polls
sub-operations. `internal/worker/tasks/fleet_selector.go` implements Kubernetes-
like selectors. The corresponding tables are `fleet_operations` and
`fleet_operation_targets`; UI, CLI, Charlie, dashboards, and scale scripts use
the same name. This implementation has no `fleet.cattle.io` dependency.

The new delivery rollout engine replaces it because keeping both would leave two
selectors, two fan-out state machines, two histories, and two user experiences.
The separate `internal/handler/agent_fleet.go` is mostly useful agent health and
lifecycle functionality; preserve the behavior but rename its public and source
surface to `cluster agents`.

### A useful local-pull foundation already exists

`internal/agent/reconcile.go` implements a disabled-by-default, manifest-based
pull loop. It requests `DesiredStateResponsePayload`, applies raw YAML locally,
prunes labeled objects, and reports `ApplyStatusPayload`. It is intentionally
limited to `astronomer-*` namespaces and refuses cluster-scoped objects.

`internal/server/desired_state.go` renders the agent plus baseline components,
but it remains coupled to Argo ownership decisions and hand-written manifests.
`pkg/protocol/types.go` defines `DESIRED_STATE_REQUEST`,
`DESIRED_STATE_RESPONSE`, and `APPLY_STATUS` without protocol negotiation,
snapshot cursors, typed assignments, or normalized Flux status.

Reuse the tunnel request/response, authenticated cluster identity, reconnect,
backpressure, session fencing, heartbeat, and event-bus conventions. Replace the
raw workload-manifest applier; do not expand it into a second GitOps engine.

### Existing control-plane conventions to retain

- PostgreSQL is authoritative for product state; generated `sqlc` queries live
  under `internal/db/sqlc` and are checked by `make sqlc-check`.
- Mutating APIs use server-side RBAC, stable typed error codes, audit events,
  operation records, and metadata-only SSE invalidation events.
- Secrets use the existing Fernet/key-version envelope pattern. See migrations
  145 and 146 and the plaintext-credential sealing worker. Never put source
  credentials in JSONB specs, audit payloads, status, task payloads, or logs.
- Asynq performs durable background work, but new HA work must use durable
  claims/fencing rather than the old Argo polling lease pattern documented in
  Advisor Plan 002.
- Frontend code uses React 19, Vite, TanStack Router/Query, shared table/status/
  operation components, SSE invalidation with bounded polling fallback, and
  generated route trees.
- Charlie capabilities are static typed adapters with bounded responses,
  effect/risk metadata, live RBAC, exact resource scope, argument digests,
  idempotency IDs, approval gates, and post-action verification.

## Commands the program will need

| Purpose | Command | Expected on success |
|---|---|---|
| Baseline | `git status --short` | no unexpected paths; user-owned dirty files remain untouched |
| Go unit/race | `make test` | exit 0; `go test -race -count=1 ./...` passes |
| Go lint | `make lint` | exit 0 |
| API contract | `make verify` | exit 0 |
| Full enterprise gate | `make verify-enterprise VERIFY_SCOPE=all` | exit 0 |
| SQL generation | `make sqlc-check` | exit 0 and no generated drift |
| Go SDK | `make sdk && git diff --exit-code -- pkg/astroclient` | generated SDK committed and stable |
| Charlie bridge | `make charlie-contract-check` | exit 0 |
| Frontend types | `npm --prefix frontend run type-check` | exit 0 |
| Frontend tests | `npm --prefix frontend test -- --run` | all tests pass |
| Frontend lint | `npm --prefix frontend run lint` | exit 0 |
| Frontend build | `npm --prefix frontend run build` | production build succeeds |
| Frontend E2E | `npm --prefix frontend run test:e2e` | all E2E scenarios pass |
| Helm | `make verify-enterprise VERIFY_SCOPE=helm` | lint, schema, render, image, dependency, and policy checks pass |
| Local management cluster | `make k3d-bootstrap` | management plane becomes Ready |
| Live delivery | `make validate-live-delivery` | two downstream clusters pass the scenario described in Wave 11 |
| Charlie repository | run the commands documented by `/root/astronomer-all/charlie/Makefile` after drift check | Charlie’s complete test/contract/pack gates pass |

Do not copy guessed Charlie commands into automation. At execution time inspect
its live `Makefile`/CI and record the exact commands in the wave PR description.

## Target architecture

```text
 User / CLI / Charlie
          |
          v
 Astronomer REST + RBAC + audit
          |
          v
 Sources -- Bundles -- Delivery Targets
                         |
                  Placement planner
                         |
                 immutable Rollout
                         |
              per-cluster Deployments
                         |
             outbound agent tunnel
                         |
       Astronomer agent on each cluster
       |      materialize / observe      |
       v                               v
  Flux Source API              Flux Helm/Kustomize API
       |                               |
       +---------- local Flux ----------+
                         |
                  cluster workloads
```

### Ownership boundary

Astronomer owns:

- source and credential registration;
- immutable revision resolution and trust policy;
- bundles, placement, rollouts, approvals, maintenance windows, rollback intent;
- assignment generation, idempotency, status normalization, history, audit;
- agent and Flux distribution lifecycle;
- all UI, CLI, REST, SSE, metrics, alerts, and Charlie capabilities.

Flux owns only:

- fetching a pinned Git/OCI/Helm source on a downstream cluster;
- rendering Kustomize or performing Helm lifecycle actions;
- server-side apply, health waiting, retry/remediation, prune, and drift repair;
- Kubernetes-native Conditions and inventories consumed by the agent.

Flux does not select clusters, decide rollout order, resolve mutable revisions,
store Astronomer users/projects, or provide a user-facing API/UI.

### Components installed downstream

Pin the Flux v2 release as a single tested distribution. At plan creation the
current upstream patch is `v2.9.3`; use it only if the support-matrix and
dependency checks in Wave 1 pass. Install:

- `source-controller`
- `kustomize-controller`
- `helm-controller`

Do not install notification-controller, image-reflector-controller,
image-automation-controller, source-watcher, Flux Operator, or a community Helm
chart. Generate and commit the official install manifest with the pinned Flux
CLI, patch it through a small checked-in Kustomize overlay, and publish the exact
result as a signed Astronomer OCI artifact. The Flux community Helm chart is
best-effort upstream and is not the production lifecycle boundary.

### Control-plane authority: PostgreSQL, not management CRDs

Delivery source, bundle, target, rollout, and deployment state is PostgreSQL-
authoritative and exposed through versioned REST/CLI declarative APIs. Remove
the management-plane `ClusterBaseline`, `ComponentBundle`, and `GitOpsTarget`
CRDs. They currently mirror product intent into Argo-shaped objects and would
reintroduce dual authority.

The CLI must support idempotent `apply -f` YAML/JSON so automation retains a
declarative interface. The YAML is an Astronomer API document, not a Kubernetes
CRD. Kubernetes `Cluster`, `Project`, and `AgentProfile` integrations may remain
where they serve other product behavior.

### Downstream namespace and RBAC model

- Create `astronomer-delivery-system` for the Flux controllers and system
  reconciliation resources.
- Create one deterministic control namespace per Astronomer project on a
  cluster: `astronomer-delivery-p-<first-12-hex-of-project-id>`. Put that
  project’s Flux Source objects, source credentials, Kustomizations,
  HelmReleases, service accounts, Roles, and RoleBindings there.
- Deny cross-namespace Flux references and remote Kustomize bases.
- Set `--default-service-account=astronomer-noop` on Helm and Kustomize
  controllers. `astronomer-noop` has no workload permissions. Every generated
  reconciler explicitly names a service account.
- Namespace-scoped bundles receive only the target-namespace verbs required by
  their renderer. Cluster-scoped bundles require `scope: platform`, a superuser
  author, an approval, an agent profile advertising
  `delivery.cluster_scope`, and the isolated platform service account.
- Only the agent service account may write Astronomer-labeled Flux objects and
  credential Secrets. Ordinary downstream users receive no access to delivery
  control namespaces by default.
- NetworkPolicies permit controller-to-source traffic, Kubernetes API access,
  DNS, and declared source/proxy egress; they deny inbound tenant traffic.

## Architecture decisions and rejected alternatives

| Decision | Reason | Rejected alternative |
|---|---|---|
| Local Flux per managed cluster | Continues convergence during management outages, keeps source traffic local, and matches the outbound-agent topology. | Central Flux with tunneled kubeconfigs preserves hub-push coupling and creates a central scale/failure bottleneck. |
| Astronomer placement/rollout engine | These are differentiating product semantics and can reuse existing clusters/groups/RBAC/audit. | Rancher Fleet would make a direct competitor a core dependency; OCM adds a second large control plane. |
| Typed assignment protocol | Prevents arbitrary management-plane YAML from becoming cluster-admin input and gives protocol/version validation. | Extending `DesiredManifest.Content` into arbitrary raw manifests duplicates Flux and weakens the trust boundary. |
| Immutable source resolution | Approval always names exactly what runs and offline clusters cannot follow a moved tag. | Letting each Flux instance track `main`, semver ranges, or mutable OCI tags can yield different artifacts across cohorts. |
| PostgreSQL authority | Matches current product state, transactions, audit, API, and HA patterns. | New management CRDs recreate dual persistence and Argo-era controller complexity. |
| Official manifest + small overlay | Reproducible, auditable, independent of best-effort chart cadence. | Flux community Helm chart or Flux Operator adds a lifecycle dependency and more APIs than needed. |
| Agent observes Flux directly | Reuses authenticated outbound transport and avoids inbound callbacks. | notification-controller creates a second networking/authentication path. |
| Delete legacy imperative operations | One rollout engine, selector, state model, UI, and audit trail. | Retaining `fleet_operations` makes behavior ambiguous and doubles maintenance. |
| Fresh-install-only v1 cutover | Produces the requested greenfield schema and removes every compatibility branch. | In-place migration would retain historical tables, mixed-version logic, and Argo-era assumptions. |

## Data model

During Waves 2–11, use temporary sequential development migrations so each wave
can be tested. At the end, Wave 12 recreates an empty database at the final code
revision, emits one canonical `001_initial.up.sql` containing the complete v1
schema and seed data, and deletes the entire historical/temporary migration
chain. The committed release has exactly `001_initial.up.sql` and
`001_initial.down.sql`. Use UUID primary keys, `TIMESTAMPTZ`, the standard
`set_updated_at` function, explicit CHECK constraints, foreign keys, project
scoping, and partial indexes for worker hot paths.

### `delivery_sources`

| Column | Contract |
|---|---|
| `id`, `project_id`, `name`, `description` | unique `(project_id, name)`; project deletion is restricted while referenced |
| `type` | `git`, `oci_artifact`, `helm_http`, or `helm_oci` |
| `url` | normalized non-secret URL; SSRF policy validated on create and every resolve |
| `auth_mode` | `none`, `basic`, `bearer`, `ssh`, `workload_identity` |
| `credential_encrypted`, `credential_key_version` | existing envelope type; never returned by read APIs |
| `ca_bundle_encrypted`, `proxy_ref` | encrypted private CA if supplied; named proxy policy, not raw per-request proxy URL |
| `trust_policy` | bounded JSON with required signature provider/identity/key refs and `allow_unsigned` (default false in production) |
| `status`, `last_resolved_at`, `last_error_code` | bounded enum/code only; detailed sanitized event in history |
| standard actor/timestamps | `created_by`, `updated_by`, `created_at`, `updated_at` |

### `component_bundles` and `component_bundle_versions`

`component_bundles` stores stable identity and ownership. Every edit that can
change downstream output creates an immutable `component_bundle_versions` row.

A version contains:

- renderer: `kustomize` or `helm`;
- delivery source ID;
- requested reference and centrally resolved immutable reference;
- for Kustomize: path, target namespace, patches, substitutions, prune, wait,
  timeout, health checks, and dependency bundle IDs;
- for Helm: chart, exact chart version, chart digest where available, release
  name template, target namespace, values, `valuesFrom` credential-free config
  refs, install/upgrade/test/remediation/drift policy, and dependencies;
- scope: `namespace` or `platform`;
- required agent/Flux/Kubernetes capabilities;
- canonical spec SHA-256, source verification result, source metadata, creator,
  and timestamp.

No plaintext Secret value, Git token, Helm password, kubeconfig, or rendered
workload manifest is stored in the version row. Values are rejected if their
schema marks a field secret; secret inputs are separate credential references.

### `delivery_targets`

Store stable target identity plus current desired bundle version, placement,
rollout policy, maintenance-window policy, suspension, generation, and actor.
Use optimistic concurrency (`If-Match`/resource version) on update. Placement is
a typed structure, not arbitrary JSON evaluation:

```json
{
  "project_ids": ["..."],
  "cluster_ids": [],
  "cluster_group_ids": ["..."],
  "match_labels": {"environment": "production"},
  "match_expressions": [
    {"key": "region", "operator": "In", "values": ["us-east-1", "us-west-2"]}
  ],
  "exclude_cluster_ids": [],
  "all_clusters": false
}
```

An empty selector matches zero clusters. Selecting every accessible cluster
requires `all_clusters: true` plus a second confirmation/approval. Every preview
returns match reasons and exclusions, not just a count.

### `delivery_rollouts` and `delivery_rollout_clusters`

The rollout row freezes target generation, from/to bundle version, placement
digest, strategy, availability/failure budgets, approval policy, initiator,
idempotency key, state, counters, deadlines, and fencing generation.

`delivery_rollout_clusters` freezes each selected cluster, cohort/order,
previous deployment revision, desired revision, state, attempt, last error code,
deadline, lease owner/expiry, and timestamps. Unique `(rollout_id, cluster_id)`
and a partial index over runnable states support worker claims.

### `cluster_deployments` and status history

`cluster_deployments` has one current row per `(target_id, cluster_id)`:

- desired/observed generations and spec digests;
- desired/observed immutable source revision;
- action (`apply`, `suspend`, `delete`);
- phase (`pending`, `blocked`, `applying`, `ready`, `degraded`, `failed`,
  `suspended`, `deleting`, `removed`, `unknown`);
- normalized Ready/Reconciling/Stalled/Drifted conditions;
- source and reconciler kind/name, inventory counts, agent session/sequence,
  last observed timestamp, error code, and bounded sanitized message;
- current rollout ID and previous known-good bundle version.

Append transitions to `cluster_deployment_events` with a retention/partitioning
policy. Do not append every repeated Flux resync. Coalesce identical condition
sets and retain state changes, warnings, operator actions, and revision changes.

### Supporting tables

- `delivery_source_resolutions`: immutable resolution attempts, digest,
  verification identity/result, and error code; no credentials/content.
- `delivery_assignment_receipts`: latest acknowledged generation/sequence per
  cluster and bounded recent protocol errors.
- `delivery_controller_inventory`: agent-reported Flux component versions,
  CRD storage versions, readiness, and compatibility.
- Reuse existing maintenance-window tables if their contract is real. If the
  old fleet checker is still a no-op, make the real implementation a Wave 4
  prerequisite rather than silently ignoring the flag.

### Final single-migration schema

The final `001_initial.up.sql` must never create any Argo or legacy imperative
fleet table, column, trigger, index, seed, permission, audit action, tool, or
setting. In particular, none of these appear in the v1 schema:

- `argocd_baseline_ownership_decisions`
- `argocd_cluster_proxy_tokens`
- `argocd_managed_clusters`
- `argocd_operation_events`
- `argocd_operations`
- `argocd_applications`
- `argocd_instances`
- `fleet_operation_targets`
- `fleet_operations`

Migration 091 historically introduced generic external ownership metadata to
`clusters` and `projects`. Include equivalent columns in the v1 initial schema
only if the remaining management CRD integrations still use them, and document
them as generic external ownership rather than “fleet” compatibility.

`001_initial.down.sql` drops the v1 schema in dependency-safe order. Replace the
numbered migration-specific Go tests with `initial_schema_test.go` and focused
schema contract tests that start an empty PostgreSQL database, migrate to version
1, verify tables/constraints/indexes/seeds, migrate down to zero, and migrate up
again. Add a guard that fails if any second `*.up.sql` or `*.down.sql` exists at
the v1 tag.

## Public API and CLI contract

Put the new surface under `/api/v1/delivery`. Do not alias old `/argocd` or
`/fleet-operations` routes. All list endpoints are project-scoped, paginated,
filterable, and stable-sorted. All writes accept an `Idempotency-Key`; updates
also require `If-Match`. Return the existing API envelope and typed error format.

### REST resources

| Method and path | Purpose / authorization |
|---|---|
| `GET/POST /delivery/sources` | list/create; `delivery_sources:read/create` |
| `GET/PATCH/DELETE /delivery/sources/{id}` | detail/update/delete; delete blocked while referenced |
| `POST /delivery/sources/{id}/verify` | durable resolution/auth/trust check; no credential echo |
| `POST /delivery/sources/{id}/rotate-credential` | write-only replacement and materialization generation bump |
| `GET/POST /delivery/bundles` | list/create bundle identity |
| `GET/PATCH/DELETE /delivery/bundles/{id}` | stable bundle metadata |
| `GET/POST /delivery/bundles/{id}/versions` | list/create immutable version and start source resolution |
| `GET /delivery/bundles/{id}/versions/{versionId}` | resolved revision, verification, policy, schema |
| `GET/POST /delivery/targets` | list/create target |
| `GET/PATCH/DELETE /delivery/targets/{id}` | target detail/update; mutations create a rollout candidate |
| `POST /delivery/targets/{id}/preview` | authoritative match set, exclusions, capability blockers, diff, risk |
| `POST /delivery/targets/{id}/rollouts` | freeze preview and launch/queue approval |
| `GET /delivery/rollouts` | filter by target/project/state/revision/actor/time |
| `GET /delivery/rollouts/{id}` | immutable plan, counters, approvals, timeline |
| `GET /delivery/rollouts/{id}/clusters` | paginated per-cluster state and reasons |
| `POST /delivery/rollouts/{id}/{pause,resume,abort,retry,approve,rollback}` | CAS-protected typed transitions; risk-specific RBAC |
| `GET /delivery/deployments` | current target×cluster state |
| `GET /delivery/deployments/{id}` | normalized conditions, source/reconciler status, events, history |
| `POST /delivery/deployments/{id}/reconcile` | bump requested reconcile nonce; never accepts raw Flux objects |
| `POST /delivery/deployments/{id}/suspend` | explicit target-level exception, audited |
| `GET /delivery/clusters/{clusterId}/inventory` | Flux distribution compatibility and deployments |
| `GET /delivery/system/compatibility` | supported agent/Flux/Kubernetes matrix and current rollout |

`DELETE target` creates a deletion rollout and leaves the target in `deleting`
until all reachable deployments report removal. `orphan=true` is a separate,
superuser-only action that records retained downstream resources. A database row
must not disappear before its finalizer-equivalent state is resolved.

### Example bundle-version request

```yaml
name: ingress-nginx
description: Managed ingress controller
source_id: 1b749766-03e2-4af3-af76-57375664c884
requested_revision: 4.12.1
renderer: helm
scope: namespace
helm:
  chart: ingress-nginx
  release_name: ingress-nginx
  target_namespace: ingress-nginx
  values:
    controller:
      replicaCount: 2
  install:
    remediation:
      retries: 3
  upgrade:
    remediation:
      strategy: rollback
      retries: 2
  tests: true
  drift_detection: enabled
health:
  timeout: 10m
requirements:
  kubernetes: ">=1.33 <1.37"
```

The response is `202 Accepted` while the resolver obtains and verifies the
exact chart. A resolved version cannot be edited:

```json
{
  "id": "...",
  "requested_revision": "4.12.1",
  "resolved_revision": "4.12.1",
  "artifact_digest": "sha256:<redacted-example>",
  "verification": {"status": "verified", "policy_id": "..."},
  "spec_digest": "sha256:<redacted-example>",
  "state": "ready"
}
```

### Example target and strategy

```yaml
name: production-ingress
project_id: 18fc69f4-5763-4541-bafb-1ef22192bcfa
bundle_version_id: 2bfdd32f-713e-4c03-8e7c-968aed474a65
placement:
  cluster_group_ids:
    - 4949dc92-9b68-4ed5-ad17-c86a7aa0aebf
  match_labels:
    environment: production
  match_expressions:
    - key: delivery.astronomer.io/enabled
      operator: In
      values: ["true"]
  all_clusters: false
rollout:
  strategy: canary
  canary:
    count: 2
    approval_after_canary: true
  max_concurrent: 10
  max_unavailable: 1
  min_ready_seconds: 120
  progress_deadline: 30m
  failure_threshold:
    count: 2
    percent: 10
  on_failure: rollback
  respect_maintenance_windows: true
reconcile:
  interval: 10m
  drift: repair
  prune: true
```

### CLI

Replace `cmd/astro/argocd.go` and `cmd/astro/fleet.go` with a `delivery` command
tree:

```text
astro delivery source {list,get,create,update,delete,verify,rotate-credential}
astro delivery bundle {list,get,create,delete,version-create,version-list}
astro delivery target {list,get,apply,delete,preview}
astro delivery rollout {list,get,start,pause,resume,approve,abort,retry,rollback,watch}
astro delivery deployment {list,get,reconcile,suspend,resume,events}
astro cluster-agent {list,get,diagnostics,upgrade}
```

`target apply -f` is idempotent by `(project_id, name)` and supports `--dry-run`
and `--output json|yaml`. `preview` prints selected/excluded clusters and risks;
it never launches. Destructive commands require `--yes` only in non-interactive
mode and still require server-side approval/RBAC.

## Agent protocol v2

Do not mutate the existing payload in place. Introduce protocol v2 with explicit
capability negotiation and delete the old desired-manifest messages and applier
before release. Because v1 is fresh-install only, the server does not negotiate
with v0.3.x agents; authentication returns a stable `agent_reenrollment_required`
error and the UI/CLI points to a newly generated registration command.

### Messages

```go
const (
    MsgDeliveryStateRequest  MessageType = "DELIVERY_STATE_REQUEST_V2"
    MsgDeliveryStateResponse MessageType = "DELIVERY_STATE_RESPONSE_V2"
    MsgDeliveryStatus        MessageType = "DELIVERY_STATUS_V2"
    MsgDeliveryReconcile     MessageType = "DELIVERY_RECONCILE_V2"
)
```

Target shape (names illustrative; finalize in `pkg/protocol` before generated
contracts are changed):

```go
type DeliveryStateRequestV2 struct {
    ClusterID              string                    `json:"cluster_id"`
    ProtocolVersion        string                    `json:"protocol_version"`
    AckedSnapshotGeneration int64                    `json:"acked_snapshot_generation"`
    AckedETag              string                    `json:"acked_etag,omitempty"`
    ControllerInventory    DeliveryControllerInventory `json:"controller_inventory"`
}

type DeliveryStateResponseV2 struct {
    SnapshotGeneration int64                  `json:"snapshot_generation"`
    ETag               string                 `json:"etag"`
    FullSnapshot       bool                   `json:"full_snapshot"`
    Assignments        []DeliveryAssignmentV2 `json:"assignments"`
    Deletions          []DeliveryDeletionV2   `json:"deletions"`
    CredentialEpoch    int64                  `json:"credential_epoch"`
}

type DeliveryAssignmentV2 struct {
    DeploymentID       string             `json:"deployment_id"`
    TargetID           string             `json:"target_id"`
    ProjectID          string             `json:"project_id"`
    Generation         int64              `json:"generation"`
    SpecDigest         string             `json:"spec_digest"`
    Action             string             `json:"action"` // apply|suspend
    Scope              string             `json:"scope"`
    Source             DeliverySourceV2   `json:"source"`
    Renderer           DeliveryRendererV2 `json:"renderer"`
    Policy             DeliveryPolicyV2   `json:"policy"`
    Credential         *SealedCredentialV2 `json:"credential,omitempty"`
}
```

Rules:

- The authenticated tunnel session is the authoritative cluster ID. Reject a
  payload cluster ID that does not match it.
- A snapshot is canonical JSON sorted by deployment ID; `ETag` is its SHA-256.
  If the request’s acknowledged ETag is current, return a small not-modified
  response rather than resending credentials/specs.
- The server sends only assignments whose rollout cohort is released. Pending
  future cohorts are invisible to the cluster.
- The assignment renderer is a closed tagged union. Unknown fields/kinds,
  unsupported protocol versions, inconsistent immutable revisions, invalid
  DNS names, and sizes over limits fail closed before any Kubernetes write.
- Maximum snapshot, assignment, values, patch, CA, and credential sizes are
  constants with unit and live tests. Compress at the WebSocket frame layer only
  after decompression-bomb bounds exist.
- Credential material is separately encrypted at rest, transmitted only over
  authenticated TLS, decrypted only for an authorized assignment, written to a
  deterministic Secret, never included in ETag/audit/log/status, and zeroed from
  temporary buffers where practical. Rotation increments `CredentialEpoch`.
- Deletions are tombstones with deployment ID, generation, spec digest, deadline,
  and orphan policy. The server retains them until the agent reports `removed`.
- Status includes deployment/generation/spec digest, monotonically increasing
  agent-session sequence, observed Flux generations, normalized Conditions,
  resolved revision/digest, inventory summary, warning codes, and observation
  time. The server ignores stale session, sequence, generation, or spec digest.
- On reconnect the agent sends a full observed snapshot. During disconnection,
  local Flux continues using the last accepted assignment; no desired resources
  are pruned just because the management plane is absent.

### Agent implementation boundaries

Replace `internal/agent/reconcile.go` with three small packages instead of a
larger god object:

- `internal/agent/delivery/validator.go` — validates closed assignment DTOs and
  capability/scope policy with no Kubernetes side effects.
- `internal/agent/delivery/materializer.go` — deterministic Flux object and
  Secret/RBAC builder plus server-side apply/prune of only Astronomer-labeled
  delivery objects.
- `internal/agent/delivery/observer.go` — shared dynamic informers, condition
  normalization, debounce/coalescing, reconnect snapshot, and status sending.

Keep system bootstrap separate in `internal/agent/systemreconcile`. It may
manage only an embedded/signed allowlisted agent+Flux system bundle. Workload
assignments must never be able to inject CRDs, ClusterRoles, webhooks, or a
controller Deployment.

## Flux distribution lifecycle

### Build and provenance

Add `deploy/flux/` containing:

```text
deploy/flux/
  VERSION
  upstream-install.yaml
  kustomization.yaml
  patches/
  install.yaml
  checksums.txt
  provenance.json
  README.md
```

`scripts/update-flux-distribution.sh <version>` must:

1. download the Flux CLI/release assets from the official GitHub release;
2. verify the release checksum/signature using committed trust roots/policy;
3. run the pinned CLI in a container or verified binary to export only the three
   selected components with network policy enabled;
4. apply deterministic local Kustomize patches;
5. reject floating image tags and resolve every controller image to
   `repository@sha256:digest` while retaining version labels;
6. generate checksums, source URL, release version, timestamp, and license/SBOM
   metadata;
7. run policy/schema validation and fail on an unreviewed RBAC/API/image diff.

CI reruns the generation in check mode and requires no diff. Dependabot or a
scheduled workflow may open update PRs, but production never follows “latest.”

### Bootstrap and upgrade

The cluster registration manifest is one ordered multi-document stream:

1. namespaces;
2. Flux CRDs;
3. Flux service accounts/RBAC/network policies;
4. Flux controllers;
5. Astronomer agent service account/RBAC/config/Deployment;
6. Astronomer system OCI source and Kustomization, suspended until the agent
   validates enrollment.

The initial `kubectl apply` is the one-time cluster-admin act. Thereafter the
signed system Kustomization upgrades the pinned Flux distribution. The agent
sets the desired system artifact digest only when the management plane releases
that cluster’s system cohort. The platform Kustomization runs under an isolated
platform-admin service account; workload Kustomizations cannot reference it.

Upgrade order is:

1. preflight Kubernetes version, CRD conversion/storage versions, API discovery,
   controller health, disk/memory headroom, and current assignment health;
2. upgrade Astronomer agent to a version supporting both old and new Flux APIs;
3. canary the new Flux distribution on internal clusters;
4. roll through opted-in groups under max-unavailable/failure budgets;
5. verify controllers and CRDs, then enable new API usage;
6. retire old served CRD versions only after storage migration is observed.

Rollback pins the previous signed distribution digest and agent version. Never
roll back across a Flux CRD storage-version boundary unless the tested upstream
procedure explicitly supports it.

### Flux hardening overlay

The overlay must include:

- `--no-cross-namespace-refs=true` on all applicable controllers;
- `--no-remote-bases=true` on kustomize-controller;
- `--default-service-account=astronomer-noop` on Helm/Kustomize controllers and
  the relevant workload-identity default flags on source-controller;
- non-root, read-only filesystem, dropped capabilities, seccomp RuntimeDefault,
  resource requests/limits, topology spread, PDBs, priority class option, and
  explicit service accounts;
- default-deny ingress and narrowly declared controller communication;
- Prometheus Service/PodMonitor integration when monitoring is enabled;
- no unauthenticated webhook receiver or external Service;
- controller concurrency and rate limits configurable from an Astronomer
  compatibility profile, with safe defaults measured in Wave 10.

## Flux resource materialization

Every object carries:

```yaml
metadata:
  labels:
    app.kubernetes.io/managed-by: astronomer-agent
    delivery.astronomer.io/deployment-id: <uuid>
    delivery.astronomer.io/project-id-hash: <bounded-hash>
  annotations:
    delivery.astronomer.io/spec-digest: sha256:<digest>
    delivery.astronomer.io/generation: "7"
```

Names are deterministic, DNS-safe, and based on the deployment UUID, not a
user-controlled display name. The materializer computes the complete expected
object set before applying. Prune lists only the relevant delivery namespace,
managed-by label, and deployment ID. It never deletes on a partial/failed apply.

### Kustomize assignment example

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: GitRepository
metadata:
  name: d-7c1f0d8e-source
  namespace: astronomer-delivery-p-9e81fcd56b87
spec:
  interval: 10m
  url: ssh://git@github.example/platform/apps.git
  ref:
    commit: 0123456789abcdef0123456789abcdef01234567
  secretRef:
    name: d-7c1f0d8e-source-auth
  verify:
    mode: HEAD
    secretRef:
      name: d-7c1f0d8e-trust
---
apiVersion: kustomize.toolkit.fluxcd.io/v1
kind: Kustomization
metadata:
  name: d-7c1f0d8e
  namespace: astronomer-delivery-p-9e81fcd56b87
spec:
  interval: 10m
  retryInterval: 1m
  timeout: 10m
  path: ./clusters/base
  prune: true
  wait: true
  serviceAccountName: d-7c1f0d8e-applier
  sourceRef:
    kind: GitRepository
    name: d-7c1f0d8e-source
```

### Helm assignment example

```yaml
apiVersion: source.toolkit.fluxcd.io/v1
kind: HelmRepository
metadata:
  name: d-7c1f0d8e-source
  namespace: astronomer-delivery-p-9e81fcd56b87
spec:
  interval: 10m
  url: https://charts.example.com
  secretRef:
    name: d-7c1f0d8e-source-auth
---
apiVersion: helm.toolkit.fluxcd.io/v2
kind: HelmRelease
metadata:
  name: d-7c1f0d8e
  namespace: astronomer-delivery-p-9e81fcd56b87
spec:
  interval: 10m
  timeout: 10m
  releaseName: ingress-nginx
  targetNamespace: ingress-nginx
  serviceAccountName: d-7c1f0d8e-applier
  chart:
    spec:
      chart: ingress-nginx
      version: 4.12.1
      sourceRef:
        kind: HelmRepository
        name: d-7c1f0d8e-source
  install:
    remediation:
      retries: 3
  upgrade:
    remediation:
      retries: 2
      strategy: rollback
  test:
    enable: true
  driftDetection:
    mode: enabled
```

For OCI artifacts, generate `OCIRepository.spec.ref.digest` and a signature
verification policy. For Helm OCI, use the supported Flux source/chart shape and
pin the exact version/digest according to the selected Flux API. Golden tests
must compare complete objects for every source/renderer/auth/trust/scope variant.

## Placement and rollout engine

### Placement algorithm

Build one pure package, `internal/delivery/placement`, used by preview and
planner. Its inputs are the caller’s allowed projects, active non-decommissioned
clusters, labels, group membership, capabilities, compatibility, exclusions,
and target selector. Its output is stable-sorted by cluster UUID and includes
one of: `selected`, `excluded_by_selector`, `excluded_explicitly`,
`unauthorized`, `disconnected`, `incompatible`, `missing_capability`, or
`decommissioning`.

Rules:

- Label expressions exactly implement `In`, `NotIn`, `Exists`, and
  `DoesNotExist` Kubernetes semantics.
- Group and label predicates are ANDed; multiple groups are ORed within the
  group field. Explicit cluster IDs still require project access and all safety
  gates.
- Preview and launch run the same function in the same transaction snapshot.
  Launch includes the preview digest and fails `409 preview_stale` if membership,
  target generation, or source resolution changed.
- Placement is frozen per rollout. Later membership changes create a separate
  membership rollout: additions apply desired state; removals delete it.
- `all_clusters` cannot be combined with an empty/implicit selector and requires
  enhanced confirmation plus any configured approval policy.

### Rollout state machines

Rollout states:

```text
draft -> resolving -> awaiting_approval -> queued -> progressing
                         |                 |          |
                         v                 v          v
                      rejected          aborted    paused
                                                     |
                         succeeded <-------+------> failed
                                                  |
                                              rolling_back
                                                  |
                                      rolled_back | rollback_failed
```

Per-cluster states:

```text
pending -> released -> acknowledged -> reconciling -> ready
             |             |               |
           blocked       timed_out        failed
             |                              |
           skipped                      rolling_back -> ready_previous|failed
```

Every transition is a compare-and-swap over current state plus fencing
generation and occurs in one database transaction with its event/outbox row.
Workers claim rollout-cluster rows using `FOR UPDATE SKIP LOCKED`, set a short
lease with owner and monotonically increasing fence, and may repeat safely.
Agent assignments are idempotent; a retried release of the same generation does
not create a new Flux action.

### Strategies

- **All at once**: releases every eligible cluster, still respecting a hard
  global safety cap.
- **Rolling**: stable order or seeded deterministic shuffle, with
  `max_concurrent`, `max_unavailable`, `min_ready_seconds`, and progress deadline.
- **Canary**: explicit cluster IDs or a count/percentage selected by stable hash,
  then optional human approval before rolling cohorts.
- **Partitioned**: group/label-derived ordered cohorts (for example dev, staging,
  production regions) with per-boundary approval and soak time.

Failure budget may be count and/or percentage; exceeding either stops release.
`on_failure` is `pause`, `continue`, or `rollback`, defaulting to `pause` in
production. Automatic rollback uses the frozen previous known-good bundle
version, not “whatever was previously on the branch.” A failed rollback is a
distinct terminal state and pages operators.

Maintenance windows gate release of a new generation, not Flux’s correction of
drift for an already released generation. Emergency reconcile and rollback can
override a window only with explicit permission and an audit reason.

### Disconnect and deletion behavior

- A disconnected cluster stays `blocked` before release or `unknown` after a
  released generation goes stale. It does not consume concurrency until an
  assignment is acknowledged, but it does count against the rollout deadline.
- The agent never deletes a working deployment because it lost the tunnel.
- A target removal produces a deletion tombstone. The agent sets Flux
  reconciliation suspended, deletes the reconciler/source/credential/RBAC set
  in dependency order, waits for prune/uninstall according to policy, and
  reports `removed`.
- Decommissioning a cluster freezes new releases, offers `delete managed
  workloads` or `orphan`, and records the choice before agent teardown.

## Source resolution and supply-chain policy

Implement `internal/delivery/resolver` behind a narrow interface and one worker
task per source type. Reuse `go-git` and Helm libraries already present only
where their URL/TLS/proxy behavior passes the existing SSRF policy. Add an OCI
registry client only if Helm’s registry package cannot resolve both artifact and
chart digests without pulling unbounded content.

Resolution rules:

- Git branch/tag -> exact 40/64-character commit ID. Verify commit/tag signature
  when policy requires it. The downstream `GitRepository` uses `ref.commit`.
- OCI tag -> manifest digest. Downstream uses `ref.digest` and Flux Cosign or
  Notation verification with exact issuer/subject/key policy.
- Helm HTTP -> exact chart version and index digest; download with bounded size,
  verify provenance/signature policy, compute SHA-256, and ensure the downstream
  source reports the same artifact digest before marking deployment Ready.
- Helm OCI -> exact chart version plus manifest digest and signature identity.
- Reject mutable `latest` in production unless the request is only a resolver
  input; it still becomes a digest-pinned immutable bundle version.
- Resolve each version once. A new upstream value never mutates an existing
  `component_bundle_versions` row.

Apply the existing outbound URL policy to initial URLs and every redirect, DNS
resolution, proxy hop, Git submodule, Helm dependency, and OCI referrer. Default
deny loopback, link-local, metadata, cluster service, Unix socket, and private
ranges unless an administrator has explicitly registered an enterprise source
network policy. Re-resolve DNS at connection time and prevent DNS rebinding.

For built-in platform components, create `deploy/bundles/` source trees and a
release job that publishes a signed, immutable `astronomer-platform-bundles`
OCI artifact. Do not keep hand-rendered workload YAML in Go such as the current
`renderKubeStateMetricsManifest`. The release manifest records the bundle
artifact digest, Flux distribution digest, chart/app version, image digests,
SBOM, and provenance together.

## Security and tenancy requirements

### Threat boundaries

Treat all of the following as untrusted input: UI/CLI specs, source content,
Helm values, Git metadata, Flux Conditions/events/messages, agent status,
downstream object names, and Charlie arguments. The trusted computing base is
the authenticated Astronomer API, validated typed assignment, signed agent
binary, pinned Flux distribution, Kubernetes RBAC/admission, and configured
source trust roots.

### Required controls

- Every API object is scoped to a project and checked through existing server
  RBAC. A caller cannot discover resource existence across projects through
  status codes, counts, SSE, search, or Charlie.
- Add resource verbs for `delivery_sources`, `component_bundles`,
  `delivery_targets`, `delivery_rollouts`, `cluster_deployments`, and
  `cluster_agents`. Split approval, rollback, platform-scope, credential-rotate,
  and orphan permissions from ordinary update.
- Source credentials are write-only. List/detail returns auth type, owner,
  fingerprint, creation/rotation/expiry metadata, never secret bytes or
  Kubernetes Secret names.
- Source verification workers receive credential IDs, not plaintext task
  payloads. They decrypt immediately before use and redact errors through the
  central redaction package.
- Agent credential Secret names are deterministic hashes without project/source
  names. Secret data is omitted from diagnostic bundles, inventory, Kubernetes
  events, logs, audit, and Charlie responses.
- Workload delivery service accounts follow least privilege. Namespace bundles
  cannot create namespaces, CRDs, RBAC outside their target namespace, webhook
  configurations, storage classes, nodes, or other cluster-scoped resources.
- Platform-scope bundle creation/versioning/rollout is superuser-only and always
  requires human approval. Charlie can diagnose but never author or auto-run a
  platform-scope rollout.
- Admission validates generated Flux resources by field allowlist. Disable
  cross-namespace references and remote bases even if a generated object is
  accidentally malformed.
- Keep source-controller artifact Services internal. No Flux UI, webhook, or
  public endpoint is exposed by the management-plane chart.
- Sign Astronomer images, chart, built-in OCI bundles, and Flux distribution
  artifacts. Verify before publication and at downstream fetch. Generate SBOMs
  and vulnerability reports under the existing release policy.
- Pin every image by digest in production output. Support registry rewrite plus
  digest mapping for air-gapped installs without falling back to tags.
- Audit create/update/delete/verify/preview/approve/release/pause/resume/abort/
  retry/rollback/reconcile/suspend/orphan/credential-rotate and system upgrades.
  Audit stores IDs, digests, counts, reasons, and result codes—not specs or secrets.
- Apply rate/size limits per user, project, source, agent, and cluster. Bound
  concurrent source fetches and decompressed archive size/file count/path depth.
  Reject archive traversal, symlinks escaping the root, and YAML/resource bombs.
- Sanitize Flux messages before persistence/display: UTF-8, length, control
  characters, URLs/query strings, token-like values, and object content.
- Add NetworkPolicy, Pod Security, seccomp, non-root, read-only filesystem,
  resource-limit, and default-deny tests for all downstream components.

### Credential rotation and revocation

Rotation writes a new encrypted credential version, increments source and
assignment credential epochs, distributes the new Secret, waits for each Flux
Source to become Ready at the already approved immutable revision, and only then
deletes the old Secret. Revocation immediately suspends affected source objects
and blocks new rollouts; it does not delete already running workloads. A
break-glass revocation action may remove material from reachable clusters and
records unreachable clusters for follow-up.

### Security test matrix

Include positive and negative tests for cross-project IDOR, selector scope,
source SSRF/redirect/rebinding, private CA, proxy policy, credential readback,
log/audit/event redaction, malicious archive paths, YAML/file/size limits,
cross-namespace Flux refs, remote bases, namespace escape, cluster-scope denial,
stale/replayed agent messages, status spoofing, invalid signatures, changed
digests, registry credential rotation, and Charlie approval/resource binding.

## Reliability, HA, scale, and disaster recovery

### Control-plane HA

- Planner/resolver/rollout workers use transactional outbox rows, durable claims,
  fencing tokens, and idempotency keys. Never use a timestamp-only lease that
  allows the same external action to be submitted twice.
- A released assignment is a database fact before notification. Tunnel push is
  a wake-up hint; agent polling heals lost hints.
- Status upserts compare authenticated session, sequence, deployment generation,
  and spec digest. Duplicate/out-of-order messages are successful no-ops.
- Counters are derived or transactionally updated from rollout-cluster state;
  a periodic verifier repairs discrepancies and exposes a metric.
- All loops have batch size, tick budget, jitter, backoff, and cancellation.
  No server request synchronously fans out over clusters or fetches sources.
- Multiple server/worker replicas pass kill/restart tests mid-resolution,
  mid-cohort, mid-status-ingest, mid-credential-rotation, and mid-delete.

### Downstream resilience

- Flux’s last accepted objects remain and reconcile locally through management
  outages and agent restarts.
- The agent rebuilds observed state from Kubernetes informers after restart;
  correctness does not depend on an in-memory queue.
- If Flux controllers are down, the agent reports distribution degradation and
  does not rewrite workload desired state until system health is restored.
- Failed source fetches or reconciliation do not prune the last healthy release.
- Clock skew cannot order messages; use sequence/fence/generation, with wall
  clock only for display and timeouts evaluated by the authority that owns them.

### Capacity targets

Qualify at minimum:

- 10,000 registered clusters;
- 5,000 concurrently connected agents spread across at least three server replicas;
- 100,000 current cluster deployments;
- a 10,000-cluster target preview under 2 seconds p95 after cache warm-up and
  under 5 seconds p95 cold;
- 1,000 assignment releases/minute without status backlog exceeding 60 seconds;
- 10 status transitions/second/cluster burst coalesced to bounded database writes;
- source artifacts up to the documented safe limit and snapshots within the
  negotiated frame limit;
- management outage of 24 hours with local convergence intact and complete
  status recovery after reconnect.

Do not claim these values without measured reports in `docs/assurance/`. If the
product chooses different targets before implementation, update this plan and
test harness together.

### Backup and restore

Include all new delivery tables, encryption metadata, outbox, and audit rows in
management-plane backup/restore drills. Restore into an isolated management
cluster with outbound notifications disabled, validate referential integrity,
then explicitly rotate agent/source credentials before reconnecting production
clusters. Restored stale rollout leases are cleared; terminal state and
assignments remain. Reconciliation resumes only after an operator unlocks the
restored installation.

The v0.3.x database is not a supported restore source for v1.0.0.

### Disk-capacity and artifact-retention safety

Building six images, Flux assets, OCI bundles, SBOMs, k3d clusters, database
backups, and Charlie evidence can exhaust the host. Add
`scripts/check-build-capacity.sh` and run it before image builds, integration
suites, backups, and live cutover. Record `df`, Docker/containerd/k3d usage, build
cache, artifact/evidence size, and the projected bytes required. Default hard
gates are at least 20 GiB free and 20% filesystem headroom; measure actual peak
in Wave 10 and raise the threshold if twice the measured peak is larger.

Automated cleanup may remove only regenerable, positively identified targets:

- stopped disposable k3d clusters whose names begin with the test-run prefix and
  whose run metadata says the test completed;
- build cache older than the configured retention and not referenced by the
  current or rollback release;
- untagged images and old Astronomer/Flux test tags outside the current,
  candidate, and one rollback version;
- expired temporary test registries, logs, JUnit files, and generated assets
  under an explicit run-specific temporary/evidence directory;
- package-manager caches that are reproducible and not the source tree.

Never automatically remove a running container/cluster, named volume, database
or backup, Helm release, Kubernetes namespace/PVC, current/rollback image,
registry content referenced by a release manifest, TLS key/Secret, source
credential, Git worktree, untracked user file, `/root`, a workspace root, or an
unresolved path/glob. Print the exact candidates and byte estimate first; for a
live/candidate release retain a cleanup manifest and command output in evidence.
Use engine filters/explicit IDs, not broad `docker system prune -a --volumes` or
recursive deletion.

After cleanup, rerun the capacity check. If safe cleanup cannot produce required
headroom, stop and request additional storage; do not trade away rollback,
backup, TLS, or user data. Add disk/build-cache/registry retention monitoring and
alerts so production does not depend on emergency manual cleanup.

## Observability and operations

### Metrics

Add bounded-label metrics for:

- delivery source resolution duration/result/type and verification result;
- planner duration, candidates, selections, exclusions by enum reason;
- rollouts and rollout clusters by strategy/state/outcome;
- cohort release-to-ack and ack-to-ready latency;
- current deployments by normalized phase and drift;
- assignment snapshot bytes/count, not-modified ratio, delivery latency, stale/
  replay rejection, and protocol errors;
- Flux distribution versions/readiness and unsupported clusters;
- status queue depth/coalescing/drop/full-resync counts;
- credential rotation/revocation state;
- worker claims, fencing conflicts, retries, and outbox age.

Never label by cluster, source URL, target, bundle, user, error message, revision,
or credential. Those belong in scoped API detail/audit.

### Logs, traces, events, dashboards, alerts

- Carry request ID, operation ID, rollout ID, deployment ID, cluster ID, agent
  session ID, and task ID as structured fields where applicable; redact source
  URLs according to policy and never log values/credentials/raw manifests.
- Trace API -> outbox -> worker -> assignment -> agent acknowledgment -> Flux
  Ready using existing OpenTelemetry conventions with sampled high-cardinality IDs.
- SSE resource types are `delivery_source`, `component_bundle`,
  `delivery_target`, `delivery_rollout`, `cluster_deployment`, and
  `cluster_agent`; events contain metadata IDs/state only.
- Replace Argo/fleet dashboards with delivery control-plane, rollout SLO, source
  health, downstream Flux distribution, and cluster-agent dashboards.
- Alert on stuck rollout, exceeded failure budget, rollback failure, assignment
  backlog, stale deployment status, incompatible Flux/agent version, source
  verification failure, credential expiry/revocation, controller outage, and
  status ingestion lag. Every alert links to an Astronomer runbook and UI page.

### Runbooks

Write runbooks for source authentication/signature failure, chart/render error,
Kustomize health timeout, Helm remediation/rollback, drift loop, controller
crash/saturation, agent disconnect, stale assignment, stuck deletion, credential
rotation, Flux distribution canary failure, air-gap mirror drift, database
restore, and v0.3.x-to-v1 fresh reinstall.

## User interface

Create a first-party **Continuous Delivery** navigation area; do not expose
“Argo,” “Flux objects,” or “Fleet” as primary navigation.

### Pages

- **Overview**: deployment/rollout/source health, drift, incompatible clusters,
  recent failures, current system distribution, and actionable alerts.
- **Sources**: scoped list, create/edit auth/trust form, verify operation,
  credential fingerprint/rotation, CA/proxy/workload-identity configuration.
- **Bundles**: stable bundles, immutable versions, resolved refs/digests,
  signature state, values/schema editor, source diff, dependency graph, policy.
- **Targets**: placement builder using labels/groups/explicit clusters,
  authoritative preview table with reasons, strategy builder, maintenance/
  approval policy, suspend/delete.
- **Rollouts**: timeline, cohorts, budgets, approvals, per-cluster table, filters,
  pause/resume/abort/retry/rollback, exact previous/desired revision.
- **Deployment detail**: normalized status, source/reconciler Conditions,
  inventory summary, drift, event history, current/previous versions, safe
  reconcile/suspend actions, advanced sanitized Flux diagnostic drawer.
- **Cluster delivery tab**: Flux/agent compatibility and every deployment on one
  cluster.
- **Cluster agents**: renamed agent health/lifecycle view currently implemented
  under agent-fleet terminology.

### UX rules

- Reuse shared `Page`, `DataTable`, `StatusBadge`, `OperationTimeline`, modal,
  form, YAML, and error components.
- URL/query state stores filters and pagination. Lists are server-paginated; do
  not fetch all deployments into the browser.
- SSE invalidates exact query keys and a bounded polling fallback heals gaps.
- Preview and launch are separate actions. Launch displays source digest,
  selected count, exclusions/blockers, strategy/budgets, and approval consequence.
- Status uses accessible text/icons in addition to color. All operations have
  loading, empty, partial, stale, permission-denied, and error states.
- Raw source credentials, Secrets, full rendered manifests, and arbitrary Flux
  object editing are never available in the browser.
- Delete all `frontend/src/routes/dashboard/argocd/`,
  `frontend/src/components/argocd/`, legacy `dashboard/fleet/`, and
  `components/fleet/` code after replacement tests pass.

## Charlie integration

Astronomer remains the sole action authority. Charlie must not connect to Flux,
the agent tunnel, or downstream Kubernetes for delivery operations.

### Remove

- `astronomer.argocd.self_management_status`
- `astronomer.argocd.self_management_sync`
- `astronomer.fleet_operations.health`
- any imperative fleet-operation create/pause/resume/retry capability
- old `astronomer.agent_fleet.*` names after their replacement aliases are
  removed from the fresh v1 catalog
- `internal/charlie/argocd_capability_adapter.go` and tests
- legacy fleet-operation adapters/queries that do not represent cluster-agent health
- Charlie’s embedded Argo 3.4 pack and Argo fixtures/tests

### Add read capabilities

- `astronomer.delivery.overview`
- `astronomer.delivery.sources`
- `astronomer.delivery.source_get`
- `astronomer.delivery.bundles`
- `astronomer.delivery.bundle_get`
- `astronomer.delivery.targets`
- `astronomer.delivery.target_preview`
- `astronomer.delivery.rollouts`
- `astronomer.delivery.rollout_get`
- `astronomer.delivery.deployments`
- `astronomer.delivery.deployment_get`
- `astronomer.delivery.system_health`
- `astronomer.cluster_agents.summary`
- `astronomer.cluster_agents.list`
- `astronomer.cluster_agents.get`
- `astronomer.cluster_agents.connection_history`

Every response is bounded, project/RBAC filtered, sanitized, and based on
PostgreSQL/API projections—not downstream proxying. Digests may be returned;
credentials, raw values/manifests, Secret names, and unbounded Flux messages may not.

### Add write capabilities

- `astronomer.delivery.rollout_pause`
- `astronomer.delivery.rollout_resume`
- `astronomer.delivery.rollout_approve`
- `astronomer.delivery.rollout_retry_failed`
- `astronomer.delivery.rollout_rollback`
- `astronomer.delivery.deployment_reconcile`

No Charlie capability creates sources/bundles/targets, rotates credentials,
changes placement, deletes/orphans resources, runs platform-scope deployments,
or bypasses a window/approval. Every write is exact-resource scoped, idempotent,
approval-required by default, argument-digest bound, live-RBAC checked, audited,
and post-action verified. Automatic mode may eventually allow a separately
reviewed pause or reconcile policy, but the v1 release ships no delivery write
on the automatic allowlist.

### Charlie repository work

At `/root/astronomer-all/charlie`:

- replace `internal/packs/embedded/argocd/` with a versioned Flux pack matching
  the exact pinned Flux distribution (initially expected `2.9`);
- cover source-controller, kustomize-controller, helm-controller, Conditions,
  source auth/signature verification, Helm remediation/drift, dependency health,
  controller upgrades, and Astronomer’s normalized diagnosis boundaries;
- update `internal/packs/p1_content_test.go`,
  `cmd/charlie/platform_pack_fixtures_test.go`, retrieval fixtures, pack registry,
  docs, and UI names;
- never teach Charlie to run raw Flux CLI/kubectl remediation against a customer
  cluster; remedies invoke typed Astronomer capabilities or provide manual guidance;
- qualify the companion Charlie release against the exact Astronomer capability
  disclosure digest and include both version/digests in release evidence.

## Helm, installation, and tagged-release contract

### Management-plane chart

Remove from `deploy/chart`:

- the `argo-cd` dependency, lock entry, vendored chart archive, license entry,
  values subtree, schema properties, ConfigMap/env projection, HTTP route/proxy,
  NetworkPolicies, notes, tests, and image list entries;
- Argo self-management values, secrets, migration/approval settings, and feature flag;
- old pull-reconcile feature flags. Delivery protocol v2 and local Flux are the
  only supported mode.

Do not add Flux as a management-plane chart dependency. Flux runs on managed
clusters and its distribution is embedded/published as a versioned release
asset. The management chart contains only the server/worker/frontend/migrate
images and configuration needed to serve registration and delivery APIs.

Add chart values for:

- delivery enablement (default true and not a dual-engine selector);
- supported Kubernetes/agent/Flux matrix;
- public/private artifact mirror and registry rewrite map;
- source resolution egress/proxy/CA policy;
- rollout global safety caps and worker concurrency;
- status retention/coalescing;
- built-in bundle OCI repository/digest/trust policy;
- Flux distribution OCI repository/digest/trust policy;
- disconnected install asset location;
- monitoring/SLO thresholds.

Production schema rejects a mutable Flux or built-in bundle tag, unsigned
artifact policy, plaintext credential, unknown registry rewrite, or a source
network policy that would bypass required SSRF validation. Development values
may use local registries but remain explicit.

### Fresh-install preflight

The `v1.0.0` migration/preflight job must query `schema_migrations` and PostgreSQL
catalogs before writing. It proceeds only when the target database is empty or
already at the exact v1 initial-schema checksum. If it sees migration version
`2..159`, any Argo/fleet table, or an unknown non-v1 schema, it exits non-zero
with `fresh_install_required`, leaving all data/PVCs untouched.

The documented transition from v0.3.x is:

1. export Helm values and any human-reference reports desired by the operator;
2. take and verify a database/PVC backup for rollback to the old release only;
3. install v1 under a new release name and preferably a new namespace/database;
4. configure auth/projects/clusters again and use new v1 registration commands;
5. validate delivery on canary clusters;
6. uninstall the old release only after acceptance; deletion of its namespace/
   PVC is an explicit operator action outside Helm hooks.

Never implement a hook that deletes or reformats an old database. `helm upgrade`
against an existing v1 database is supported for future v1 patch/minor releases;
v0.3.x -> v1.0.0 is not.

### Release contents and compatibility manifest

Publish one release manifest that binds:

- Astronomer chart version/digest;
- server, worker, migrate, frontend, agent, and shell image digests;
- Flux version, controller image digests, CRD/API versions, distribution artifact digest;
- built-in bundle artifact and constituent source/image digests;
- minimum/maximum Kubernetes, agent protocol, PostgreSQL, and browser versions;
- Charlie minimum/exact qualified version and capability disclosure digest;
- SBOM/provenance/signature references and source commit.

The chart and registration API read this generated manifest; versions must not
be duplicated manually across Go constants, values, docs, and CI. Add a check
that every image in rendered management/downstream manifests exists in
`deploy/chart/images.txt` and the release manifest.

### Release gates

Tag `v1.0.0` only after:

- clean install from the packaged chart succeeds three times on each supported
  Kubernetes minor with both online and mirrored/air-gapped assets;
- a v0.3.x database is rejected without mutation;
- a fresh v1 database contains exactly the singleton initial migration and no
  Argo/legacy fleet schema;
- two downstream clusters enroll from only the generated command and reach
  compatible agent+Flux Ready;
- public Git, private Git, HTTP Helm, OCI Helm, and signed OCI Kustomize sources
  deploy immutable revisions;
- canary, rolling, approval, pause/resume, failure budget, rollback, deletion,
  disconnect/reconnect, controller upgrade/rollback, and credential rotation pass;
- multi-replica, load, chaos, restore, security, and redaction suites pass;
- `make verify-enterprise VERIFY_SCOPE=all`, live validation, Charlie
  qualification, vulnerability policy, SBOM, signature, and provenance gates pass;
- runtime/release-artifact scans find no Argo or Rancher Fleet dependencies.

## Removal and cleanup inventory

Delete only after replacement characterization exists and all callers have
moved. Historical documentation may retain a short decision record, but shipped
guides, examples, UI, OpenAPI, and runbooks must describe only the new model.

### Delete Argo runtime code and assets

- `cmd/astro/argocd.go`
- `internal/auth/argocd_proxy_token.go`
- `internal/argolabels/`
- `internal/argosecurity/`
- `internal/handler/argocd.go`
- `internal/handler/argocd/`
- every `internal/handler/argocd_*.go` and Argo-only test
- `internal/server/self_manage_argocd*.go`
- `internal/server/self_manage_migration*.go`
- `internal/server/self_manage_values*.go`
- `internal/server/baseline_appsets.go`
- Argo-only registration advancers/listeners and middleware
- `internal/worker/tasks/argocd_*.go`
- `internal/charlie/argocd_capability_adapter*.go`
- `frontend/src/components/argocd/`
- `frontend/src/routes/dashboard/argocd/`
- `frontend/src/lib/argocd*.ts`
- `deploy/chart/charts/argo-cd-9.5.21.tgz`
- `deploy/chart/licenses/argo-helm-APACHE-2.0.txt`
- `scripts/validate-live-argocd*.sh`
- Argo-specific dashboards and active runbooks/docs

Remove Argo branches from shared files including server construction/routes,
configuration, baseline/catalog, CRD wiring, cluster responses/diagnostics,
platform settings, navigation/search/command palette, query keys, audit detail,
RBAC roles, OpenAPI, error catalog, generated SDK, chart/templates/values/schema,
Makefile, CI, release scripts, image/license/SBOM lists, reconciliation/control-
plane docs, Charlie knowledge generation, and tests/goldens.

### Delete legacy imperative fleet-operation code

- `cmd/astro/fleet.go`
- `internal/handler/fleet_dispatcher*.go`
- `internal/handler/fleet_operations*.go`
- `internal/worker/tasks/fleet_orchestrate*.go`
- `internal/worker/tasks/fleet_selector*.go` after the pure selector semantics
  are covered in `internal/delivery/placement`
- `internal/worker/tasks/fleet_sweep*.go`
- imperative fleet-operation Charlie adapters/queries/tests
- `frontend/src/components/fleet/`
- `frontend/src/lib/api/fleet-operations*.ts`
- `frontend/src/routes/dashboard/fleet/`
- `deploy/dashboards/fleet-cve-rollup.json` if it is tied to the old operation model
- `scripts/scale-test/spawn-fleet.sh`
- old fleet operation docs/patch plans from active product documentation

### Rename useful agent-health code

- `internal/handler/agent_fleet.go` -> `internal/handler/cluster_agents.go`
- corresponding interfaces/tests/routes/OpenAPI/SDK/frontend/query keys/docs
- `internal/charlie/fleet_capability_adapter.go` -> a narrowly scoped
  `cluster_agent_capability_adapter.go` containing only agent/tunnel health
- `astronomer.agent_fleet.*` -> `astronomer.cluster_agents.*`
- operator-visible “fleet” text -> “cluster agents” or “managed clusters”

Do not rename generic Go concepts in unrelated code where “fleet” is ordinary
English and not user-facing, but the final runtime grep should be nearly empty
and every remaining match must have an allowlisted reason.

### Remove obsolete CRDs and pull applier

- Remove `ClusterBaseline`, `ComponentBundle`, and `GitOpsTarget` Go types,
  reconcilers, webhook/validation helpers, schemes, CRD chart templates, RBAC,
  envtests, docs, and examples.
- Remove Argo-shaped `ArgoApplicationStatus`, ApplicationSet generator helpers,
  GVK/GVR constants, and cluster baseline ownership decision paths.
- Remove `internal/server/desired_state*.go`, old protocol payloads/message types,
  `internal/agent/reconcile*.go`, `PullReconcileEnabled`, interval settings, and
  feature-flag branches after delivery protocol v2 is live.
- Keep cluster registration, agent install rendering, decommission pause guards,
  authenticated tunnel, heartbeat, state subscriber, and capability reporting;
  adapt them to system/delivery reconciliation.

### Database migration squash and generated cleanup

At Wave 12, delete every existing migration SQL file from
`internal/db/migrations/` and replace them with only:

```text
001_initial.up.sql
001_initial.down.sql
initial_schema_test.go
```

Additional focused schema tests may remain if named by invariant rather than old
migration number. Delete/refactor every `migration_NNN_test.go` and obsolete
migration guard fixture. Regenerate all sqlc models/queries after removing Argo
and old fleet SQL files. The new initial migration includes only tables used by
the final code and required seed data.

### Dependency and artifact cleanup

After code deletion:

- run `go mod tidy` and remove Argo-only modules;
- add only Flux API modules actually used for typed scheme/status handling, at
  versions aligned with the pinned distribution. Do not import controller
  implementation modules;
- regenerate frontend lockfile after deleting unused Argo-specific packages;
- update chart dependency lock/vendor contents, images, licenses, SBOM inputs,
  API SDK, route tree, docs indexes, error codes, and Charlie knowledge artifact;
- ensure no bundled `argocd`, `quay.io/argoproj`, `fleet.cattle.io`, or
  `rancher/fleet` image/package/resource remains.

## Scope

This is a program plan, so each wave must narrow its PR file list. The complete
program may modify only these roots/files in Astronomer:

- `cmd/agent`, `cmd/astro`, `cmd/server`, `cmd/worker`
- `deploy/agent`, new `deploy/flux`, new `deploy/bundles`, `deploy/chart`, and
  delivery/agent dashboards
- `docs`, `advisor-plans/README.md`
- `frontend/src`, frontend tests/config/lockfile only as required
- `internal/agent`, `internal/charlie`, `internal/config`, `internal/crd`,
  `internal/db`, new `internal/delivery`, delivery-related handler/RBAC/server/
  worker/observability/event code and tests
- `pkg/protocol`, generated `pkg/astroclient`, version/release manifest packages
- `scripts`, `.github/workflows`, `Makefile`, `go.mod`, `go.sum`, API/codegen config

Charlie scope is limited to its Astronomer connector/capability contracts,
Flux/platform knowledge packs, related docs/frontend/tests, and release metadata.
Do not touch `/root/astronomer-all/charlie/plans/`.

Out of scope unless a wave proves a direct compile/schema dependency:

- cluster provisioning providers, backups, compliance, monitoring/logging engine,
  authentication providers, billing, and unrelated workload CRUD;
- unrelated user changes in either repository;
- any new generic plugin framework, policy language, message bus, database, or UI framework;
- any Rancher Fleet, OCM, Argo, or generic Flux UI integration.

## Git and delivery workflow

- Program branch: `advisor/007-flux-native-delivery`; use one short-lived branch/
  PR per wave rather than one unreviewable mega-PR.
- Astronomer commits follow the repository’s observed concise imperative style;
  prefix scopes where useful, for example `delivery: add immutable rollout planner`.
- Every PR states plan task IDs, schema/API/protocol changes, rollback, exact
  verification commands, and evidence artifact paths.
- Merge additive foundations before callers; delete old paths only in Wave 9;
  squash migrations only in Wave 12 after all schema changes settle.
- Do not push, publish artifacts, tag, or open PRs unless the operator separately
  authorizes those external actions.

## Implementation waves and granular tasks

Each task ID is a reviewable commit/PR unit unless noted. A failed wave exit gate
blocks later waves. Temporary feature flags are allowed only to keep additive
work unreachable before cutover; all dual-engine/legacy flags are deleted in
Wave 9.

### Wave 0 — Freeze contracts and establish characterization

#### W0-01: Record the architecture decision

Create `docs/architecture/decisions/flux-native-delivery.md` with the ownership
boundary, PostgreSQL authority, local Flux topology, typed agent protocol,
fresh-install-only schema, security model, rejected alternatives, and exact
upstream references from this plan. Update the architecture index and
`docs/control-plane-state-contract.md`.

**Verify**:

```bash
rg -n "Rancher Fleet.*not|local Flux|PostgreSQL.*authoritative|fresh.install" docs/architecture docs/control-plane-state-contract.md
```

Expected: all four decisions are explicit; no decision implies Fleet is installed.

#### W0-02: Inventory old surfaces mechanically

Add `scripts/check-legacy-delivery-surface.sh` with an allowlist for historical
decision/migration context during development. It must scan Go, TypeScript,
OpenAPI, chart render, image/license lists, CLI help, routes, generated SDK,
runtime docs, and built images for Argo and legacy fleet identifiers. Start in
report mode; Wave 9 changes it to fail mode.

Record the report under `docs/assurance/flux-migration/legacy-baseline.txt` with
file counts, not source/credential contents.

**Verify**: `./scripts/check-legacy-delivery-surface.sh --report` exits 0 and
lists known Argo and fleet-operation surfaces; a fixture containing
`fleet.cattle.io` is detected.

#### W0-03: Characterize reusable foundations

Add focused tests, without changing behavior, for:

- authenticated tunnel cluster binding, reconnect, backpressure, stale session;
- current desired-state request/response routing and decommission pause;
- agent heartbeat capability/version projection;
- project/cluster/group label selection semantics;
- operation audit/SSE/idempotency conventions;
- Fernet credential envelope and redaction;
- worker leader/claim patterns;
- chart registration manifest ordering;
- Charlie capability approval/verification.

Use existing tests in `internal/agent`, `internal/tunnel`, `internal/handler`,
`internal/worker/tasks`, and `internal/charlie` as patterns. These tests create
the safety net for later extraction and deletion.

**Verify**: `make test` and `make charlie-contract-check` pass with no production
code changes in W0-03.

#### W0-04: Define support and SLO matrices

Create machine-readable `deploy/release/compatibility.yaml` and JSON Schema with
supported Kubernetes minors, Flux version/APIs/controller images, agent protocol,
PostgreSQL, source types, maximum payloads/artifacts, and SLO targets. Generate a
human table into docs; do not maintain two hand-written copies.

**Verify**: a script validates schema, rejects overlapping/empty version ranges,
and proves every advertised Kubernetes minor is present in CI/live matrix jobs.

**Wave 0 exit gate**: baseline tests pass, decision is approved, legacy inventory
is reproducible, and product/security owners have approved the fresh-install and
platform-scope contracts.

### Wave 1 — Pin and qualify the Flux execution substrate

#### W1-01: Select the exact Flux patch

Starting from upstream `v2.9.3`, verify:

- supported Kubernetes range covers Astronomer’s matrix;
- source-controller, kustomize-controller, and helm-controller API/storage
  versions align with Astronomer’s Go/Kubernetes dependencies;
- upgrade/rollback procedures and known CVEs satisfy release policy;
- required fields exist: Git commit pin, OCI digest/signature verification,
  Kustomization prune/wait/health/dependencies/service-account, Helm v2
  remediation/test/drift/service-account;
- images are multi-arch and available for mirroring.

Record the decision and upstream release/checksum URLs in `deploy/flux/README.md`.
If the current Flux release does not support the product Kubernetes range, stop
and revise the support matrix or select a supported Flux maintenance line; do
not claim compatibility based on a successful single-cluster smoke test.

**Verify**: `scripts/verify-flux-version.sh` checks the version, checksums,
controller/API list, digest pins, and compatibility file.

#### W1-02: Generate the distribution deterministically

Implement `scripts/update-flux-distribution.sh`, `deploy/flux/kustomization.yaml`,
hardening patches, checksums, provenance, license, and generation check described
above. Commit generated `install.yaml`; do not hand-edit it.

**Verify**:

```bash
./scripts/update-flux-distribution.sh --check
kubectl apply --dry-run=client -f deploy/flux/install.yaml >/dev/null
./scripts/check-k8s-security-contexts.sh deploy/flux/install.yaml
```

Expected: zero diff, valid objects, only three controller Deployments, all
images digest-pinned, hardening/RBAC/network assertions pass.

#### W1-03: Add typed API dependencies narrowly

Add Flux API modules for source v1, Kustomize v1, and Helm v2 only. Register
their schemes in a new `internal/delivery/fluxapi` package. Do not import Flux
controller internals, CLI packages, server code, or Kubernetes clients beyond
what Astronomer already uses.

Add a dependency guard test that reads `go list -deps` and fails on forbidden
Flux controller implementation/CLI modules.

**Verify**: `go mod tidy`, `make test`, `make lint`, and the dependency guard pass.

#### W1-04: Prove Flux behavior in disposable clusters

Add an integration harness that creates a k3d cluster, applies the generated
distribution, and tests:

- signed OCI source -> Kustomization Ready;
- exact Helm chart -> HelmRelease Ready and tests pass;
- drift repair for both renderers;
- suspend/resume;
- source auth failure and recovery;
- namespace-scoped service-account denial of cluster-scoped content;
- controller restart and CRD/API discovery;
- distribution rollback to the previous tested patch when supported.

The harness uses local fixtures/registry and contains no external credentials.

**Verify**: `make test-flux-integration` passes twice consecutively from clean
clusters and stores JUnit/log/version evidence under a temporary output path.

**Wave 1 exit gate**: exact distribution and APIs are pinned, generated,
hardened, dependency-bounded, and behaviorally qualified.

### Wave 2 — Build the delivery domain and clean API contracts

#### W2-01: Add temporary development schema

Add temporary migrations and `internal/db/queries/delivery_*.sql` for every table
in “Data model.” Use constraints for every enum/state, unique idempotency and
identity keys, foreign keys, partial worker indexes, and non-secret comments.
Add schema tests for project isolation, immutable versions/rollouts, stale status
CAS, one current deployment per target/cluster, and valid transitions.

These migrations are intentionally squashed in Wave 12.

**Verify**: `make migrate-up`, `make sqlc-check`, schema tests, `make migrate-down`
to zero, and re-up all pass against an empty disposable PostgreSQL database.

#### W2-02: Implement domain types and state transitions

Create `internal/delivery/model`, `internal/delivery/store`, and
`internal/delivery/state`. Keep handlers/workers out. Define validated types for
sources, immutable bundle versions, placements, strategies, rollout/deployment
states, Conditions, revisions, digests, and events. Put transition tables in one
package; reject impossible transitions with typed error codes.

**Verify**: table-driven tests cover every allowed/disallowed transition,
terminal idempotency, pause/resume, abort, rollback, delete, stale fence, and
counter derivation; `go test -race ./internal/delivery/...` passes.

#### W2-03: Implement credential storage

Create a delivery credential service using the existing encryption/key-version
and redaction primitives. The service accepts write-only typed credential input,
stores ciphertext separately, returns only metadata, supports rotation/revocation,
and gives resolvers/assignment builders narrowly scoped decrypt methods.

Extend plaintext credential migration only if required during development; it
must not survive the final greenfield migration because v1 never stores plaintext.

**Verify**: tests prove database/task/audit/API/log representations contain none
of fixture secret bytes, old keys decrypt through rotation, revoked credentials
cannot be fetched, and read DTOs have no secret field.

#### W2-04: Implement source resolution

Create resolver interfaces and Git/OCI/Helm implementations plus durable Asynq
tasks, transactional outbox dispatch, retry classification, SSRF policy, bounded
fetch/unpack, trust verification, and immutable resolution records. Ensure
source update/credential rotation does not mutate an already resolved bundle version.

**Verify**: local Git/HTTP/OCI fixtures cover public/private auth, redirects,
proxy/CA, mutable ref -> immutable pin, signature accept/reject, digest mismatch,
retryable/permanent errors, size/path attacks, cancellation, and redaction.
`go test -race ./internal/delivery/resolver/... ./internal/worker/tasks/...` passes.

#### W2-05: Define OpenAPI before handlers

Add schemas/endpoints/error codes for the full REST resource table, including
pagination, optimistic concurrency, idempotency, preview digest, typed strategies,
normalized conditions, and write-only credential shapes. Generate the Go SDK
and add compile-time handler interface assertions.

**Verify**: `make verify`, `make sdk`, and `git diff --exit-code` after a second
generation pass. Contract tests reject secret response fields and unbounded
free-form strategy/source unions.

**Wave 2 exit gate**: domain/schema/credentials/resolution/OpenAPI pass without
any Argo or legacy fleet path calling the new packages.

### Wave 3 — Implement placement, rollout, and durable HA orchestration

#### W3-01: Replace selector logic with pure placement

Implement `internal/delivery/placement` and migrate relevant semantic tests from
`fleet_selector_test.go`. Add project authorization, group membership,
capability/compatibility/decommission filters, explicit exclusions, match
reasons, stable ordering, all-clusters safety, and canonical preview digest.

Use batch SQL projections; no per-cluster query in the planner.

**Verify**: unit/property tests cover empty, 10k, label operators, group+label,
duplicates, unauthorized explicit IDs, membership drift, deterministic digest,
and exact parity between preview/launch. A query-count test remains O(1) queries
per preview page/snapshot, not O(clusters).

#### W3-02: Create rollout plans transactionally

Implement create/update target and rollout planning services. Freeze source,
bundle, target generation, placement rows/digest, previous known-good revisions,
strategy, approvals, and deadlines in one transaction. Enforce idempotency and
`preview_stale` conflict.

**Verify**: concurrency tests submit identical/different idempotency keys,
mutate membership between preview/launch, race target updates, and prove exactly
one immutable rollout with the intended snapshot.

#### W3-03: Implement the rollout scheduler

Create small workers for plan resolution, cohort release, deadline/failure-budget
evaluation, rollback creation, deletion, and counter repair. Use the durable
claim/fencing/outbox rules above. Keep external effects limited to committed
cluster-deployment desired generations and wake-up notifications.

**Verify**: deterministic clock tests cover all strategies and states; multi-
worker tests kill a worker after claim/commit/notification and prove no duplicate
generation or skipped target. Run with `-race` and real PostgreSQL transactions.

#### W3-04: Implement maintenance and approval gates

Wire the real maintenance-window store and existing approval/action policy.
Windows gate new releases; approvals bind rollout ID, target generation,
placement digest, bundle version/spec digest, strategy digest, actor, expiry, and
decision ID. Any plan change invalidates approval.

**Verify**: timezone/DST/overlap/emergency override tests; approval replay,
wrong-resource, changed-plan, expiry, double decision, and RBAC tests all pass.

#### W3-05: Implement status-driven advancement

Create status ingest/CAS service and outbox trigger. A Ready observation must
match cluster, authenticated session, deployment generation, spec digest, and
expected immutable revision before it advances a cohort. Coalesce repeats and
store bounded transitions.

**Verify**: stale session/generation/spec/revision, duplicate/out-of-order,
disconnect, drift, transient Ready, min-ready timer, failed and rollback states
all behave as defined; fuzz invalid status payloads without panic or state advance.

**Wave 3 exit gate**: a simulation of 10,000 fake clusters executes canary ->
approval -> rolling -> success and failure -> rollback with exact counters and
no duplicate release under three workers.

### Wave 4 — Replace the raw desired-state applier with protocol v2

#### W4-01: Define and fuzz protocol v2

Add message types/DTOs, version negotiation, canonical snapshot hashing, bounds,
not-modified response, deletion tombstones, controller inventory, status sequence,
and stable error codes in `pkg/protocol`. Generate any Charlie/agent contracts
from one source rather than duplicating structs.

**Verify**: round-trip/golden/backward-rejection tests and fuzzers cover unknown
unions/fields, oversized/deep values, invalid identifiers/digests, duplicate IDs,
canonical ordering, and decompression bounds. `make test` passes.

#### W4-02: Implement the server delivery-state provider

Create `internal/server/delivery_state_provider.go` and tunnel hub handlers. Query
only authorized released current assignments, build a full canonical snapshot,
decrypt credentials only when the ETag/epoch requires transfer, return not-
modified otherwise, and persist receipts/status through the store.

Push only a lightweight `DELIVERY_RECONCILE_V2` wake-up when desired state
changes. The agent always pulls the authoritative snapshot.

**Verify**: provider tests cover disconnected/reconnect, no assignments, 10k
assignments/page/bounds, credential epoch, tombstone retention, stale session,
lost wake-up, and concurrent target changes. Tunnel backpressure tests still pass.

#### W4-03: Implement assignment validation/materialization

Build the three agent delivery packages described earlier with typed Flux API
objects, deterministic names, source Secrets/trust, service accounts/RBAC,
server-side apply, inventory, and guarded prune. Validation completes for the
entire snapshot before any object changes. Apply source/credential/RBAC before
reconciler; delete in safe reverse order.

**Verify**: fake-client and envtest golden tests cover every source/renderer/auth/
trust/scope combination, conflicts, partial apply (no prune), stale generation,
foreign/unlabeled objects, namespace escape, cluster-scope denial, deletion,
orphan, credential rotation, and deterministic output.

#### W4-04: Implement Flux observation

Use shared dynamic/typed informers filtered by Astronomer labels. Normalize
Source and HelmRelease/Kustomization Conditions, revision/digest, inventory, and
warnings; debounce/coalesce and send session-sequenced status. Rebuild full state
on start/reconnect and periodically resync.

**Verify**: informer tests cover Ready, Reconciling, Stalled, Drifted, suspended,
missing source, generation lag, deletion, controller absent/CRD absent, rapid
event storms, disconnect queue saturation, and full reconnect snapshot. No raw
Secret/object content enters status.

#### W4-05: Integrate agent lifecycle and capabilities

Start protocol v2 after authenticated tunnel connection; advertise delivery
protocol, source/renderer, scope, Flux API, and Kubernetes capabilities in
heartbeat. Decommission pauses new apply and resolves delete/orphan before agent
teardown. Registration profiles make required delivery permissions explicit.

**Verify**: `cmd/agent` integration tests prove startup ordering, readiness,
reconnect, graceful shutdown, decommission race prevention, old-agent rejection,
and capability blockers in placement.

**Wave 4 exit gate**: with fake control plane plus real k3d Flux, one agent
applies, observes, updates, suspends, deletes, reconnects, and reports correctly;
the old raw workload-manifest applier has no new callers.

### Wave 5 — Ship cluster bootstrap, system lifecycle, and built-in bundles

#### W5-01: Produce the single registration manifest

Refactor `deploy/agent` renderers and registration endpoints to include the
ordered Flux+agent bootstrap described above. Support online OCI and a fully
rendered offline asset. Include exact release manifest/digests and no live
credential in URLs/logs/history.

**Verify**: golden tests validate object order, CRDs before controllers, exact
RBAC, images/digests, namespaces/network policy, registration-token handling,
and successful `kubectl apply` into an empty supported cluster.

#### W5-02: Implement signed system reconciliation

Publish the pinned Flux distribution as a signed OCI artifact and create the
isolated system OCIRepository/Kustomization. Add server-side system rollout
policy and agent inventory status. Workload assignments cannot alter system objects.

**Verify**: tests reject unsigned/wrong-identity/digest artifacts, workload refs
to system namespace, and incompatible upgrades. Live canary upgrades and rolls
back controller Deployments without losing workload reconciliation.

#### W5-03: Package built-in platform bundles

Move baseline component definitions from Go/ApplicationSets into
`deploy/bundles/`, publish the signed OCI artifact, seed delivery sources/bundles/
versions/targets in the fresh schema, and make registration’s “install platform
components” choice create normal delivery rollouts after agent+Flux Ready.

Every built-in chart and image is exact-version/digest pinned and appears in the
release manifest/SBOM/license inventory.

**Verify**: artifact generation is deterministic; each built-in bundle installs,
upgrades, drift-repairs, and deletes on every supported Kubernetes minor; opt-out
creates no target/assignment.

#### W5-04: Support mirrors and air-gap

Add release tooling to mirror Astronomer images, Flux controller images,
distribution artifact, built-in bundle artifact, charts, and referenced images;
emit a signed mapping manifest; validate all rewrites before install.

**Verify**: an isolated k3d network with only the local registry enrolls a
cluster and installs built-in bundles. Packet/DNS logs show no public-registry or
GitHub egress.

**Wave 5 exit gate**: a new downstream cluster reaches agent+Flux Ready and
optional built-ins using only one generated command in both online and air-gap tests.

### Wave 6 — Expose the product API, CLI, RBAC, audit, and operations

#### W6-01: Implement delivery handlers

Create small handlers under `internal/handler/delivery/` grouped by source,
bundle, target, rollout, deployment, and system compatibility. Handlers parse/
authorize/validate and call domain services; they do not query Flux, decrypt
credentials, calculate placement, or fan out.

Register the REST table exactly, enforce project scope, idempotency and ETags,
and add typed error codes with safe messages.

**Verify**: route golden and OpenAPI conformance tests cover every method,
anonymous/forbidden/cross-project cases, invalid bodies/sizes, idempotency,
optimistic concurrency, pagination/filter/sort, and no secret echo. `make verify` passes.

#### W6-02: Add RBAC, audit, operations, and SSE

Seed the resource/verb split described in Security. Use the existing operation
and event conventions for async verify/resolution/rollout/rotation/system work.
Publish scoped metadata-only SSE after commit. Extend audit detail renderers with
digests/counts/reason codes, never full specs.

**Verify**: matrix tests for built-in roles and custom permissions; read/write/
approval/platform/orphan/credential boundaries; cross-project SSE filtering;
transaction rollback emits neither audit success nor SSE; audit/redaction fixture
contains no secret/source values.

#### W6-03: Implement `astro delivery` and `astro cluster-agent`

Build the CLI tree from generated SDK types. Add declarative YAML/JSON apply,
dry-run/preview, watch with reconnect, table/json/yaml output, non-interactive
safety, and stable exit codes. Remove direct Argo/Fleet concepts from help.

**Verify**: CLI golden/help tests, fake-server tests, idempotent apply, stale
preview conflict, approval, watch reconnect, permission errors, and secret
redaction pass. `astro --help` contains Continuous Delivery and Cluster Agents,
and no `argocd` or legacy `fleet` command.

#### W6-04: Update cluster registration and diagnostics API

Cluster responses expose agent/Flux compatibility and normalized delivery
summary. Registration progress is: created -> bootstrap generated -> agent
connected -> Flux installing -> Flux ready -> platform rollout -> ready. Rename
agent-fleet endpoints and response fields cleanly; there are no aliases in v1.

**Verify**: handler/OpenAPI/frontend contract tests cover read-only agent profiles,
missing capability, incompatible Kubernetes, system failure/retry, and no Argo
diagnostic fields.

**Wave 6 exit gate**: complete API/CLI operations work against simulated agents,
and API/RBAC/audit/SSE/generated SDK gates pass.

### Wave 7 — Build the first-party Continuous Delivery UI

#### W7-01: Add typed frontend API/query layer

Generate/import exact OpenAPI types; add query-key factories, hooks, SSE
invalidation, pagination, optimistic updates only where safe, and common phase/
condition mappings. Do not duplicate backend state machines in the browser.

**Verify**: hook tests cover scope, filters, SSE, stale polling, abort signals,
errors, and mutation invalidation; TypeScript has no new `any`/unchecked cast for
delivery DTOs.

#### W7-02: Build Sources and Bundles

Implement the pages/forms/detail flows under
`frontend/src/routes/dashboard/delivery/` and
`frontend/src/components/delivery/`. Use schema-driven values where available.
Credentials are write-only with replace/rotate controls; trust/proxy/CA errors
are actionable and sanitized.

**Verify**: component tests cover all source kinds/auth modes, verification
states, immutable version behavior, schema validation, signature failures,
rotation, permissions, and accessibility.

#### W7-03: Build Targets and authoritative preview

Implement label/group/explicit selection, all-cluster confirmation, exclusion/
blocker table, immutable source summary, strategy/budget/window/approval forms,
and launch confirmation bound to preview digest.

**Verify**: tests cover zero matches, unauthorized/excluded/incompatible,
10k-row server pagination, stale preview, dangerous all-cluster flow, every
strategy, keyboard/screen-reader use, and no client-side selector reimplementation.

#### W7-04: Build Rollouts and Deployments

Implement overview/list/detail/timeline/cohort/per-cluster views and typed action
dialogs. Add cluster delivery tab and advanced sanitized Flux diagnostics. Reuse
shared operation/status/table components and virtualize only after measurement.

**Verify**: tests cover every state/action/permission, partial/stale/disconnected,
rollback failure, deletion/orphan, SSE gap recovery, deep links, and accessibility.

#### W7-05: Rename Cluster Agents and navigation

Move useful agent health/lifecycle UI away from “fleet,” update sidebar, command
palette, global search, breadcrumbs, dashboards, settings, and cluster pages.

**Verify**: route-tree generation, unit tests, and navigation E2E pass; user-
visible runtime strings contain no Argo or ambiguous Fleet label.

**Wave 7 exit gate**:

```bash
npm --prefix frontend run type-check
npm --prefix frontend test -- --run
npm --prefix frontend run lint
npm --prefix frontend run build
npm --prefix frontend run test:e2e
```

All pass with source/bundle/target/rollout/deployment/cluster-agent E2E coverage.

### Wave 8 — Replace Astronomer and Charlie intelligence contracts

#### W8-01: Replace Astronomer Charlie adapters

Implement read/write capability adapters listed above using delivery services/
queries and existing action guard patterns. Rename cluster-agent adapter and
remove Argo/legacy fleet-operation descriptors, schemas, policy seeds, context
registry entries, work-pipeline domains, visibility summaries, and tests.

**Verify**: every new capability has positive, empty, partial, forbidden,
cross-project, timeout, oversized, redaction, approval, idempotency, and verify
tests as appropriate. Removed capabilities cannot be discovered or invoked.
`make charlie-contract-check` and the full Astronomer test suite pass.

#### W8-02: Update Charlie’s Flux knowledge pack

In the Charlie repo, replace the Argo pack with exact Flux content and update
registries/fixtures/docs/UI/tests. Include Astronomer-specific decision boundaries:
Charlie diagnoses normalized status; it never bypasses Astronomer to operate Flux.

**Verify**: Charlie’s live Makefile/CI pack, retrieval, contract, unit, frontend,
and generated-artifact checks pass. Searches find no active Argo pack/reference
except a migration/release note explicitly allowlisted.

#### W8-03: Perform contract qualification

Run Astronomer locally with the final capability catalog, connect a Charlie
candidate, rediscover/acknowledge the exact disclosure digest, and exercise:

- delivery overview/source/bundle/target/rollout/deployment reads;
- project denial and response bounds/redaction;
- propose -> approve -> execute -> verify pause/resume/retry/rollback/reconcile;
- changed/expired approval rejection;
- platform-scope, credential, create/delete/orphan bypass rejection;
- disconnect/Flux failure diagnosis and runbook retrieval.

**Verify**: both repositories retain content-free evidence tying versions,
commits, disclosure digest, action IDs/result codes, and zero leaked fixture
secrets. All expected product-call counts and denials match.

**Wave 8 exit gate**: companion Charlie candidate is qualified against exact
Astronomer candidate; neither side contains an Argo or legacy fleet operation contract.

### Wave 9 — Cut over and delete every old delivery path

#### W9-01: Switch all callers to delivery

Wire server/worker/agent constructors, scheduler, routes, chart, registration,
cluster summaries, dashboards, UI/nav, CLI, Charlie, docs, and tests exclusively
to the new packages. Make delivery default and unconditional in v1.

**Verify**: all compile/test/generation gates pass before deletion; runtime route
golden contains `/delivery` and no `/argocd` or `/fleet-operations`.

#### W9-02: Delete Argo code/assets

Execute the Argo removal inventory. Delete the chart dependency/archive, UI
proxy and route, client/handler/worker/self-management/controller code, CRD
fields/helpers, CLI/UI, metrics/settings/audit/RBAC/OpenAPI/SDK surfaces, scripts,
tests, and active docs. Replace useful generic docs with delivery equivalents.

Do not merely leave dead files unreferenced.

**Verify**:

```bash
./scripts/check-legacy-delivery-surface.sh --fail
helm dependency list deploy/chart
helm template astronomer deploy/chart | rg -i 'argocd|argoproj' && exit 1 || true
```

Expected: no non-allowlisted runtime match and no Argo chart/image/resource.

#### W9-03: Delete legacy imperative fleet operations

Execute the legacy fleet-operation removal inventory, remove routes/scheduler/
queries/generated code/UI/CLI/Charlie/dashboard/scale scripts, and use only
delivery rollouts. Rename agent-health paths to cluster agents.

**Verify**: legacy checker fails on fixtures but passes the source/release;
OpenAPI/SDK/CLI/route/UI searches contain no `fleet_operations`,
`fleet-operations`, `FleetOperation`, or `agent_fleet`.

#### W9-04: Delete old CRDs and raw pull applier

Remove ClusterBaseline/ComponentBundle/GitOpsTarget management CRDs, Argo-shaped
reconcilers, desired-state renderer/payload/applier, flags, RBAC, chart templates,
envtests, docs, and generated manifests. Keep/adapt only reusable agent/tunnel/
cluster/project/profile foundations.

**Verify**: rendered management chart contains no removed CRD; Go scheme tests
do not register them; protocol contains only v2 delivery/system payloads; a
v0.3.x agent receives the explicit reenrollment error.

#### W9-05: Tidy generated/dependency/artifact surfaces

Run SQL/API/route/error/docs/Charlie generators, `go mod tidy`, frontend install/
lock update, chart dependency update, images/license/SBOM extraction. Inspect the
diff; generated deletions must correspond to removed contracts.

**Verify**: second generation produces zero diff and
`make verify-enterprise VERIFY_SCOPE=all` passes.

**Wave 9 exit gate**: one delivery engine remains; legacy checker is mandatory
in CI fail mode; no Argo or Rancher Fleet runtime/release dependency and no old
imperative fleet operation surface exists.

### Wave 10 — Enterprise hardening, observability, scale, and chaos

#### W10-01: Complete security controls and assessment

Threat-model source->resolver->DB->assignment->agent->Flux->workload and status
return, then implement every control/test in “Security and tenancy.” Run SAST,
dependency/container/chart/manifest scans, secret scanning, fuzzers, and a
manual RBAC/admission review. Resolve all critical/high reachable findings; do
not waive a cluster-admin or credential-leak path for schedule.

**Verify**: security report maps threats to tests/evidence, contains no secrets,
and release policy passes.

#### W10-02: Add metrics/dashboards/alerts/runbooks

Implement the observability section and dashboard/runbook deep links. Test metric
label bounds and redaction. Add alerts to both management and downstream
monitoring integration without requiring notification-controller.

**Verify**: promtool/dashboard schema/runbook-link tests pass; injected failures
fire and clear expected alerts; metrics have no forbidden high-cardinality labels.

#### W10-03: Run load and soak tests

Extend scale fixtures to simulate 10k clusters/100k deployments, three server/
worker replicas, placement, cohort release, status storms/coalescing, reconnect,
and source resolution. Measure CPU/memory/DB connections/query plans/queue age/
latency/disk/network. Run at least a 24-hour soak at representative load.

**Verify**: all Capacity targets pass and reports include environment, commit,
dataset, commands, raw summaries, p50/p95/p99, error rate, and resource peaks.

#### W10-04: Run failure and recovery drills

Kill/restart replicas/controllers/agents/PostgreSQL connections/registry/source
mid-operation; partition tunnel/source/API; inject stale/out-of-order frames;
fill queues; rotate/revoke credentials; fail canary and rollback; restore backup.

**Verify**: no duplicate generation/action, cross-project leak, unsafe prune,
lost terminal state, availability-budget violation, or unbounded recovery. RTO/
RPO and alert/runbook evidence meet documented targets.

#### W10-05: Implement safe disk retention and capacity gates

Add the disk script/retention rules described above to Makefile, integration
harness, release pipeline, and runbooks. Measure peak build/test/cutover disk use,
set headroom to at least twice peak, and exercise cleanup only against disposable
fixtures plus unreferenced caches/images.

**Verify**: tests prove protected running/current/rollback/volume/backup/TLS/
workspace items never become candidates; dry-run and apply report the same IDs;
capacity is rechecked; a simulated insufficient-safe-space state stops cleanly.

**Wave 10 exit gate**: security, SLO, scale, 24-hour soak, chaos/restore,
observability, and disk-capacity evidence is reviewed and passes.

### Wave 11 — Full new-user and release-candidate live acceptance

#### W11-01: Build release-candidate artifacts

Run the safe capacity check, build all images, Flux distribution and built-in
bundle OCI artifacts from clean source, generate release manifest/SBOM/
provenance/signatures, and mirror them into the test registry. Use one immutable
candidate version across all tests.

**Verify**: clean rebuild is reproducible, signatures/digests match manifest,
image/license lists are complete, scans pass, and current+rollback images remain retained.

#### W11-02: Test management plane as a new user

Install the packaged chart into a new namespace/database using only released
values/schema/docs. Configure TLS/auth through supported values, create a user/
project/group/source/bundle/target through public surfaces, and never use an
internal SQL/kubectl shortcut to make the product work.

**Verify**: all pods/PDBs/NetworkPolicies/metrics Ready, browser and CLI login
work through HTTPS, certificate/hostname are valid, fresh DB is version 1, and
no Argo/legacy schema/resource/image exists.

#### W11-03: Enroll two clean downstream clusters

Use only each generated registration command. One cluster is online; one uses
the mirrored/air-gap path. Validate agent+Flux inventory and RBAC/hardening.

**Verify**: both reach Ready, no direct control-plane kubeconfig exists, online
and offline artifacts match release digests, and the air-gap cluster makes no
undeclared egress.

#### W11-04: Run the end-to-end delivery matrix

Exercise every source type, renderer, auth/trust mode, placement/group change,
strategy, approval/window, pause/resume/abort/retry/rollback, drift, disconnect,
controller restart/upgrade/rollback, credential rotation/revocation, deletion/
orphan, project denial, backup/restore, UI, CLI, SSE, metrics, alerts, and runbook.

Add `scripts/validate-live-delivery.sh` and `make validate-live-delivery`. It must
be rerunnable/idempotent, use a unique run prefix, clean only its own fixtures,
redact output, and emit machine-readable evidence.

**Verify**: three consecutive clean runs from newly created clusters pass.

#### W11-05: Qualify Charlie against the live candidate

Connect the companion Charlie candidate through the public TLS endpoint and run
the entire W8-03 matrix against real rollout/agent/Flux conditions. Fix any
Astronomer or Charlie code/contract/pack issue discovered, rebuild both affected
artifacts, and restart the three-run counter for the impacted acceptance suite.

**Verify**: exact versions/disclosure digest/actions/results recorded; reads and
approved writes behave correctly; forbidden paths remain zero; no secret/raw
manifest/tunnel access; both repos’ gates pass after every fix.

**Wave 11 exit gate**: three clean full candidate runs, including Charlie, with
no waiver or manual internal repair.

### Wave 12 — Squash the entire database history into one v1 migration

This wave is deliberately last among code/schema feature work. Freeze schema
changes before starting it.

#### W12-01: Generate the canonical clean schema

Create a brand-new empty PostgreSQL database, apply all temporary development
migrations at the exact candidate commit, run seeders, and dump a normalized
schema-only representation. Review it against live sqlc queries and domain
invariants. Write one deterministic `001_initial.up.sql` by dependency sections:

1. extensions/types/functions;
2. identity/auth/RBAC/projects/clusters;
3. existing non-delivery product tables still used by v1;
4. delivery sources/bundles/targets/rollouts/deployments/status/outbox;
5. Charlie/agent/operations/audit/observability state;
6. constraints/indexes/triggers/comments;
7. minimal production-safe seed data.

Do not blindly commit a `pg_dump`: remove environment ownership/ACL/noise and
ensure every object is intentional/reviewable.

**Verify**: applying `001_initial.up.sql` to empty PostgreSQL produces a
normalized schema diff identical to the reviewed final temporary schema, except
explicitly documented migration-history artifacts.

#### W12-02: Delete the full historical/temporary chain

Delete every prior `*.up.sql`, `*.down.sql`, `migration_NNN_test.go`, and
migration-specific fixture. Add `001_initial.down.sql`,
`initial_schema_test.go`, schema contract tests, and a guard enforcing exactly
one up/down pair for v1.

**Verify**:

```bash
find internal/db/migrations -maxdepth 1 -name '*.up.sql' -printf '%f\n'
find internal/db/migrations -maxdepth 1 -name '*.down.sql' -printf '%f\n'
```

Expected: only `001_initial.up.sql` and `001_initial.down.sql` respectively.

#### W12-03: Prove up/down/up and application compatibility

Run the migration image/tool on empty DB -> version 1, start every service and
execute all integration tests, migrate down -> empty user schema, migrate up ->
version 1 again, and repeat service smoke. Compare constraints/indexes/seeds and
run `make sqlc-check`.

**Verify**: all passes; no runtime query references a missing column/table; no
second-up drift; no Argo/legacy fleet identifier appears in schema.

#### W12-04: Prove old databases fail without mutation

Restore a disposable copy of a v0.3.9 database, hash/count its schemas/tables/
rows, run v1 preflight/migrate, expect `fresh_install_required`, then recompute
hash/count.

**Verify**: preflight exits non-zero with actionable docs link and before/after
catalog/table/row evidence is identical. No Secret value is included in evidence.

**Wave 12 exit gate**: exactly one initial migration, clean up/down/up, final
application gates pass, and old database rejection is non-mutating.

### Wave 13 — Rebuild the running system on the same domain/TLS and release

The operator has authorized rebuilding the current Astronomer system, re-serving
it on the same domain/TLS, validating Charlie, and fixing discovered issues.
This authorization does not permit targeting an unverified cluster/namespace,
deleting backups/TLS/user files, or silently resetting a database.

#### W13-01: Discover and freeze the exact live target

Before any mutation, record:

- kube context/cluster UID, Helm release/namespace, chart/app version;
- public DNS hostname, Ingress/HTTPRoute/Gateway/Service chain, load balancer/IP;
- TLS issuer, Certificate, Secret name, SANs, expiry, renewal state, and ownership;
- values and external Secret references with secret values redacted;
- database/Redis storage classes/PVCs, backup destination and latest verified backup;
- current image digests, replica counts, PDBs, NetworkPolicies, DNS TTL;
- Charlie endpoint/connector IDs and exact currently qualified versions;
- disk usage and current/rollback artifacts.

Resolve the actual access path first; the planning environment had no active
`kubectl` context, so no live namespace/domain is assumed here.

**Verify**: two independent identifiers (cluster UID plus expected public
hostname/Certificate) match the operator-designated installation; backup restore
test and capacity gate pass. If not, stop.

#### W13-02: Prepare parallel v1 installation

Prefer blue/green: install v1 with a new release name, namespace, database/PVCs,
and internal hostname while leaving v0.3.x serving the public route. Reuse the
same TLS Certificate/Secret only through its supported ownership model: either
reference a Secret in the routing namespace or issue a parallel Certificate for
the same SAN. Never copy private-key bytes into files/logs/plan evidence.

Recreate configuration through v1 values/API, enroll clean test clusters, and
complete W11 against the parallel system. Do not point old v0.3 agents at v1.

**Verify**: v1 is healthy via internal/SNI override, certificate chain/SAN is
valid, v0.3 remains available, and database/storage/resources are isolated.

#### W13-03: Switch the same public domain atomically

Lower DNS TTL in advance only if DNS moves; otherwise patch the existing
Gateway/Ingress/Service backend using a reviewed manifest. Capture the exact
rollback patch. Switch traffic to v1, verify external DNS/TCP/TLS/HTTP/login/API/
SSE/WebSocket/browser/CLI, and monitor errors/latency/certificates.

**Verify**: the original public hostname serves v1 with the same valid TLS
identity, no mixed-version API/asset traffic, and all critical probes pass from
inside and outside the cluster. On failure, apply the captured route rollback;
do not alter old data to force success.

#### W13-04: Reconnect and qualify Charlie

Update Charlie’s Astronomer connector only if endpoint metadata/disclosure
digest requires it; the public hostname/TLS trust should remain stable. Run the
full live capability/approval/denial/Flux diagnosis suite. Fix issues in the
owning repo, rebuild/redeploy only affected immutable artifacts, and rerun all
dependent gates and three-run acceptance.

**Verify**: Charlie is connected/healthy, exact companion versions and
disclosure digest match, allowed reads/writes and forbidden boundaries pass, and
no old Argo/fleet capability remains discoverable.

#### W13-05: Soak, clean safe artifacts, and decommission old system

Soak the public v1 system for at least 24 hours through representative delivery,
agent reconnect, UI/CLI, backup, alert, and Charlie activity. Retain old v0.3
release/database/PVC/images as rollback throughout soak. After written acceptance,
uninstall the old Helm release. Delete its namespace/PVC/database only as a
separate explicit reviewed action; record whether its backup remains and until when.

Run safe disk cleanup using the explicit candidate manifest, retaining v1
current and one rollback release, verified backups, release evidence, registry
artifacts referenced by manifests, and TLS material.

**Verify**: 24-hour SLOs and backups pass; old route has no traffic; cleanup
never touches protected items; post-cleanup capacity gate passes; public domain,
TLS, Astronomer, agents, delivery, and Charlie remain healthy.

#### W13-06: Tag and publish

Only after all previous gates, create the signed `v1.0.0` tag/release and the
qualified Charlie companion tag using the already tested commits/artifact
digests. Publish chart/images/assets/checksums/SBOM/provenance/signatures,
compatibility, fresh-install/reset guide, runbooks, known limitations, and
evidence index. Do not rebuild after qualification; promote the tested digests.

**Verify**: install again from public release references into an empty cluster,
verify signatures/digests, run smoke/live delivery/Charlie reads, and prove the
release manifest matches every deployed artifact.

**Wave 13 exit gate**: the same public domain/TLS serves the tagged clean v1
system, Charlie is qualified, safe headroom remains, old system is decommissioned
only after soak/acceptance, and the published artifacts are the tested artifacts.

## Consolidated test plan

This matrix is mandatory even where a wave already names the test. Prefer small
unit/property tests for algorithms, real PostgreSQL for transactional claims,
envtest for API/RBAC/materialization, k3d for Flux behavior, and packaged live
tests for release acceptance. Do not mock the layer whose behavior the test claims.

| Layer | Required coverage | Primary location/command |
|---|---|---|
| Domain | validation, immutability, every allowed/denied state transition, canonical digests | `internal/delivery/**/*_test.go`; `go test -race ./internal/delivery/...` |
| Placement | Kubernetes selector semantics, groups/project scope, exclusions, zero/all safety, deterministic 10k result | `internal/delivery/placement`; unit/property/benchmark |
| Resolver | all sources/auth/trust, exact pins, SSRF/redirect/DNS/proxy/CA, size/archive limits, redaction | resolver unit + local Git/HTTP/OCI integration |
| Database | constraints/indexes/CAS/idempotency/claims/fences/outbox/project isolation; singleton migration up/down/up | PostgreSQL integration and `initial_schema_test.go` |
| Rollout | strategies, budgets, approvals/windows, deadlines, failure/rollback/delete, multi-worker crash/retry | deterministic worker + real PostgreSQL tests |
| Protocol | canonical snapshot, bounds, negotiation, stale/replay/session/sequence, tombstones, fuzz | `pkg/protocol`, tunnel, server provider, agent tests |
| Materializer | complete Flux object goldens, RBAC/Secret ordering, safe apply/prune/delete/orphan, no namespace escape | fake client + envtest |
| Observer | all Flux Conditions/revisions/inventory, event storms, sanitization, reconnect snapshot | informer unit/envtest |
| Flux | source/Kustomize/Helm Ready, health, drift, remediation, suspend/delete, controller restart/upgrade | `make test-flux-integration` |
| API/RBAC/audit | full route contract, project/verb matrix, idempotency/ETag, scoped SSE, no secret response | handler/OpenAPI/golden/integration |
| CLI | apply/preview/watch/actions, formats, exit codes, non-interactive safety | CLI fake server + live smoke |
| UI | all pages/states/actions, server pagination, SSE recovery, accessibility, no secret/raw editor | Vitest + Playwright |
| Charlie | discovery, bounded reads, RBAC, approval/action digest/idempotency/verify, denial, Flux pack retrieval | both repos’ contract/unit/live suites |
| Helm/release | clean install, old DB reject/no mutation, schema/render/image/license/security, TLS | Helm verify + packaged k3d/live |
| Air-gap | all images/charts/artifacts mirrored, zero undeclared egress | isolated registry/k3d test |
| HA/chaos | multi-replica worker/server failure, partitions, stale frames, restore, no duplicate/unsafe prune | chaos harness/report |
| Scale/soak | 10k clusters/100k deployments, SLOs, 24h, disk peak/headroom | scale harness/report |
| Security | threat controls, fuzz, SAST/dependency/image/chart, secret/redaction, least privilege | security report and enterprise gate |
| Live cutover | same hostname/TLS, blue/green rollback, external probes, Charlie, 24h soak, safe cleanup | Wave 13 evidence |

Every regression discovered in live or Charlie qualification receives the
smallest reproducing automated test before the fix. Rebuilding an artifact
invalidates all evidence downstream of its digest.

## Documentation deliverables

Before release, active documentation must include:

- architecture decision and state/ownership contract;
- installation and fresh v1 reset/reinstall guide;
- source, trust, proxy/CA/workload-identity, and air-gap mirror guides;
- bundle/target/placement/strategy/approval/window/rollback reference;
- API/OpenAPI and `astro delivery` CLI reference with safe examples;
- agent/Flux compatibility and registration lifecycle;
- security/tenancy/RBAC/credential model and threat model;
- SLOs, capacity limits, retention/disk policy, backup/restore and DR;
- every runbook listed above;
- release compatibility/provenance/evidence index;
- Charlie capability and diagnostic boundaries.

Archive the old Argo decision/runbook material under one clearly marked
historical location only if it has lasting decision value. Delete instructions
that could cause a v1 operator to run Argo commands or old fleet-operation APIs.

## Done criteria

All boxes are mandatory:

- [ ] The architecture docs say Flux is the downstream executor and explicitly
      say Rancher Fleet, OCM, Argo, and generic Flux UI are not dependencies.
- [ ] Only source-controller, kustomize-controller, and helm-controller are in
      the pinned, signed, digest-locked Flux distribution.
- [ ] A new user can install tagged Astronomer, register a clean cluster with one
      command, and deploy a signed immutable bundle without Flux-specific setup.
- [ ] Sources, bundles, targets, previews, rollouts, deployments, system health,
      and cluster agents work through REST, CLI, UI, audit, SSE, and approved
      Charlie surfaces.
- [ ] Git commits, OCI artifacts, HTTP Helm charts, and OCI Helm charts are
      centrally resolved and approved immutably; each downstream observation
      matches the expected revision/digest.
- [ ] Placement, canary/rolling/partitioned/all-at-once, approvals, windows,
      budgets, pause/resume/abort/retry/rollback/delete/orphan are transactionally
      durable, HA-safe, and live-tested.
- [ ] Local Flux continues reconciling through a 24-hour management outage and
      the agent restores exact status after reconnect without unsafe pruning.
- [ ] Namespace/project/platform scope, RBAC, NetworkPolicy, service-account
      impersonation, source SSRF, signature, credential, redaction, replay, and
      size-bound controls pass security tests.
- [ ] `./scripts/check-legacy-delivery-surface.sh --fail` exits 0 and scans source,
      chart render, images, licenses, SDK, CLI, UI, docs, and built artifacts.
- [ ] `rg -n -i 'fleet\.cattle\.io|rancher/fleet|quay\.io/argoproj' cmd deploy frontend/src internal pkg scripts go.mod go.sum` returns no matches outside the checker’s negative fixtures.
- [ ] `/argocd`, `/fleet-operations`, `argocd-*`, `FleetOperation`,
      `fleet_operations`, `agent_fleet`, old desired-manifest protocol, removed
      CRDs, and their settings/metrics/audit/RBAC/Charlie contracts are absent.
- [ ] `find internal/db/migrations -maxdepth 1 -name '*.up.sql'` prints only
      `001_initial.up.sql`; the equivalent down search prints only
      `001_initial.down.sql`.
- [ ] The v1 initial migration passes empty up/down/up and final schema comparison;
      no Argo/legacy fleet schema exists; v0.3.x DB preflight rejects without mutation.
- [ ] `go mod tidy`, SQL/OpenAPI/SDK/route/error/docs/Charlie/chart/image/license/
      release generators are committed and a second run produces no diff.
- [ ] `make test`, `make lint`, `make verify`, `make sqlc-check`, `make sdk`,
      `make charlie-contract-check`, and
      `make verify-enterprise VERIFY_SCOPE=all` exit 0.
- [ ] Frontend typecheck, test, lint, build, and complete E2E commands exit 0.
- [ ] Flux integration, air-gap, security, HA/chaos, backup/restore, 10k/100k
      scale, 24-hour soak, and disk-capacity/retention gates pass with reviewed evidence.
- [ ] Three consecutive clean packaged live acceptance runs pass on supported
      Kubernetes minors and artifacts are not rebuilt afterward.
- [ ] The existing public Astronomer hostname serves the tagged v1 system with
      the same valid TLS identity; rollback route/artifacts/backups were retained
      through acceptance.
- [ ] Charlie’s companion tagged release is qualified against the exact public
      v1 system/capability digest; every allowed/denied/redaction boundary passes.
- [ ] Old live Astronomer resources are decommissioned only after 24-hour soak
      and explicit acceptance; protected backups, TLS, current/rollback images,
      volumes, release artifacts, and user files survive cleanup.
- [ ] `advisor-plans/README.md` marks Plan 007 DONE and links the evidence index.

## STOP conditions

Stop and report; do not improvise if any occurs:

- An in-scope architecture/schema/protocol/release file materially drifted from
  commits `564ce9a` or `aaca50b` and invalidates a contract here.
- Product owners withdraw the fresh-install-only decision or request old Argo/
  fleet data migration. That is a different architecture and plan.
- Any proposed code imports/installs Rancher Fleet, OCM, Argo, Flux Operator,
  community Flux Helm chart, notification/image automation, or a generic Flux UI.
- The chosen Flux release does not officially cover the Kubernetes support
  matrix or requires incompatible Kubernetes/Go APIs.
- A typed assignment cannot enforce the required namespace/platform scope, or a
  workload assignment can mutate the Flux system/controller/RBAC trust boundary.
- Source resolution cannot guarantee the exact approved immutable artifact on
  every cluster, including authenticated/mirrored sources.
- A secret appears in a task payload, API response, audit, event, status, metric,
  log, evidence file, command line, committed manifest, or Charlie result.
- Multi-replica tests produce a duplicate generation/action, skipped cohort,
  incorrect availability/failure budget, stale status advance, or unsafe prune.
- The singleton initial migration does not exactly reproduce final schema, a
  runtime query relies on historical migration behavior, or old database
  preflight changes any old data.
- A test/build/release filesystem falls below required headroom and the explicit
  safe cleanup allowlist cannot restore it. Never delete protected data to continue.
- The exact live cluster/release/namespace/domain/TLS ownership cannot be proven
  with two independent identifiers before cutover.
- A verified backup/restore, route rollback, current+rollback artifacts, or TLS
  continuity is unavailable before live mutation.
- Blue/green isolation is impossible and an in-place destructive rebuild would
  be required without a separately reviewed outage procedure.
- The public domain serves an invalid/different certificate, mixed versions, or
  failed critical probe after switch. Roll route back immediately and stop.
- Charlie would need raw downstream tunnel/Kubernetes/Flux access, an untyped
  generic operation, credential access, or platform-scope automation.
- A wave verification fails twice after a reasonable scoped fix, or fixing it
  requires unrelated subsystem changes not listed in Scope.
- A critical/high reachable security finding or unreviewed release waiver remains.

## Maintenance notes

- Flux upgrades are supply-chain and API migrations, not routine tag bumps.
  Review release notes, regenerate/inspect manifest+RBAC+CRD/image diffs, qualify
  every supported Kubernetes minor, canary system rollout, and retain one tested rollback.
- Once v1.0.0 ships the migration chain is no longer squashed. New schema changes
  begin at `002_<name>` and follow forward-compatible expand/contract rules for
  v1 upgrades. The singleton constraint applies only to the v1.0.0 baseline tag.
- Add a new renderer/source kind only through the closed API/protocol union,
  immutable resolver, materializer golden tests, RBAC/threat review, UI/CLI/API/
  Charlie contract, air-gap packaging, and live matrix.
- Do not add an abstract “provider hook” in v1. Flux is the only executor. The
  closed source/renderer interfaces are sufficient. Introduce a provider
  abstraction only when a second real executor has approved semantics and tests;
  otherwise it would recreate the complexity this plan removes.
- Review source trust roots, keyless OIDC issuer/subject policies, registry mirror
  mappings, credentials, CAs, Flux versions, Kubernetes compatibility, and
  built-in bundle/image digests on a scheduled cadence.
- Keep delivery event retention, database growth, source/controller caches,
  registry retention, build cache, backup storage, and host/PVC disk alerts under
  measured budgets. Cleanup must remain manifest/reference aware.
- Reviewer focus areas: authority boundaries, immutable resolution, source SSRF,
  credential lifecycle/redaction, downstream RBAC, apply/prune/delete safety,
  status fencing, rollout availability math, schema baseline accuracy, release
  digest coherence, live target identity, TLS continuity, and Charlie action scope.

## Primary upstream references

Use primary upstream documentation and pin conclusions to the release under test:

- [Flux installation and supported Kubernetes versions](https://fluxcd.io/flux/installation/)
- [Flux v2 releases](https://github.com/fluxcd/flux2/releases)
- [Flux components and optional-component selection](https://fluxcd.io/flux/installation/configuration/optional-components/)
- [Flux security best practices](https://fluxcd.io/flux/security/best-practices/)
- [Flux multi-tenancy and service-account impersonation](https://fluxcd.io/flux/installation/configuration/multitenancy/)
- [Source-controller OCIRepository and signature verification](https://fluxcd.io/flux/components/source/ocirepositories/)
- [Source-controller GitRepository](https://fluxcd.io/flux/components/source/gitrepositories/)
- [Kustomization prune, health, dependencies, service accounts, and status](https://fluxcd.io/flux/components/kustomize/kustomizations/)
- [HelmRelease lifecycle, remediation, tests, drift, and Conditions](https://fluxcd.io/flux/components/helm/helmreleases/)
- [Flux Prometheus metrics](https://fluxcd.io/flux/monitoring/metrics/)
- [Flux Kubernetes events](https://fluxcd.io/flux/monitoring/events/)
- [Rancher Fleet architecture](https://fleet.rancher.io/explanations/architecture) — behavioral comparison only; not a dependency
- [Rancher Fleet bundle stages](https://fleet.rancher.io/explanations/ref-bundle-stages) — terminology/state comparison only; not a dependency

The official Flux docs observed on 2026-08-17 list Flux `v2.9.3` as the current
patch and describe Kubernetes `v1.33+` support. Re-verify both at W1-01; never use
“latest” as a release input.
