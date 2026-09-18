# Apps and Flux Delivery Implementation Ledger

- **Plan:** `2026-08-26-rancher-like-apps-implementation-plan.md`
- **Started:** 2026-08-26
- **Execution mode:** Fast vertical slices; comprehensive tests are deferred to Workstream 17.
- **Development cluster:** `astro-const-dev-k3s-{1,2,3}`
- **Astronomer branch:** `fix/v1.2.0-release-readiness`

| Task | Status | Repository / files | Manual result | Deferred hardening |
|---|---|---|---|---|
| Supporting repositories | Complete | `alphabravo-oss/astronomer-catalog`, `alphabravo-oss/astronomer-examples` | Both repositories validate/render; protected `main` requires CI and CODEOWNER review; `v0.1.0` immutable tags exist; catalog release workflow publishes/signs OCI | Full release qualification remains Workstream 18 |
| Curated catalog v1 | Complete | `astronomer-catalog/catalog.json`, `schemas/` | 20 entries validate and their upstream Helm repositories synchronize | Split entries into per-application release payloads; OCI publication and signatures |
| Catalog persistence | Application-catalog slice complete | `internal/db/migrations/027_application_catalog_v1.up.sql`, `internal/db/queries/catalog_blessed.sql` | The pinned catalog, its trust/sync state, 20 presentation records, compatibility, resource/storage guidance, and lifecycle policy are persisted by one atomic reconcile statement | Organization trust-root administration and historical catalog revisions |
| Catalog synchronization and trust | Immutable digest slice complete | `internal/catalog/blessed.go`, `deploy/chart/values.yaml` | The server reconciled the 20-entry catalog from commit `b572440c8a30c499cfd35009b87706fe230cf46c`; its `sha256:a9cf...cb9` index is `digest-verified`; a failed fetch or validation cannot replace the last-known-good catalog | Cryptographic publisher signatures, SBOM/provenance verification, revocation feeds, and local asset caching |
| Portable catalog retrieval | Complete for this slice | `internal/catalog/source.go`, catalog Helm values/templates | A 20-entry catalog reconciled from temporary OCI manifest `sha256:6cbfec...2965`; a deliberately unreachable HTTPS source then reconciled through its digest-checked mirror; revision 34 runs the permanent pinned source with Secret-backed proxy, appended private CA trust, and explicit private-mirror policy available | Publisher authentication/signatures, end-to-end private proxy/CA environment exercise, and air-gap bundle import |
| Apps catalog frontend | Server-driven discovery complete | `frontend/src/routes/dashboard/catalog`, `frontend/src/lib/catalogs/astronomer.ts` | The 20 curated cards and TanStack table now use API-owned presentation metadata, including category/support/access/storage facets, compatibility, documentation, and verified artifact identity | Favorites and locally cached screenshots/icons |
| Catalog source experience | Rancher-like source slice complete | `frontend/src/lib/catalogs/source.ts`, `frontend/src/components/catalog`, cluster Apps routes | One underlying 139-chart list is presented as 20 Astronomer Curated and 119 Community charts; individually named Custom sources receive stable distinct colors; repository and functional-category facets remain independent; the cluster sidebar exposes Charts, Installed Apps, and Repositories | Administrator color overrides, recent catalog operations page, and custom ApplicationCatalog registration beyond Helm/OCI repositories |
| Offline catalog behavior | Last-known-good slice complete | `internal/catalog/blessed.go`, `internal/db/queries/catalog_blessed.sql`, catalog source API/UI | Fetch failures never stop startup or replace verified rows; source errors are persisted and the UI explains cached/offline or unavailable state while installed applications remain manageable | Bundled air-gap catalog/chart/image export and import, internal asset mirror, and fresh-install failure exercise |
| Routed application experience | Complete | cluster Apps routes, schema form, catalog operation APIs/UI | Charts route to enriched full detail pages with upstream/curated metadata, publisher documentation, operational guidance, and version history; install uses Target, Configure, Values Review, and Preflight stages; accepted work remains visible through server-recorded operation events | Browser qualification across schema-rich and YAML-only charts |
| Cluster-scoped Apps authorization | Complete | catalog handler/API, cluster Apps routes, migration 029 ownership field | Browse, detail, favorites, repositories, and install are scoped to the selected cluster; the UI no longer asks for a project; target namespaces come from effective RBAC and the server derives optional `project_id` from authoritative namespace membership | Browser qualification with cluster-admin, project-member, namespace-only, and denied identities |
| Automatic project baseline | Complete | migration 030 | Every cluster receives managed `default` and `system` projects; Default owns the `default` namespace and System owns Kubernetes system namespaces when those namespaces are otherwise unassigned | Richer administrator-controlled project templates |
| Astronomer First Party | Constellation entry complete | `astronomer-catalog`, Constellation Helm chart | Immutable catalog commit `e9adffb` contains the packaged Constellation 0.2.0 chart, metadata, logo URL, compatibility/storage/resource guidance, and schema-driven common settings | Signatures, cached assets, and full lifecycle qualification remain final hardening work |
| Catalog operations experience | Complete for this slice | cluster Apps Operations route, operation polling, Flux delivery projection | Operations have a dedicated sidebar page; install progress remains open through authoritative Flux workload readiness and exposes server-recorded events and retry | Broader multi-cluster rollout timelines and notifications |
| Catalog collection response contract | Fixed and deployed | catalog handlers and frontend catalog API adapter | Applications and application-source endpoints now emit the OpenAPI collection shape; the rolling-upgrade client also tolerates the older double envelope, restoring Charts and Repositories | Add the focused response-contract regression test in Workstream 17 |
| Catalog discovery context | Complete | migration 031, catalog discovery API, cluster Charts and detail routes | Favorites and recent views persist per authenticated user; cards display complete installed version/release/target context; catalog trust and sync health links directly to repository remediation | Cross-device browser and accessibility coverage remains in Workstream 17 |
| Catalog-to-Flux lifecycle | Guided lifecycle slice complete | `internal/delivery/catalogapp/service.go`, `internal/handler/catalog.go`, `internal/server/app_delivery.go` | Install performs a server-side prerequisite preview and explicit confirmation; upgrade offers only the catalog-pinned verified version; apply records immutable version/value digests; existing Grafana history was backfilled; rollback and uninstall remain Flux-owned | Browser exercise of authenticated install/upgrade/rollback/uninstall, dependency ordering, and multi-cluster rollout orchestration |
| Existing Delivery substrate | Reused | `internal/delivery`, `internal/agent/delivery`, Delivery APIs/UI | Built-in applications converge through local Flux; Installed Apps labels request-owned releases as Flux-managed | Richer rollout history and application naming |
| System component inventory | Agent-observed vertical slice complete | protocol inventory, migration 032, delivery status ingestion, cluster Delivery System Components routes | Platform-scoped agents report normalized Astronomer server/frontend/worker/Dex, CNPG, Valkey, agent, Flux, Longhorn/storage classes, cert-manager, Gateway/CNI, DNS/LB and Kubernetes distribution observations. Read-only adapters add Longhorn node/volume capacity, schedulability, replica and robustness evidence; Certificate Ready counts; Gateway programmed/listener/route evidence; and every PVC's class/driver/phase/requested/capacity/access mode/expansion/snapshot posture. Component details link claims and Longhorn/Certificate/Gateway objects into native workload, storage, and CRD pages. Focused Go packages, migrations, generated contracts, frontend type-check/lint, and production build pass | Direct logs/runbook shortcuts, external integration status beyond workload readiness, and live three-node browser qualification |
| Final automated coverage | Deferred by plan | Workstream 17 | Not started | All unit, contract, integration, browser, security and scale coverage |
| Catalog and delivery operations | Complete locally | catalog/delivery metrics, `continuous-delivery.json`, Prometheus rules, support bundle, DR chart templates, runbooks, air-gap tooling, release workflow | Focused Go tests, chart lint, enabled/disabled chart renders, JSON and shell syntax checks pass | Live outage/recovery and disconnected-site qualification remain Workstream 18 |
| Catalog publisher signatures | Complete locally, operator-enabled | catalog source loader, shared delivery Sigstore verifier, Helm catalog signature values/mount | Digest-pinned OCI catalogs can now fail closed on cosign key or offline keyless verification; verified publisher identity is persisted and invalid evidence cannot replace last-known-good rows | Enable with the exact release identity and pinned trust-root Secret during production qualification |
| Deterministic configuration merge | Rollout-bound slice complete locally | `internal/delivery/configuration`, migration 034, target planning and Flux materialization | Override CRUD, deterministic merge/conflict rejection, per-cluster scope filtering, frozen effective renderers and digests, target UI selection, and Helm Secret `valuesFrom` references are wired end to end | Rendered-resource diff and live multi-cluster qualification remain Workstreams 6, 17, and 18 |
| Delivery CLI follow-through | Configuration slice complete locally | `cmd/astro/delivery_configuration.go` | Generation-fenced template and override CRUD, effective resolution, and cluster/fleet system inventory are available through the CLI; package tests pass | Catalog/application/rollout CLI breadth and diff output remain Workstream 11 |
| Enterprise static qualification | Complete for the current local source tree | `scripts/verify-enterprise.sh`, evidence manifest under `/tmp/astronomer-verify-enterprise/` | The combined `all` scope passed formatting, ShellCheck, migration and sqlc policy, Go build/vet/lint, the complete Go and race suites, 770/770 routed OpenAPI coverage, generated-client drift, 1,055 frontend tests, production build and bundle budgets, zero frontend dependency vulnerabilities, Helm lint/renders/contracts, and release/image/air-gap contracts | Live browser, real PostgreSQL/source integration, multi-cluster failure injection, scale, clean-install lifecycle, catalog/examples publication qualification, and release sign-off remain Workstreams 17–18 |
| Catalog schema compatibility | Complete locally | `astronomer-catalog/schemas`, `scripts/validate_schema.py`, validation CI | The actual 21-entry index now validates through Draft 2020-12 reference resolution and strict HTTPS asset-link policy; focused positive and fail-closed schema tests pass and run in CI | Publisher release qualification and network-backed artifact validation remain Workstream 2 |
| Catalog v1 specification and builder | Core local implementation complete | `astronomer-catalog/schemas`, `scripts/build_catalog.py`, `generated/`, validation and release workflows | Version, presentation, compatibility, prerequisite/conflict, dependency ownership, capacity/PVC, lifecycle, support-state, immutable identity, safe-link, and reader-negotiation contracts are defined. A deterministic builder emits 34 canonical release/mirror files for all 21 entries, including compatibility and revocation indexes; schema and determinism tests pass | Helm OCI/generic OCI/Git resolution, remote asset digesting/caching, dependency example metadata, and signed release qualification remain |
| Immutable Helm artifact resolution | Complete for catalog Helm HTTP sources | `astronomer-catalog/scripts/resolve_artifacts.py`, `artifact-lock.json`, release builder | All 21 pinned chart versions resolve through bounded public HTTPS with private-address and credential-bearing-source rejection; redirects are validated without persisting transient signed URLs. The committed lock records exact archive digest and size and is consumed by release manifests without network access | Helm OCI, generic OCI, Git revisions, provenance/SBOM retrieval, and periodic re-resolution policy remain |
| Examples repository qualification | Complete locally | `astronomer-examples` render workflow and validation documentation | Both Helm examples lint and render; the Kustomize and bounded raw-manifest examples render offline. CI now enforces all four paths without needing a cluster | Live installation through Astronomer and multi-environment/dependency lifecycle qualification remain Workstream 17 |

