# Astronomer-go vs Rancher: Validation Report & Remaining Gaps

**Date:** 2026-05-10  
**Scope:** Current completion audit after the parity/hardening work landed. This document replaces the earlier 2026-05-08 gap report, which had become stale in several important areas.

---

## TL;DR

The codebase is materially stronger than the earlier validation report claimed.
Several items that were previously listed as critical gaps are now implemented
and tested:

- Phase B frontend surfaces exist for ArgoCD, backups, Dex settings/connectors, and CIS scans.
- Audit logging is no longer limited to projects; it now spans auth, clusters, ArgoCD, backups, Dex, security, monitoring, logging, alerting, catalog, and more.
- The chart defaults are no longer single-replica for the core stateless services: server, worker, and optional frontend now default to two replicas with rolling updates, PDBs, affinity, and topology spread.
- Self-observability is no longer absent: server and worker expose dedicated Prometheus listeners, structured request logs exist, and the platform has contract tests around documented metrics/log fields.
- Login is rate-limited.
- Several earlier parity gaps are now closed or partially closed: PSA enforcement, image pull secret propagation, cert-manager chart integration, richer built-in RBAC catalog, OAuth state-cookie signing, and schema-driven Helm values forms for catalog installs.

The project is still **not done** against the user’s objective. The main
remaining gaps are now narrower and more honest:

1. **Most of the big Phase B runtime paths are now live-demonstrated.**
   ArgoCD now has live validation for application CRUD, managed-cluster
   registration, and ApplicationSet fan-out. Dex now has live validation
   through connector create/apply, deployment rollout, corrected SSO
   redirect construction, and a real external OIDC callback. Velero now has
   live validation through backup storage creation, real object-storage
   artifacts, and restore into a different namespace. CIS now has live
   validation through real `cis-operator` install, scan creation,
   generated-report discovery, findings ingestion, and CSV export.
2. **Production recovery posture is still thinner than Rancher.** Bundled
   Postgres and Redis are still dev-shaped single-instance defaults, and the
   management-plane backup/restore story remains under-documented and
   under-validated.
3. **The final requirement-by-requirement closure audit is not yet complete.**
   We have closed many gaps, but we still need artifact-backed proof that the
   documented shortcomings in `comparison.md` and the parity plan are either
   fixed, intentionally out of scope, or still open.

---

## What Was Stale In The Prior Report

The previous 2026-05-08 report is no longer accurate on these points:

- "No frontend UI" for Phase B features was false by 2026-05-10:
  - ArgoCD pages exist under `astronomer/frontend/src/app/dashboard/argocd/*`
  - Backups pages exist under `astronomer/frontend/src/app/dashboard/backups/*`
  - Dex settings/connectors UI exists under `astronomer/frontend/src/app/dashboard/settings/auth/*`
  - CIS scan pages exist under `astronomer/frontend/src/app/dashboard/security/*`
- "Audit logging is partial" was stale:
  - mutating audit calls now exist across auth, clusters, ArgoCD, backups, Dex,
    security, monitoring, logging, alerting, catalog, tools, and more
  - shared audit infrastructure exists in `internal/audit/*`
  - contract tests exist in `internal/audit/action_contract_test.go` and
    `internal/audit/coverage_contract_test.go`
- "Single-replica everything" was stale:
  - `deploy/chart/values.yaml` now defaults `server.replicaCount: 2`,
    `worker.replicaCount: 2`, and `frontend.replicaCount: 2`
- "No self-observability" was stale:
  - dedicated metrics listeners exist in
    `internal/server/metrics_server.go` and `internal/worker/metrics_server.go`
  - documented metrics/log contracts now exist in `docs/metrics-v1.md` and
    `docs/logs-v1.md`
  - request logging and structured lifecycle events exist
- "Login is unthrottled" was stale:
  - login rate limiting exists in
    `internal/server/middleware/login_rate_limit.go`
  - route wiring exists in `internal/server/routes.go`
  - tests exist in `internal/server/login_rate_limit_integration_test.go`
- Several "missing parity" items were stale:
  - PSA reconciliation: now present
  - image pull secret propagation: now present from cluster registry config
  - OAuth state tamper-evidence: now present
  - cert-manager Gateway integration: now present
  - richer built-in roles: now present
  - schema-driven Helm values form for catalog installs: now present

---

## Current Validation Snapshot

### A1 — Generic OIDC

Status: `Implemented, UI-present, and live-validated`

Evidence:

