# Default image scanning and real add-on validation

User decision: image scanning is default-on, with an explicit opt-out.

## Implementation and acceptance plan

1. Promote the artifact-verified, digest-pinned Trivy Operator chart into the
   canonical Flux baseline. Keep one scanner/report-ingestion implementation.
2. Default new writable-cluster Quick Start registrations on, expose a separate
   image-scanning opt-out, preserve resumed choices, and correct baseline UI
   descriptions. Read-only agents must not install add-ons.
3. Test default and opted-out component selection, generated release contracts,
   frontend behavior, and narrow backend packages.
4. Install Trivy through the offered application/delivery path on a task-owned
   dev member; prove real workload report ingestion and Image Scans rendering.
5. Exercise offered monitoring and logging installation paths on task-owned dev
   members. Require actual metric samples and a uniquely identifiable workload
   log returned through Astronomer, not just Ready pods or simulated responses.
6. Record exact versions, operations, failures, resource limits, and evidence.
   Preserve the management K3s service, ingress, existing storage and services.

## Boundaries

- Existing Ready clusters are not silently re-enrolled or retrofitted.
- Trivy is workload vulnerability scanning, not runtime network enforcement.
- Sideloaded development images may not be readable by a registry-based scanner;
  report any coverage gaps rather than mounting host runtime sockets implicitly.
- Local dev validation does not produce signed production release approval or
  substitute for the full Kubernetes/architecture qualification matrix.

## Implemented

- The canonical baseline now includes Trivy Operator 0.36.0, operator 0.34.0,
  and scanner 0.74.0 with verified chart and multi-platform image digests.
  New writable-cluster Quick Start registrations select it by default.
- The independent checkbox persists `astronomer.io/image-scanning: disabled`
  for opt-out. Viewer/namespace-only agents cannot select the cluster-wide
  baseline. Existing Ready clusters are not silently changed. This preference
  controls initial installation, not live uninstallation.
- Catalog installs into unassigned namespaces resolve the verified,
  system-owned platform project **after** the original target authorization.
  Namespace ownership and caller permissions are not expanded.
- Catalog install/upgrade rollouts now include their required transactional
  audit intent, retaining actor, cluster, project, and operation identity.
- Logging namespace patterns now match actual container-file tags. Invalid
  namespace selections fail closed. Rewrite rules match the tail record's
  `log` field instead of treating `$TAG` as an existing record field.
- Empty delivery conditions persist as `[]`, not SQL-contract-invalid JSON
  `null`. Regression tests cover pending, deleting, and removed observations.