## Known defects / intentional temporary limits

- Catalog presentation metadata now comes from the persisted, verified catalog;
  a small frontend decorator remains only for UI defaults that are not yet in
  the catalog schema.
- Catalog identity is currently protected by immutable Git revision and SHA-256
  digests, not publisher signatures. Production release qualification must add
  signatures, provenance, and revocation policy.
- Remote icon URLs are used with upstream project branding. Asset digesting and
  safe local caching are deferred to the catalog trust slice.
- No production release is authorized until Workstreams 17 and 18 pass.
- Migration 032 and the System Components UI are local-only and have not been
  deployed. The full server suite still has the previously recorded route
  golden/security-classification drift and monitoring fixture panics; this
  slice adds no HTTP route and its focused backend/frontend checks pass.
- The routed catalog frontend, catalog server, and migrator are deployed as
  `local-20260826-catalog-discovery` in Helm revision 38. Each long-running
  Astronomer workload has a ready replica on every k3s node.
- Authenticated live checks passed for chart discovery, chart detail, versions,
  generated values/schema, README, and catalog operations. A duplicate
  Constellation installation was deliberately not created during verification.
- Migration 030 is live. The development cluster has a Default project owning
  `default`, a System project owning `kube-system`, `kube-public`, and
  `kube-node-lease`, and its pre-existing Astronomer project remains unchanged.
