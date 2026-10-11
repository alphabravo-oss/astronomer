# Local k3s smoke pass and remaining cleanup

Date: 2026-10-11. Branch: `implement/032-technology-guide`.

## Verdict

The user narrowed immediate acceptance to deploying locally, checking for crashes,
and exercising core functions once. That local smoke pass is complete. This does
**not** complete every implementation or qualification task in Plan 032, nor prove
the branch has no dead code, stubs, or guide deviations. A subsequent user question
explicitly asked whether cleanup and guide alignment were actually complete; the
focused review below found real remaining work.

## Deployment and checks

- Helm release `astronomer`, namespace `astronomer`, revision **236**, deployed on
  existing local k3s `v1.35.7+k3s1`.
- Server, worker, and embedded local agent use source
  `dc04f078c5f04352c83d7c47b809b63c59ffe391`. Frontend source is `68543cf7`; the later
  commit changes only backend code/tests. Migration source is `54763ab3`, unchanged
  by the subsequent documentation and runtime fixes.
- Server/worker images: `localhost/astronomer-{server,worker}:plan032-dc04f078`.
  Frontend: `localhost/astronomer-frontend:plan032-68543cf7-r2`. Agent image is also
  built/imported; this management cluster uses the embedded local agent.
- Existing PostgreSQL and Valkey images, data, credentials, and PVCs were retained.
  A private custom-format `pg_dump` was saved before migration; local `pg_restore
  --list` successfully reads its catalog. A restore rehearsal was not performed.
- Canonical migrations 72–81 succeeded; database reports `81|false`.
- Final frontend/server/worker pods are Ready with **zero restarts**. Several
  minutes of final server/worker logs contain no ERROR entries. Worker audit-outbox
  and SIEM dispatch jobs complete successfully.
- 29 positive read probes cover readiness, identity, clusters, projects, catalog,
  delivery, settings, audit, Kubernetes nodes/namespaces/pods/events, metrics,
  vulnerability lists, and tool status. Five initial delivery requests correctly
  rejected missing project scope; all five passed with explicit `project_id`.
- Fresh password login, authenticated identity, and logout pass. The browser was
  signed back in after the logout check.
- Temporary project create/read/update/read-back/delete/confirmed-absence pass.
  The owned project is removed, and its mutations appear in the audit UI.
- Browser pages render: cluster list, node list, catalog, delivery deployments,
  audit history, and general settings. Three existing delivery deployments report
  ready. Nine relevant API/readiness checks were repeated successfully after the
  final backend fixes.
- Fresh local delivery inventory is persisted as object/array/array, with
  `ready=true` and `compatibility_status=compatible`.

The initial local image packaging retained private build-artifact permissions,
preventing non-root binaries/static content from being read. Corrected local
images use executable/readable runtime permissions. Those failed images are not
the final deployed images.

Live testing also exposed two source defects, fixed in `dc04f078`: nil controller
inventory collections wrote JSON null into non-null durable contracts, and the
diagnostic redactor omitted typed slog severity values. Both affected package
suites, full `go vet ./internal/... ./cmd/...`, complexity, and dependency-boundary
checks pass. No production security or compatibility check was weakened.

This is a development deployment using the existing development configuration,
local imported images, and the supported development artifact path. The old
signed v1.2.1 manifest was removed from the local values because it does not
describe these candidate bytes. This is not a new signed release. It does not
qualify disconnected installs, downstream onboarding, every API operation,
CNPG failover/PITR, cloud, scale, or all supported browsers. Existing upstream-CVE
waivers and release qualification deferrals were not expanded or republished.

One existing Flux source-controller standby remains unready on its artifact-port
probe; its peer is healthy and delivery continues. The condition predates this
deployment by over three days. It is not represented as a clean whole-cluster
readiness result.

Private build, Helm values, database backup, logs, and smoke results remain under
`/root/.cache/astronomer-032/local-smoke-68543cf7/`; these contain operational data
and are not committed. Prior Helm revision 233 describes the released baseline;
application rollback and database restoration are separate operations.

## Focused cleanup/guide review

The current [guide](https://technology-selection-guide.aws.ablabs.io/) and its
`llms-full.txt` were retrieved on October 11. This was a focused source review,
not an exhaustive audit of every changed line. Concrete remaining items:

1. **Collector migration is not implemented in the dev stack.**
   `deploy/docker-compose.telemetry.yml` still selects Promtail 3.4.2 and its YAML
   configuration. `deploy/toolchain/versions.json` explicitly defers conversion
   to Alloy under W10, with review due October 16. Grafana/Loki/Tempo/Prometheus
   also retain selected versions under component exceptions; discovering a newer
   version did not deploy it. These are recorded gaps, not completed upgrades.
2. **Six public schemas remain placeholders.** `docs/openapi.yaml` explicitly
   describes CloudProviderSpec, ClusterRegistryConfigLegacy,
   ClusterTemplateResponse, DecommissionStatusResponse,
   NetworkPolicyApplicationResponse, and NetworkPolicyTemplateResponse as
   permissive placeholders. Their real wire shapes need reconciliation before
   claiming fully typed public contracts.
3. **A real audit stub and stale explanation remain.**
   `internal/agent/apiserver_audit.go` still offers `AUDIT_DELIVERY=stub`, which
   returns success while dropping batches. The normal default uses the real
   authenticated tunnel; the file's opening comments incorrectly say the default
   production wiring is a stub with no authentication path. This is both a
   selectable stub path and misleading legacy documentation.
4. **Repository consolidation is not complete.** At `dc04f078`, the diff from
   main has 5,538 changed paths. Of these, 4,019 paths and approximately 703,000
   added text lines are advisor/assurance material, not application code. This
   bulk needs review and consolidation before calling the branch clean; counts
   alone do not establish that individual fixtures or evidence are unnecessary.

Preserved choices include CNPG in the production profile, Flux delivery, pgx/sqlc,
REST/OpenAPI, and TanStack routing. The master plan documents intentional
departures from literal guide prescriptions. The source selects Go 1.27.2 and
Helm SDK v4.3.0; the local build used Go 1.27.2. This smoke pass does not certify
that every selected dependency is today's newest stable release.

**Status:** local deployment/smoke acceptance complete; whole-plan cleanup and
complete guide alignment remain incomplete. Do not replace this distinction with
an all-green checklist.
