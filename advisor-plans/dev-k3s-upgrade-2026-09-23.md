# Local K3s development upgrade — 2026-09-23

Status: management upgrade deployed; add-on follow-up reached revision 121.
Real monitoring metrics, Grafana, Trivy reports, and Fluent Bit -> Loki canary
ingestion passed. Native query/status and failed-install lifecycle gaps remain.
The latest results supersede the earlier add-on discussion below; see
[the add-on validation report](./addon-validation-2026-09-23.md).
The user explicitly authorized deployment of the latest
working tree to local K3s, an in-place upgrade attempt, and validation by
attaching fresh K3d member clusters. This supersedes the previous no-deployment
handoff for this dev environment only; it does not authorize production release
promotion or replacement of the management cluster.

## Baseline and boundaries

- Management node: `astronomer-dev-1-mj`, K3s v1.35.7+k3s1, Ready.
- Target: Helm release `astronomer` in namespace `astronomer`, revision 113,
  chart 1.2.0, development configuration, three healthy application deployments.
- Database: PostgreSQL 16, one clean schema row at version 62, approximately
  891 MB. Existing PostgreSQL/Redis volumes and credentials must be retained.
- External application health/readiness pass; three agent tunnels are connected.
- Use the explicit `/etc/rancher/k3s/k3s.yaml` kubeconfig for management actions.
  Do not reinstall K3s, CNI, ingress, certificates, monitoring or Charlie.
- Existing K3d clusters are not cleanup targets. Fresh member clusters are
  separate adopted clusters, not replacement management nodes.
- This is an unpublished development revision upgrade using immutable local
  image digests. It is not the signed, newer-tag production promotion workflow.

## Execution plan

1. Preserve current values/manifests/history/Secrets and previous image content
   in a private recovery directory. Capture a custom-format PostgreSQL dump,
   restore it into an isolated temporary database, and verify schema and core
   record counts before deleting only that temporary database.
2. Build all seven first-party images from one identified working-tree snapshot.
   Import them without deleting existing images; record immutable digests.
3. Render the current chart with preserved operator settings and explicit new
   image/schema overrides. Review resource changes and run a server-side dry run.
   Keep preflight hooks and authorization/audit/tenant boundaries enabled.
4. Attempt `helm upgrade --reset-then-reuse-values --atomic --cleanup-on-fail
   --wait --wait-for-jobs` on the existing release. Exercise schema 62 → 66,
   retain the old Helm revision and backup for recovery, and do not recreate the
   database or force migration metadata.
5. Verify rollout, migration cleanliness, TLS-routed health/readiness, UI/API
   access, worker health, existing cluster records and agent reconnection.
6. Enroll two fresh, uniquely named K3d member clusters through the canonical
   authenticated API, using the new agent image. Verify heartbeats, Kubernetes
   proxy access and Flux reconciliation; retain successful test clusters for
   inspection. Do not silently weaken a failed acceptance check.
7. Record exact results, source/image identities, recovery location and any
   limits. If Helm rollback itself fails or database recovery would require
   replacing live data, stop and request direction instead of wiping state.

## Evidence

Private recovery/evidence directory: `/var/tmp/astronomer-dev-upgrade.dABUk7`.
It contains credentials and database material and must not be committed or
attached to public reports. Only sanitized results belong in this document.

## Results — 2026-09-23

- In-place application upgrade succeeded: Helm revision **113 → 114**, without
  reinstalling the release. Preflight and migration hooks passed; migrations
  63–66 ran successfully and the database is clean at **66**.
- The pre-upgrade dump restored into an isolated verification database with
  matching schema and core counts. That temporary database was dropped after
  verification; the original database was unchanged. Original application
  Secret data, PVC UIDs and backing-volume identities were preserved. Users,
  projects and cluster counts matched immediately after upgrade, before test
  enrollment added its own records.
- Seven runtime images were built/imported from source-tree SHA-256
  `1a80bcc00f595e3104cdec38cbac9e7eda12a93c4d40eeb4257601dd5a2fa663`,
  with version `1.2.0-local.1a80bcc00f59`. The source includes uncommitted changes;
  it is not represented as a clean release commit. Immutable image references
  and the previous images are retained in the private evidence directory.
- The user then reported the sidebar multi-open regression. The hook now keeps
  at most one section open, supports explicitly closing it, reveals the active
  section on navigation, isolates global/cluster preferences, and normalizes
  old multi-open saved state. Equivalent navigation metadata refreshes do not
  reopen a manually closed section.
- A **frontend-only revision 114 → 115** deployed this correction. Server-side
  manifest comparison confirmed only the frontend Deployment changed. Its
  image is `localhost/astronomer-frontend@sha256:b893e1349853cd3a149fae699fbc53870a764de0b2fdbd2dd6e9878b7ae90832`,
  version `1.2.0-local.1f45b8f2b91f`, from source-tree SHA-256
  `1f45b8f2b91f580d54e2e83c3b6356600256c84c45bea0036832994436434c3d`.
  Backend/agent images remain the revision-114 build. Subsequent source changes
  only refresh the generated code-health inventory and these status documents.