- generic OIDC/OIDC discovery support exists in `internal/auth/*`
- SSO login/callback flow is in `internal/handler/sso.go`
- the CSRF/state cookie is now signed and time-bound
- SSO providers now have real settings-backed CRUD in `internal/handler/resources.go`
  and the login page renders configured enabled providers dynamically
- live validation succeeded on 2026-05-10 against a disposable Keycloak realm
  through `scripts/validate-live-generic-oidc.sh` with
  `make validate-live-generic-oidc`:
  - `POST /api/v1/settings/sso/` created a real generic OIDC provider backed
    by Keycloak discovery
  - `GET /api/v1/settings/sso/` exposed that provider publicly for the login UI
  - `/api/v1/auth/login/{provider}/` redirected through Keycloak and returned
    to Astronomer’s direct callback route
  - the final redirect included `provider=<slug>`, `token=...`, and
    `refresh=...`

### A2 — OCI Helm registry

Status: `Implemented and live-validated`

Evidence:

- OCI repo handling landed in the catalog backend
- catalog install UX now supports schema-driven values forms when a chart ships
  `values.schema.json`
- live validation succeeded on 2026-05-10 against a real public OCI registry:
  - `POST /api/v1/catalog/repositories/` created
    `oci://ghcr.io/argoproj/argo-helm` with explicit chart list
    `["argo-cd"]`
  - `POST /api/v1/catalog/repositories/{id}/test-connection/` proved registry
    reachability
  - `POST /api/v1/catalog/repositories/{id}/sync/` ingested the OCI chart and
    created chart-version rows
  - `POST /api/v1/catalog/installed/` installed a real `argo-cd` chart version
    from that synced OCI catalog onto the remote cluster through Astronomer
  - the catalog operation completed successfully, the installed-chart row
    converged to `installed`, and the remote cluster exposed a real Helm
    release plus ready ArgoCD deployments in the target namespace
- repeatable validation script now exists at `scripts/validate-live-oci.sh`
  with `make validate-live-oci`

Remaining gap:

- OCI metadata enrichment is still thinner than traditional index-backed repos:
  default values, README, and values schema are not yet being harvested from
  the OCI artifacts

### A3 — Cross-cluster search

Status: `Implemented and improved`

Evidence:

- backend fan-out handler in `internal/handler/resources_search.go`
- frontend search UI in `frontend/src/app/dashboard/search/page.tsx`
- per-resource-type RBAC filtering now exists instead of the earlier coarse
  route-level gate

Remaining gap:

- still needs stronger fleet-scale runtime evidence, not just handler logic

### A4 / B1 — ArgoCD lifecycle

Status: `Implemented, audited, UI-present, and live-validated through install, app CRUD, registration, and ApplicationSet fan-out`

Evidence:

- real sync path and broader ArgoCD lifecycle handlers live in
  `internal/handler/argocd.go`
- audit coverage exists for instance, app, project, appset, cluster-register,
  and repo mutations
- frontend pages exist under `frontend/src/app/dashboard/argocd/*`
- live validation against the running mgmt ArgoCD instance and remote cluster
  succeeded on 2026-05-10:
  - `POST /api/v1/argocd/instances/{id}/applications/` created a new
    remote-targeted Helm application
  - the remote cluster reconciled the deployment to `1/1`
  - `PATCH /api/v1/argocd/instances/{id}/applications/{name}/` updated Helm
    values and the remote deployment scaled to `2/2`
  - `DELETE ...?cascade=true` removed the ArgoCD Application and the remote
    deployment
- repeatable validation script now exists at
  `scripts/validate-live-argocd.sh` with `make validate-live-argocd`
- live validation of managed-cluster registration and ApplicationSet fan-out
  also succeeded on 2026-05-10:
  - Astronomer registered the remote `dev` cluster into the running mgmt
    ArgoCD instance via
    `POST /api/v1/argocd/instances/{id}/clusters/{cluster_id}/register/`
  - Astronomer then created a cluster-generator ApplicationSet via
    `POST /api/v1/argocd/instances/{id}/applicationsets/`
  - ArgoCD generated an Application for the `dev` cluster and reconciled the
    remote `podinfo` deployment to `1/1`
  - deleting the ApplicationSet removed both the generated Application and
    the remote deployment
- repeatable validation script now exists at
  `scripts/validate-live-argocd-register-appset.sh` with
  `make validate-live-argocd-register-appset`