## 2026-08-26 — application operations UI completion slice

- Added project-scoped `target_id` filters to rollout and cluster-deployment
  list contracts, handlers, SQL queries, generated clients, and SDKs.
- Turned delivery target details into application overviews with aggregate
  ready/desired, failure, drift, generation, and observation posture.
- Completed the application list columns for observed revision, Flux source,
  ready/desired clusters, drift, age, and persisted last actor.
- Added navigable per-cluster deployment inventory and immutable release
  history to each application target.
- Added rollout cohort cards with immutable order, approval/soak policy, live
  ready/failure counts, and progress visualization; cluster UUIDs now resolve
  to human-readable names.
- Added normalized deployment resource/drift posture and completed deployment
  suspend/resume/reconcile action gating.
- Extended the agent protocol and OpenAPI contract with up to 64 bounded,
  secret-free Flux Kustomization resource identities; deployment details now
  provide TanStack search plus namespace/kind filters. Helm inventory remains
  aggregate-only and is labeled as such instead of fabricating identities.
- Exposed the persisted target updater/creator as the application list's last
  actor identity.
- Rebuilt cluster-group administration on the shared TanStack `DataTable`,
  added direct-membership detail, and added a multi-cluster assignment editor.
- Added target-scoped audit history using the existing composable audit API.
- Validation: focused delivery handler/controller Go tests, SQLC drift, Go SDK
  drift, frontend OpenAPI drift, frontend type-check, lint, and production
  build all pass.