The rewrite correction follows the upstream
[Fluent Bit rule contract](https://docs.fluentbit.io/manual/data-pipeline/filters/rewrite-tag).
The live pre-fix metrics showed records reaching the rewrite filter, zero
emissions, and zero Loki output records; Ready pods alone did not detect it.

## Live validation scope and evidence

These are real installations, not mocked tests. Private evidence and credentials
remain under `/var/tmp/astronomer-dev-upgrade.dABUk7`; no credentials are checked in.

| Add-on | Actual deployment | Result |
| --- | --- | --- |
| Monitoring | Member A, kube-prometheus-stack 61.3.2; Prometheus, Grafana, Alertmanager, KSM, node exporter | Real metric samples and Grafana passed; native summary integration remains incomplete |
| Image scanning | Member B, Trivy Operator 0.36.0 through catalog/Flux | 14 reports ingested at 16:53 UTC; real findings displayed in Image Scans; browser passed |
| Log storage | Member B, Loki 7.3.0 chart / Loki 3.6.11, SingleBinary/filesystem, internal service | Flux Ready; actual workload canary returned through authenticated Astronomer Kubernetes proxy |
| Log collection | Member B, offered Fluent Bit 0.58.1, managed ConfigMap, hot reload | Corrected installation collected/enriched/shipped the canary; native status/lifecycle gaps remain |

Member A is `astro-upgrade-0923-a` (`61947052-58fc-4444-b83c-8718bf696232`).
Member B is `astro-upgrade-0923-b` (`aff2ee54-ff0c-4a6d-8a97-5e9d55176352`).
Both run K3s 1.35.7+k3s1. The management cluster was not used to host these
additional monitoring/logging/scanning stacks.

### Monitoring

Authenticated Astronomer Kubernetes-proxy queries returned 128
`node_cpu_seconds_total` series, one `kube_node_info`, one
`astronomer_cluster_info`, and 12 `up` series. These are series counts, not a
claim that every scrape target is healthy. Grafana's same-origin authenticated
health endpoint returned `database: ok`, version 11.1.0, and its browser view
loaded. Alertmanager was Ready; external notification delivery was not tested.

The stack used six-hour retention and a 2 GiB local-path PVC. No shared
Thanos/S3 backend, HA, remote-write path, or recovery qualification is claimed.
Evidence: `addon-real-metric-samples.json`, `addon-grafana-health.json`,
`addon-browser-validation.json`.

### Scanning

The operator scanned actual workloads, including the deliberately old public
canary image, Loki, Flux controllers, and Fluent Bit. The API snapshot contained
14 reports: 3 critical, 78 high, 43 medium, 61 low, and 744 unknown findings.
These are scanner-reported totals, not a manual vulnerability assessment or a
claim that deployed images are vulnerability-free. The browser showed reports
without JavaScript errors. Local sideloaded development images without a
reachable registry remain a coverage limitation.

Evidence: `addon-trivy-summary.json`, `addon-image-scans-browser.png`.
Installation: `903b870b-829c-4189-998a-5822b1ccd168`.

### Logging

The managed pipeline selects only `addon-validation` and sends to the internal
Loki service. The workload emits
`astronomer-addon-live-20260923-unique-canary`. No synthetic direct Loki push
is used to claim collection success. ConfigMap hot reload was observed.
At 17:17 UTC, Loki returned four actual canary records with the expected cluster
and namespace labels and Kubernetes pod/container metadata. A separate query
for this job outside the selected namespace returned no streams. Evidence:
`addon-loki-canary-query.json` and `addon-loki-namespace-isolation.json`.
After saving the evidence, the temporary canary Deployment was deleted through
the authenticated API. Its manifest is retained privately for recreation; the
add-on stacks and test members remain available.

The chart's default `/etc/machine-id` host mount does not work in this K3d
fixture. The corrected deployment mounts only `/var/log` read-only; no host
machine ID was fabricated. The initial failed `fluent-bit` release is retained
as lifecycle-failure evidence. A separate `fluent-bit-validation` release runs
the corrected values; this is a clean install, **not** a passed recovery upgrade.

Important operation identities:

- Loki install: `405334a1-1c36-44bf-8fdf-718c02b3fce3`.
- Initial Fluent Bit install: `db507d65-2b4a-4d52-9ad0-59cbc56bf573`.
- Blocked corrective upgrade: `303241cd-7643-4724-92bf-4546bd082f47`.
- Uninstall request: `230b6ac8-547c-49c8-aa30-f5da68ac710f`.
- Corrected Fluent Bit install: `dcb6b74e-e9f6-4902-85b5-60053f9c6456`.

## Remaining work — do not call the integration suite fully green

1. **Native monitoring routing:** standalone member Prometheus contains data,
   but Astronomer's native summary returned zeros and stack status was degraded.
   The backend client/reconciler selects the shared/default backend, not this
   standalone member service. Implement explicit, tenant-safe routing and test
   the native summary, workload charts, and status together.
2. **Corrective upgrades from unhealthy state:** the previous unavailable
   deployment consumes the one-cluster availability budget, so the correcting
   rollout never releases its new generation. Add recovery semantics without
   weakening healthy-cluster disruption limits; test failed install -> fix.
3. **Fenced uninstall:** deletion advances the database desired generation from
   1 to 2 while the agent requires the tombstone to match accepted generation 1.
   The old release is consequently not removed. Align tombstone/control
   generations and test install, upgrade-in-progress, disconnect, and uninstall.
   The empty-array fix addresses serialization, not this fencing mismatch.
   After that fix, status ingestion also exposed `acknowledge delivery snapshot:
   no rows in result set`: the incomplete snapshot acknowledgement rolls back
   fresh workload observations. Thus the corrected Fluent Bit HelmRelease is
   Ready while its catalog operation remains pending/running. Qualify partial
   snapshot acknowledgement and status projection together with deletion.
4. **Pipeline PUT:** keeping the same selected output produces a duplicate-key
   violation in `logging_pipeline_outputs_pkey`. Fix replacement query ordering
   with a real PostgreSQL regression test. The enable action works and was used
   to re-render configuration; this is not a passed pipeline-edit test.
5. **Native private-Loki query:** the canary data check uses Astronomer's
   authenticated Kubernetes service proxy. The separate BYO-output query path
   connects directly from management: the actual output-query test returned
   HTTP 502 because member service DNS did not resolve there. Implement a scoped
   member-service transport; do not relax SSRF protections.
6. **Catalog rollback audit:** static inspection shows the separate rollback
   planner call still lacks an audit intent. Install/upgrade validation does not
   qualify rollback. Cover its audit transaction and immutable-version recovery.
7. **Fresh registration matrix:** exercise default-on and explicit opt-out on
   genuinely fresh clusters, plus the previously recorded first-apply CRD
   discovery race. This run tested selection logic, live onboarding controls,
   and a real catalog Trivy install, not fresh default-baseline qualification.
8. **Production qualification:** supported Kubernetes/architecture matrix,
   air-gap database availability, signed release evidence, registry coverage,
   alert delivery, HA/storage sizing, long-running ingestion, and load/recovery
   tests remain separate work. Trivy is not runtime network protection.

## Verification and deployment record

- Full handler suite and delivery-status regressions passed after the final
  fixes; empty-condition race test passed. Final Go vet passed.
- The broad backend run passed build, vet, lint, full Go unit and race suites,
  and API/static contract checks. Its overall gate **failed** the 20% free-disk
  safety guard and its source-stability check (the late status fix was added).
  Do not present it as a green immutable backend qualification.
- Unused, task-created build-stage images were removed to restore headroom.
  They are reproducible caches; running images, backups, volumes, and recovery
  artifacts were preserved. The release-contract check subsequently passed.
- Full final frontend enterprise verification passed: lint, types, formatter
  tests, 282 files / 1,737 tests, production build, bundle checks, npm audit.
  The initial attempt caught a stale generated operation inventory; it was
  regenerated and the complete gate rerun successfully.
- Helm enterprise verification passed. Browser default/opt-out, Image Scans,
  Grafana, and logging-destination checks passed. The full PR matrix was not rerun.
- Final server image: `localhost/astronomer-go-server@sha256:b59ec12d0e9668edbe73a66a4e4a6218a8c3a077268d478d89ce7b396536d215`,
  source tree `385802ed5d53f23904195e6a14d7643df53071f33491b36fb34db08f5e3444c8`.
  This dev image reused the task's compiled builder cache; build before/after
  source hashes matched. Later status-report edits are documentation-only.
- Frontend remains the tested revision-118 image
  `sha256:f0aec82458c88355515141f59c18223e896cdfc12db0415fbe49167c3f979ab2`.
  Worker and member agents retain their earlier deployment artifacts; this is
  not a claim that every component was rebuilt with this source-tree hash.
- Revision 116 was a no-op due to an incorrect values key; 117 deployed the
  corrected server, 118 the frontend, 119 integration fixes, and 120 the
  qualified intermediate server. Final revision/readback recorded below.
- No schema change was introduced. Schema 66 remains clean; the documented
  dev hotfix `image.allowSchemaSkew` override preserves the existing migration
  image. This is not a new migration-path qualification.

## Management node protection

The management K3s service retained PID 938597 throughout; its API/ingress ports,
CNI, node identity, database, Redis, and existing PVCs were not replaced.
During simultaneous build/test pressure the server lost its embedded-agent
leader lease and restarted five times, briefly causing API unavailability.
This was not an OOM-killed database or a K3s restart. Build load was reduced;
revision 120 subsequently remained healthy with all five cluster connections.
Future heavy qualification should run away from the management node.

Final readback at 17:18 UTC: Helm revision **121 deployed**, final server pod
Ready with zero restarts; node Ready with pressure conditions false; K3s PID
unchanged; `/readyz` healthy with database, Redis, schema, critical runtime, and
all **five** cluster connections passing. The real Loki canary check passed.
The original failed Fluent Bit fixture remains visible for lifecycle debugging;
its failed mount means it is not a second running log collector.