- local-cluster managed-cluster registration is now hardened in code:
  - `RegisterManagedCluster` auto-mints a fresh ArgoCD
    `argocd-application-controller` TokenRequest token when registering the
    Astronomer local cluster without a pasted bearer token
  - the ArgoCD reconciler now refreshes expiring local-cluster registration
    tokens and normalizes stale `kubernetes.default.svc` registrations onto the
    stable `clusters.api_server_url` path
  - managed-cluster DB rows now upsert their current `server_url`,
    labels, and resolved upstream Secret name instead of leaving stale local
    mappings behind after re-registration
- live validation of the tools-catalog install path also succeeded on
  2026-05-10 against the running local control-plane cluster:
  - `POST /api/v1/tools/argocd/install/` now checks Helm status first
  - when the local `argocd` release already exists but Astronomer has no
    corresponding `installed_charts` row, the operation adopts the existing
    release into Astronomer state instead of failing on Helm ownership
  - `/api/v1/clusters/{local_cluster_id}/tools/status/` now reports ArgoCD as
    `installed` with release `argocd`

Remaining gap:

- the original “can ArgoCD be installed from the tools path?” question is now
  closed in this environment
- a fully clean-room install on a brand-new cluster would still be useful as
  an additional proof mode, but it is no longer a primary blocker

### B2 — Velero backup engine

Status: `Implemented, audited, UI-present, and live-validated`

Evidence:

- Velero-aware backend is in `internal/handler/backups.go`
- audit coverage exists for storage, schedules, runs, and restores
- frontend pages exist under `frontend/src/app/dashboard/backups/*`
- server-side backup/restore reconciliation now exists in
  `internal/handler/backups_reconciler.go` and is wired from
  `internal/server/server.go`
- live validation succeeded on 2026-05-10 against the running remote cluster:
  - Astronomer created a `BackupStorageLocation` plus credentials secret in the
    remote cluster via `POST /api/v1/backups/storage/`
  - upstream Velero reported that BSL as `Available`
  - `POST /api/v1/backups/` created a real Velero `Backup` CR for a source
    namespace containing a verification ConfigMap
  - the remote Velero controller wrote real backup artifacts into MinIO under
    the configured prefix, including the backup tarball and metadata objects
  - the Astronomer backup row converged from `pending` to `completed` via the
    new reconciler and now records a stable `velero://...` artifact path
  - `POST /api/v1/backups/{id}/restore/` created a real Velero `Restore` CR
    with `namespace_mapping`, and the ConfigMap was restored into a different
    destination namespace
- repeatable validation script now exists at `scripts/validate-live-velero.sh`
  with `make validate-live-velero`

Remaining gap:

- management-plane backup and restore posture is still thin

### B3 — Project enforcement

Status: `Implemented, audited, and live-validated`

Evidence:

- quota/namespace reconciliation exists in `internal/worker/tasks/project_reconcile.go`
- reconciliation now also covers:
  - PSA labels
  - image pull secret propagation from cluster registry config
  - local server-side execution when a live tunnel-backed requester is present
- live validation succeeded on 2026-05-10 against the running remote cluster:
  - Astronomer created a real Project via `POST /api/v1/projects/`
  - `POST /api/v1/projects/{id}/add-namespace/` reconciled a managed namespace
    with:
    - `astronomer.io/project-id` namespace labeling
    - PSA namespace labels from the default pod-security template
    - managed `ResourceQuota`, `LimitRange`, and `NetworkPolicy`
    - propagated `astronomer-registry` dockerconfigjson secret plus default
      ServiceAccount `imagePullSecrets` wiring from cluster registry config
  - `POST /api/v1/projects/{id}/remove-namespace/` removed the managed quota,
    limits, network policy, propagated registry secret, default
    ServiceAccount pull-secret reference, and project label from the namespace
  - `DELETE /api/v1/projects/{id}/` completed successfully after detach
  - audit rows were observed for `project.create`, `project.add_namespace`,
    `project.remove_namespace`, `project.delete`, and the related registry
    mutation path
- repeatable validation script now exists at `scripts/validate-live-projects.sh`
  with `make validate-live-projects`
- the live validator surfaced and closed a real backend bug: `limit_range`
  values using the API’s `default_request` key were not being translated into
  Kubernetes `defaultRequest`, so remote `LimitRange` objects silently drifted
  from the stored project spec until that renderer was fixed

Remaining gap:

- reconnect / periodic-drift recovery is still thinner than the create/update
  path now live-validated here
- current image pull secret model is narrower than a richer per-project secret
  abstraction