## 2026-08-26 — operations and packaging completion slice

- Added bounded catalog synchronization metrics and extended the continuous
  delivery dashboard and Prometheus alerts without source/cardinality labels.
- Added one catalog/application operations runbook covering synchronization,
  source resolution, rollouts, assignments, dependencies, drift, system
  inventory, and secret-free support evidence.
- Added aggregate catalog/delivery support-bundle diagnostics that explicitly
  exclude URLs, credentials, rendered values, manifests, and event messages.
- Extended the restore drill with existence checks for every delivery/catalog
  schema that may legitimately contain zero rows.
- Included a configured private-catalog CA in the encrypted DR key bundle and
  documented its restoration contract.
- Added `catalog.enabled` safe-off behavior, generated product release notes
  from the Unreleased changelog, and added a checksum-verified application
  catalog/chart/image-inventory export/import workflow for static internal
  HTTPS mirrors.
- Validation: focused catalog, agent delivery, protocol, and handler tests;
  chart lint; Kubernetes 1.35 chart render; disabled-catalog render; dashboard
  JSON validation; and air-gap shell syntax all pass.

## 2026-08-26 — configuration evidence slice

- Added deployment drift detail that combines the current normalized Drifted
  condition, generation/revision convergence, and durable drift/reconcile/
  repair event history.
- Added an immutable bundle-version detail route with explicit artifact,
  project bundle, placement override, and frozen-rollout precedence. Helm
  values are recursively redacted by sensitive key and Kustomize patch bodies
  remain hidden; immutable digests remain visible.
- Linked every bundle version into the new detail route.
- Validation: frontend production build, route generation, and type-check pass;
  the lint-only query-key defect was corrected with the existing canonical key.

## 2026-08-26 — configuration templates and override foundation

- Added migration 033 with project-scoped configuration templates and
  deterministic override sets covering organization, project, environment,
  group, cluster, and rollout scopes.
- Added generation-fenced configuration-template CRUD, dedicated RBAC
  vocabulary and built-in role grants, bounded payload validation, inline
  secret rejection, explicit Kubernetes Secret references, and audit events.
- Added OpenAPI/Go/TypeScript generated contracts and a cluster Delivery
  Templates page with TanStack list, JSON editor, Secret-reference editor,
  update fencing, and permission-gated actions.
- Extended the DR table-presence contract for both new tables.
- Validation: focused backend/RBAC/route tests, route golden/inventory,
  SQLC drift, Go SDK drift, frontend type-check/lint, and production build pass.

## 2026-08-26 — effective configuration rollout binding

- Added generation-fenced override-set CRUD and a redacted effective-values
  endpoint across SQL, handlers, OpenAPI, generated clients, CLI, and UI.
- Added migration 034 so targets select a base template and ordered override
  sets, while cluster deployments retain the frozen effective renderer and
  configuration digest used by the released assignment.
- Bound configuration changes into placement preview, approval, plan, and
  assignment digests. Group/environment and cluster layers are filtered per
  candidate and each planned cluster carries its own immutable renderer.
- Materialized Helm template Secret references as Flux `valuesFrom` entries;
  only Secret identity/key/target path is persisted. Unsupported Kustomize
  secret projection now fails explicitly instead of being ignored.
- Added target create/edit controls and target-detail configuration posture.
- Validation: focused rollout, provider, agent materializer, protocol, and
  delivery-handler tests; SQLC drift; frontend type-check and lint pass.