- Sidebar qualification: **61 targeted tests**, **1,735 frontend tests**,
  type-check and lint pass. The complete frontend enterprise gate passes,
  including code-health, formatter tests, production build, route-tree drift,
  bundle budgets and dependency audit (**0 vulnerabilities**). Complexity and
  patch-whitespace checks pass. The earlier 25-lane Local CI result is separate
  pre-deployment evidence; it was not rerun for this narrow sidebar change.
- Real HTTPS/browser validation passes login with secure session semantics,
  deep-link return, dashboard, cluster list, agents, projects, preferences and
  both new cluster pages. Desktop mouse/Enter/Space and mobile single-section
  behavior pass, with no page exceptions, API 5xx responses or mobile page
  overflow in the checked journeys. Session refresh also passed after the
  verification client's 15-minute access token expired.
- New adopted members **`astro-upgrade-0923-a`** and
  **`astro-upgrade-0923-b`** remain running and registered, both K3s
  v1.35.7+k3s1 with the new agent version. Their Kubernetes API listeners are
  loopback-only ports **16651/16652**. Both passed heartbeat/version, exact
  pinned Flux distribution, generation-current baseline HelmRelease readiness,
  registration `ready`, authenticated Kubernetes proxy create/read/delete,
  durable audit intent/outcome records, and kubectl shell open/close checks.
- All **five registered clusters** have connected tunnels and return Ready
  nodes through Astronomer's authenticated proxy. Existing `astro-k3d-a` and
  `astro-k3d-b` agents intentionally remain on their previous 1.2.0 images;
  their continued operation validates mixed-version compatibility, not an
  in-place member-agent upgrade.
- Management node `astronomer-dev-1-mj` remains Ready. K3s service PID **938597**
  and its original start timestamp are unchanged. Main K3s, PostgreSQL and
  Redis were not restarted. Existing K3d clusters, ingress/TLS/443, the older
  test fixture, and the default kubeconfig context were preserved. Final
  server, worker and frontend Deployments are each ready at 1/1.

## Remaining follow-up and limits

1. **Fresh one-command bootstrap is not clean.** On both empty member clusters,
   the combined registration manifest initially creates Flux CRDs and then
   fails discovery for `OCIRepository` and `Kustomization` custom resources.
   A subsequent apply after CRD establishment succeeds. The second member
   reproduces this with the documented server-side apply/field-manager flags.
   Fix the canonical bootstrap flow to install CRDs, wait for `Established`,
   refresh discovery, then apply dependent resources; add a genuinely empty
   cluster regression check. The accepted members required this explicit
   reapply; no readiness assertion was removed or forced in the database.
2. The existing smoke helper confirms registration after waiting for the first
   heartbeat. An agent can already have advanced to `provisioning`, making that
   late confirm return 409. A follow-up should align the helper with the current
   state machine, confirming while `created` and observing later transitions.
3. Scheduled remote management backups are not configured (missing S3 setup),
   despite the enabled default flags. This exercise used the private,
   restore-tested local backup. It is not off-host disaster-recovery evidence.
4. This tested a development Helm revision upgrade and real schema upgrade,
   not signed production version promotion, failure-triggered rollback,
   management Kubernetes upgrades, or the protected external qualifications
   still tracked by Plan 026. No commit or push was performed.

Key private evidence: `helm-upgrade.json`, `sidebar-helm-upgrade.json`,
`migration-init.log`, `backup-restore-proof.log`, `members-validation.json`,
`fresh-bootstrap-crd-discovery-failure.log`, `browser-validation.json`,
`frontend-enterprise-rerun.log`, and `clusters-final.json`. Initial host-tool and
validation-client failures are retained separately from the successful results.

## Subsequent integration questions — evidence, not additional deployment

- The new-member monitoring deployment proof covers kube-state-metrics and
  node-exporter only. This run did not install and exercise a complete
  Prometheus/Grafana stack or real Loki/log-collector pipeline.
- The previous live-browser Loki journey uses the synthetic response in
  `scripts/testdata/live-browser-fixture/main.go`; it proves the routed UI/API
  query behavior, not a real Loki deployment or log ingestion. Do not count it
  as full logging-stack qualification.
- The previous Trivy journey is different: it deploys the real, pinned operator
  through Flux, waits for a real VulnerabilityReport, and checks ingestion/API/UI.
  Its chart is 0.36.0, operator 0.34.0 and scanner 0.74.0, all digest-pinned.
- Trivy ingestion, scan APIs, the remote-cluster Image Scans page and fleet
  rollups remain implemented. The two-component default bundle catalog does
  not install Trivy; the new member inspected here has zero reports and no
  Trivy CRDs. Management-cluster navigation/page code explicitly hides/disables
  Image Scans when `isLocal` is true.
- The user wants the scanning capability retained/restored. Default-on versus
  opt-in onboarding policy was presented as an explicit choice. No Trivy
  installation or baseline-membership change has been performed by this run.
  Follow-up should validate real scan visibility through the existing Flux
  integration, fix misleading onboarding tool lists, and separately qualify a
  full monitoring/logging install. Management-cluster scan support must be
  validated rather than merely removing the UI guard.