### B4 — Dex shim

Status: `Implemented, audited, UI-present, and live-validated through external OIDC callback`

Evidence:

- backend in `internal/handler/dex_config.go`
- routes under `/api/v1/auth/dex/*`
- frontend pages under `frontend/src/app/dashboard/settings/auth/*`
- audit coverage for connector CRUD, settings update, config apply, and
  register-as-SSO
- live validation succeeded on 2026-05-10:
  - Astronomer created a disposable Dex connector via
    `POST /api/v1/auth/dex/connectors/`
  - `POST /api/v1/auth/dex/apply/` updated the ConfigMap and forced a Dex
    deployment rollout
  - the mounted `/etc/astronomer-dex/config.yaml` inside the running Dex pod
    changed to the new connector instead of serving the stale mock config
  - Astronomer SSO login then redirected through Dex into
    `/dex/auth/<connector-id>` for that live connector
  - the outbound `redirect_uri` now correctly uses
    `/api/v1/auth/callback/dex` instead of the earlier broken
    `/api/v1/auth/auth/callback/dex`
- live external OIDC callback validation also succeeded on 2026-05-10:
  - a disposable Keycloak realm was started as the upstream OIDC provider
  - Astronomer configured a Dex `oidc` connector pointing at that real issuer
  - the rendered Dex config defaulted the connector `redirectURI` to
    `<dex issuer>/callback`
  - the Astronomer static Dex client now carried both callback URI forms
    (`.../callback/dex` and `.../callback/dex/`) so Dex accepted the browser
    redirect
  - the Astronomer router now accepts both callback route forms, so the live
    callback no longer falls through to authenticated routing
  - completing the Keycloak login redirected back through Dex into
    Astronomer’s callback and finally returned the frontend redirect with
    `provider=dex`, `token=...`, and `refresh=...`
- repeatable validation script now exists at `scripts/validate-live-dex.sh`
  with `make validate-live-dex`, and it now asserts the callback path does not
  regress back to `/auth/auth/...`
- repeatable full external-callback validation now also exists at
  `scripts/validate-live-dex-oidc.sh` with `make validate-live-dex-oidc`
- apply is now hardened in code to restart the Dex deployment after ConfigMap
  updates, rather than assuming hot reload is sufficient
- the HTTP request log middleware now preserves `http.Hijacker`, preventing
  WebSocket regressions that can strand the embedded local cluster agent and
  break Dex apply against the management cluster

Remaining gap:

- broader provider coverage beyond the OIDC-brokered path is still a product
  expansion question, not a current runtime blocker

### B5 — CIS scans

Status: `Implemented, audited, UI-present, and live-validated`

Evidence:

- backend in `internal/handler/security.go`
- scan-trigger audit coverage exists
- frontend pages exist under `frontend/src/app/dashboard/security/*`
- live validation succeeded on 2026-05-10 against the running
  `k3d-astronomer-mgmt` and `k3d-astronomer-remote` clusters:
  - installed `rancher-cis-benchmark-crd` and `rancher-cis-benchmark`
  - verified Astronomer selected a current live profile
    (`k3s-cis-1.11-profile`) instead of the stale `1.8` default
  - verified Astronomer created a real `ClusterScan`
  - verified the operator created a suffixed generated
    `ClusterScanReport` owned by that scan
  - verified Astronomer ingested the report to `completed` with real
    pass/fail counts
  - verified the full-scan API returned findings and the CSV export
    endpoint returned the expected header/rows
- repeatable validation script now exists at `scripts/validate-live-cis.sh`
  with `make validate-live-cis`

Remaining gap:

- the original “is CIS actually usable end to end?” question is now closed
- broader distro/provider coverage could still be added later, but it is no
  longer a primary blocker

### B6 — Live k8s informers to SSE

Status: `Implemented, tested, and live-validated`

Evidence:

- agent-side subscriber exists in `internal/agent/state_subscriber.go`
- tunnel-side handling and SSE publish exist in `internal/tunnel/handler.go`
- unit/integration-style coverage exists in:
  - `internal/agent/state_subscriber_test.go`
  - `internal/tunnel/handler_state_test.go`
- the earlier bootstrap-flood failure mode was specifically reduced by
  suppressing informer bootstrap replay until cache sync completes
- live validation was re-run on 2026-05-10 against the running
  `k3d-astronomer-mgmt` and `k3d-astronomer-remote` clusters:
  - authenticated SSE stream on `/api/v1/events/stream/?token=...`
  - `kubectl run` on the remote cluster emitted real `cluster.k8s_changed`
    events for `Pod default/b6-probe`
- repeatable validation script now exists at `scripts/validate-live-b6.sh`
  with `make validate-live-b6`

Remaining gap:

- the original "is the watch path actually flowing?" question is now closed
- broader fleet-scale/load validation could still be added, but it is no longer
  the primary blocker it was on 2026-05-08

---

## Cross-Cutting Areas

### Audit Logging

Status: `Much stronger than before, but still worth one final sweep`

What is now true:

- shared audit writer exists
- audit schema includes `source` and `correlation_id`
- HTTP and service-side writes use the same path
- many major mutating handlers now record audit
- contract tests exist to keep audit coverage from silently regressing

Remaining gap:

- final sweep still needed for any remaining mutating outliers
- export/retention/compliance runbook is still thinner than Rancher

### Observability

Status: `Implemented, but not complete`

What is now true:

- dedicated metrics listeners exist for server and worker
- request metrics, DB query latency metrics, worker job metrics, dropped-event
  metrics, tunnel/agent/leader metrics, and instance ID labels exist
- structured logs exist for HTTP requests, worker jobs, tunnel lifecycle,
  process lifecycle, metrics listeners, and audit recording
- docs and scrape contract tests exist

Remaining gap:

- no tracing/OpenTelemetry yet
- no evidence yet of production dashboards/alerts over these metrics

### Rate Limiting / Abuse Protection

Status: `Partially addressed`

What is now true:

- login is rate-limited

Remaining gap:

- broader abuse controls still do not exist for every sensitive route
- bootstrap and cluster-registration abuse posture is still relatively light

### High Availability / Install Posture

Status: `Improved, but still partial`

What is now true:

- server, worker, and optional frontend now have HA-shaped stateless defaults
- metrics listeners and readiness endpoints exist
- cert-manager integration hooks exist in the chart

Remaining gap:

- bundled Postgres and Redis are still single-instance/dev-shaped defaults
- no complete, validated production runbook for upgrades, rollback, and DR
- worker scaling semantics still deserve explicit validation for periodic work

### Frontend Completeness

Status: `No longer a critical blocker of the same kind`

What is now true:

- the earlier "no UI" claim is obsolete
- the major Phase B subsystems now have user-facing pages

Remaining gap:

- presence of pages is not the same as a completed parity story
- some flows still need end-to-end live validation against real upstreams
- web terminal UI and a cross-cluster events view are still missing

---

## Still-Open Strategic Gaps

These remain reasonable future work even after the recent hardening:

- management-plane backup/restore story
- web terminal UI for the existing exec websocket
- cross-cluster events view
- onboarding wizard
- richer role-template aggregation model
- richer project-scoped secret distribution model
- broader webhook story for Git/Alertmanager
- stronger production install/runbook guidance

---

## Priority Order From Here

### P0 — Prove the hard runtime paths

1. Complete one real external IdP login callback through Dex end to end.
### P1 — Close the remaining production-readiness gaps

6. Define and document the supported production topology for Postgres/Redis.
7. Add or document a management-plane backup/restore path.
8. Do one final mutating-route audit sweep and update the audit contract tests if needed.
9. Decide whether broader rate limiting is required beyond login.

### P2 — Finish the requirement-by-requirement closure audit

10. For every still-relevant shortcoming in `comparison.md`, mark it as one of:
    - fixed with code/test evidence
    - intentionally out of scope
    - still open
11. Do the same for the parity plan acceptance criteria.
12. Only after that should the overall user objective be marked complete.

---

## Validation Checklist For The Next Cycle

Use this as the real completion gate:

```text
[ ] Bootstrap an admin user.
[x] Sign in through a live external OIDC or Dex-backed IdP.
[ ] Register two existing clusters.
[ ] Browse pods/deployments and observe live UI invalidation after kubectl create/delete.
[x] Create a Project and verify ResourceQuota + LimitRange + NetworkPolicy + PSA labels.
[x] Verify project image pull secret propagation into a managed namespace.
[x] Install ArgoCD from the tools catalog.
[x] Register managed clusters into ArgoCD.
[x] Create an ApplicationSet and observe a real sync.
[x] Configure backup storage, run a Velero backup, verify artifacts, and restore into a different namespace.
[x] Run a CIS scan and view findings.
[x] Verify audit rows for the project / registry validation path.
```

We have not yet completed this checklist end to end in the current audit cycle.
