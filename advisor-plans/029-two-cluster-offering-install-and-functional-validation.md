# Plan 029 — Install and functionally qualify every offered application and integration

> **Executor instructions**: Follow this plan in order. Do not install an offered
> application directly with Helm or kubectl. K3d is permitted only to create the
> disposable Kubernetes infrastructure; adopt those clusters through the normal
> Astronomer registration API and drive every offered lifecycle through its
> supported authenticated public API. Stop on any condition listed below instead
> of making a failing case green with an out-of-band repair.
>
> **Drift check (run first)**:
> `git diff --stat f87b189f..HEAD -- advisor-plans/028-all-offerings-api-qualification.md advisor-plans/028-offering-test-inventory.md scripts/qualify-offerings scripts/testdata/offering-qualification docs/openapi.yaml internal/server internal/handler/tools_catalog.go ../astronomer-catalog/catalog.json`
> If inventory, routes, catalog metadata, or runner contracts changed, regenerate
> the denominator before creating either member cluster.

## Status

- **Priority**: P1
- **Effort**: XL, multi-campaign
- **Risk**: HIGH because it installs cluster-scoped controllers and runs recovery tests
- **Depends on**: Plan 028 inventory, runner, and estate preflight
- **Category**: tests / release qualification
- **Planned at**: Astronomer `f87b189f`, 2026-09-25; catalog `3e24ab98`
- **Current state**: estate IaC implemented and statically validated; live execution blocked on cloud credentials, a reviewed provider plan, an internally consistent candidate, and functional `run` mode

## Decision: use one K3d host and three native K3s hosts

Use the independently versioned
[`astronomer-full-test-iac`](../../astronomer-full-test-iac/README.md) project at
commit `c47492f` to create the estate on either AWS or Hetzner Cloud. The current
host continues to run only the management K3s plane, its database/Redis, central
monitoring components, and the browser/API runner. Running members and every
heavy controller here would recreate the memory and disk contention that
previously made the machine unstable and would make failures ambiguous.

The selected estate has four additional machines:

- three separate native Linux machines forming one K3s embedded-etcd cluster;
- one 48–64 GiB Linux host running two independent K3d clusters, each with one
  server and two agents.

Two worker nodes in one K3d cluster are not sufficient because cross-cluster
routing, isolation, placement, disconnected-member behavior, and federated
monitoring need distinct cluster identities. Three K3d containers on one machine
also cannot prove real node loss. The native cluster supplies three actual
failure domains and dedicated 200 GiB test disks; the K3d host supplies cheap
parallel cluster identities and a dedicated 500 GiB Docker disk.

Terraform owns cloud networking, SSH ingress, machines, disks and expiry/owner
metadata. Ansible owns the declared K3s/K3d host configuration. Astronomer owns
its generated agent manifest and every offered workload after adoption. SSH may
observe or run a reviewed failure injection; it must never repair a product
failure. Change Terraform, Ansible, or Astronomer source and replay the estate.

The defaults are Hetzner `3 × ccx23 + 1 × ccx43` or AWS
`3 × m7i.xlarge + 1 × m7i.4xlarge`. Keep at least 25% RAM and 20% disk free;
preflight must stop before mutation below either threshold. Run heavy overlapping
families serially on the K3d host.

K3d is valid for K3s behavior, ordinary installs, cross-cluster isolation,
monitoring/logging canaries, policy, mesh, backup to object storage, and API
lifecycle testing. It cannot by itself qualify:

- EKS, AKS, GKE, RKE, or RKE2-specific CIS profiles;
- physical-host or availability-zone failure;
- a production CSI driver's snapshot behavior when only local-path storage exists;
- Longhorn durability across real host loss, because all K3d nodes share one host;
- real cloud identity, DNS, object-storage, notification, or logging providers.

Those cases remain visible in the same report and run later on the specialized
fixtures listed below. They are `BLOCKED`, never silently skipped or counted as a
K3d pass.

## Qualification topology and trust boundaries

| Lane       | Runs where                          | Purpose                                                                                                                                    |
| ---------- | ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `MGMT`     | Existing management K3s host        | Bundled Dex, local auth, direct SSO, SCIM, shared observability, global settings, extensions, Charlie, management backup/restore reporting |
| `K3D-A`    | Additional host                     | Primary clean-install, upgrade, rollback, failure/retry, workload-canary and cleanup target                                                |
| `K3D-B`    | Additional host                     | Isolation, cross-cluster/federated behavior, alternate registration choices, disconnect/reconnect and parallel-read target                 |
| `K3S-HA`   | Three native cloud machines         | Embedded-etcd quorum, real node loss, native service/reboot/network behavior, Longhorn/CloudNativePG failover and storage recovery         |
| `RECEIVER` | Additional host or provider sandbox | Real Vault, MinIO, registry, Git, SMTP, syslog, webhook and other task-owned protocol endpoints with independent evidence APIs             |
| `PROVIDER` | Vendor sandboxes                    | AWS, Azure, GCP, DigitalOcean, Slack, PagerDuty, Teams, Datadog, Splunk, Okta and other named integrations                                 |
| `SPECIAL`  | Disposable real VMs/cloud clusters  | CSI snapshots, real multi-host storage/failover, distribution-specific CIS and cloud-managed Kubernetes behavior                           |

The member agents initiate their normal authenticated connection to
`https://astronomer.dev.alphabravo.io`. Do not expose Docker daemons or K3d API
ports publicly. Bind K3d APIs to loopback on the K3d host and use SSH only for
infrastructure preparation and independent observation. Product mutations still
go through Astronomer APIs. Use the public TLS origin without `-k`, CA bypasses,
forged sessions, database writes, direct worker invocation, or generated-resource
patches.

## Current state and gaps the executor must preserve

- `scripts/qualify-offerings` currently implements `inventory`, `preflight`, and
  `verify`. Functional `run` mode is still missing.
- The live GET-only inventory matches six registry families, but all 174
  functional cases remain `NOT_RUN`.
- The current estate preflight blocks correctly: only the local management
  cluster exists and it cannot be a mutation target.
- The deployed development release has component-version/schema-skew overrides.
  Rebuild and deploy all first-party images from one commit before qualification.
- The 21 catalog applications and 12 Tools have a 25-name union because eight
  products have both entrypoints. Both paths must be tested independently and
  never installed concurrently under competing owners.
- Dex is bundled into the management chart. `TOOL-02` must reject member install
  and direct the operator to Auth/Dex; its working behavior is proved by the
  identity cases on `MGMT`.
- Four catalog applications advertise no rollback: kube-prometheus-stack, Loki,
  Longhorn, and CloudNativePG. Their rollback test must prove truthful API
  rejection without side effects.
- Historical K3d evidence is useful diagnostic input only. The old members were
  removed, and no prior result carries into this campaign.

## Commands and expected gates

| Purpose                   | Command                                                                                                                                                                                                        | Expected result                                          |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- |
| Runner tests              | `go test ./scripts/qualify-offerings/... -count=1`                                                                                                                                                             | exit 0                                                   |
| False-green/schema tests  | `python3 scripts/tests/offering_qualification_test.py`                                                                                                                                                         | all tests pass                                           |
| Inventory                 | `go run ./scripts/qualify-offerings inventory --config "$QUAL_CONFIG" --output "$QUAL_EVIDENCE/inventory.json"`                                                                                                | six or more reconciled registries, zero unmapped entries |
| Estate preflight          | `go run ./scripts/qualify-offerings preflight --config "$QUAL_CONFIG" --output "$QUAL_EVIDENCE/estate-preflight.json"`                                                                                         | `PASS`, two distinct non-local ready clusters            |
| Functional campaign       | `go run ./scripts/qualify-offerings run --config "$QUAL_CONFIG" --cases scripts/testdata/offering-qualification/cases.json --inventory "$QUAL_EVIDENCE/inventory.json" --output "$QUAL_EVIDENCE/results.json"` | checkpoints every case; no silent skip                   |
| Final verifier            | `go run ./scripts/qualify-offerings verify --cases scripts/testdata/offering-qualification/cases.json --report "$QUAL_EVIDENCE/results.json"`                                                                  | exit 0 only when every required case passes              |
| Browser companion         | `cd frontend && npx playwright test --config tests/offering-qualification/playwright.config.ts`                                                                                                                | real login; no smoke stubs or injected auth              |
| Product gates after a fix | `make verify-enterprise VERIFY_SCOPE=all`                                                                                                                                                                      | authoritative static gates pass                          |

The functional and browser commands are target interfaces from Plan 028. Their
implementation is part of this plan; do not pretend they exist until their tests
and help output pass.

## What counts as a pass for every installable entry

Each supported entrypoint must record these dimensions separately:

1. Inventory and prerequisites: exact offered slug/version/preset and target
   compatibility are returned by the API.
2. Preview: rendered ownership, namespace, images, resources, CRDs and conflicts
   are accurate without mutation.
3. Install acceptance: authenticated request, idempotency key, audit identity,
   operation ID and sanitized response are recorded.
4. Reconciliation: operation reaches a terminal success state; installed status,
   desired/observed generation, version, revision and owner agree after a fresh
   session.
5. Functional canary: the promised behavior occurs using a real workload or
   provider protocol. Ready pods and HTTP 202 are insufficient.
6. Authorization and isolation: read-only/wrong-project/wrong-cluster callers are
   denied with no side effect; K3D-A data never appears in K3D-B scope.
7. Update/upgrade: configuration and version change become observable through the
   normal APIs; failed updates recover through the advertised retry path.
8. Rollback: succeeds to the prior immutable generation when advertised, or is
   rejected without mutation when unsupported.
9. Restart/disconnect: state and operation receipts survive a new client session
   and the applicable server, worker, agent or member restart.
10. Uninstall and cleanup: only run-owned resources disappear; retained data and
    unrelated objects remain; the API reports the final absence.

## Complete offering denominator

The following 174 rows are the minimum denominator. Phase 0 must add newly
returned runtime entries before execution; it may not remove a row because a
credential or specialized fixture is unavailable.

### Curated applications (21)

**Default lane:** K3D-A, then K3D-B where cross-cluster behavior applies.
APP-13 and APP-21 use K3D-A for ordinary lifecycle coverage and K3S-HA for the
required storage, reschedule and real-node-loss canaries.

| Case     | Offering or contract                                |
| -------- | --------------------------------------------------- |
| `APP-01` | Constellation (constellation)                       |
| `APP-02` | Kube State Metrics (kube-state-metrics)             |
| `APP-03` | Prometheus Node Exporter (prometheus-node-exporter) |
| `APP-04` | Metrics Server (metrics-server)                     |
| `APP-05` | Kube Prometheus Stack (kube-prometheus-stack)       |
| `APP-06` | Grafana (grafana)                                   |
| `APP-07` | Loki (loki)                                         |
| `APP-08` | Trivy Operator (trivy-operator)                     |
| `APP-09` | cert-manager (cert-manager)                         |
| `APP-10` | Ingress NGINX (ingress-nginx)                       |
| `APP-11` | External Secrets Operator (external-secrets)        |
| `APP-12` | Kyverno (kyverno)                                   |
| `APP-13` | Longhorn (longhorn)                                 |
| `APP-14` | OPA Gatekeeper (gatekeeper)                         |
| `APP-15` | Fluent Bit (fluent-bit)                             |
| `APP-16` | ExternalDNS (external-dns)                          |
| `APP-17` | Velero (velero)                                     |
| `APP-18` | OpenTelemetry Collector (opentelemetry-collector)   |
| `APP-19` | Grafana Tempo (tempo)                               |
| `APP-20` | KEDA (keda)                                         |
| `APP-21` | CloudNativePG (cloudnative-pg)                      |

### Tools entrypoints (12)

**Default lane:** K3D-A/B; TOOL-02 is a management-plane contract and member rejection test.
TOOL-10 repeats its destructive durability canary on K3S-HA.

| Case      | Offering or contract       |
| --------- | -------------------------- |
| `TOOL-01` | CIS Scanner (cis-operator) |
| `TOOL-02` | Dex (dex)                  |
| `TOOL-03` | Fluent Bit                 |
| `TOOL-04` | Trivy Operator             |
| `TOOL-05` | cert-manager               |
| `TOOL-06` | Gatekeeper                 |
| `TOOL-07` | kube-state-metrics         |
| `TOOL-08` | Prometheus Node Exporter   |
| `TOOL-09` | Ingress NGINX              |
| `TOOL-10` | Longhorn                   |
| `TOOL-11` | NeuVector                  |
| `TOOL-12` | Istio                      |

### Fresh-adoption baseline combinations (6)

**Default lane:** Fresh K3D-A/B registration permutations.

| Case      | Offering or contract                                                        |
| --------- | --------------------------------------------------------------------------- |
| `BASE-01` | Metrics baseline on; image scanning on/default                              |
| `BASE-02` | Metrics baseline on; image scanning explicit opt-out                        |
| `BASE-03` | Metrics baseline off; image scanning on/default                             |
| `BASE-04` | Metrics baseline off; image scanning explicit opt-out                       |
| `BASE-05` | Resumed registration across all four baseline combinations                  |
| `BASE-06` | Already adopted or formerly restricted agent; no silent privilege expansion |

### Identity and access (19)

**Default lane:** MGMT plus a real external identity-provider sandbox.

| Case    | Offering or contract               |
| ------- | ---------------------------------- |
| `ID-01` | Dex generic OIDC                   |
| `ID-02` | Dex Okta                           |
| `ID-03` | Dex Microsoft / Entra ID           |
| `ID-04` | Dex GitHub                         |
| `ID-05` | Dex GitLab                         |
| `ID-06` | Dex Bitbucket Cloud                |
| `ID-07` | Dex Google Workspace               |
| `ID-08` | Dex SAML                           |
| `ID-09` | Dex LDAP / Active Directory        |
| `ID-10` | Dex generic OAuth                  |
| `ID-11` | Direct GitHub SSO preset           |
| `ID-12` | Direct Google Workspace preset     |
| `ID-13` | Direct Entra ID (azure-ad) preset  |
| `ID-14` | Direct GitLab preset               |
| `ID-15` | Direct Okta preset                 |
| `ID-16` | Direct generic OIDC                |
| `ID-17` | SCIM discovery and Users           |
| `ID-18` | SCIM Groups and group mappings     |
| `ID-19` | Local identity, MFA and API tokens |

### Managed observability (8)

**Default lane:** MGMT shared services plus K3D-A/B canaries.

| Case     | Offering or contract                                      |
| -------- | --------------------------------------------------------- |
| `OBS-01` | Cluster monitoring stack                                  |
| `OBS-02` | Shared Thanos                                             |
| `OBS-03` | Shared Alertmanager                                       |
| `OBS-04` | Shared Grafana                                            |
| `OBS-05` | Shared Loki / hosted Loki                                 |
| `OBS-06` | Bring-your-own monitoring endpoints/backend configuration |
| `OBS-07` | Shared vs standalone member routing                       |
| `OBS-08` | External dashboard links, query widgets and saved views   |

### Logging (11)

**Default lane:** K3D-A/B collectors plus real/self-hosted destination APIs.

| Case     | Offering or contract                     |
| -------- | ---------------------------------------- |
| `LOG-01` | Elasticsearch                            |
| `LOG-02` | OpenSearch                               |
| `LOG-03` | Loki                                     |
| `LOG-04` | Splunk                                   |
| `LOG-05` | CloudWatch                               |
| `LOG-06` | Datadog                                  |
| `LOG-07` | S3                                       |
| `LOG-08` | Syslog                                   |
| `LOG-09` | Pipeline filters and namespace selection |
| `LOG-10` | Pipeline edit/reload/lifecycle           |
| `LOG-11` | Native destination query/saved search    |

### Notifications and email (7)

**Default lane:** MGMT dispatch plus real provider/receiver sandboxes.

| Case      | Offering or contract                 |
| --------- | ------------------------------------ |
| `SEND-01` | Slack notification channel           |
| `SEND-02` | Email notification channel           |
| `SEND-03` | PagerDuty channel                    |
| `SEND-04` | Generic webhook notification channel |
| `SEND-05` | Microsoft Teams channel              |
| `SEND-06` | SMTP / transactional email           |
| `SEND-07` | Platform event webhooks              |

### SIEM transports and formats (9)

**Default lane:** MGMT dispatch plus task-owned receiver or real Splunk.

| Case      | Offering or contract                         |
| --------- | -------------------------------------------- |
| `SIEM-01` | Syslog UDP                                   |
| `SIEM-02` | Syslog TCP                                   |
| `SIEM-03` | Syslog TLS                                   |
| `SIEM-04` | Splunk HEC                                   |
| `SIEM-05` | NDJSON HTTPS                                 |
| `SIEM-06` | RFC5424 format                               |
| `SIEM-07` | RFC3164 format                               |
| `SIEM-08` | CEF format                                   |
| `SIEM-09` | NDJSON format and automatic format selection |

### Vault and secret backends (5)

**Default lane:** Task-owned real Vault plus K3D-A/B consuming workloads.

| Case        | Offering or contract  |
| ----------- | --------------------- |
| `SECRET-01` | Vault token auth      |
| `SECRET-02` | Vault AppRole auth    |
| `SECRET-03` | Vault Kubernetes auth |
| `SECRET-04` | Vault KV v1           |
| `SECRET-05` | Vault KV v2           |

### Cloud credentials (5)

**Default lane:** Real provider sandboxes; Generic also runs on K3D-A.

| Case       | Offering or contract     |
| ---------- | ------------------------ |
| `CLOUD-01` | AWS credentials          |
| `CLOUD-02` | GCP credentials          |
| `CLOUD-03` | Azure credentials        |
| `CLOUD-04` | DigitalOcean credentials |
| `CLOUD-05` | Generic credentials      |

### Registries, repositories, delivery sources, and catalogs (7)

**Default lane:** MGMT control APIs plus K3D-A/B delivery targets.

| Case        | Offering or contract                                 |
| ----------- | ---------------------------------------------------- |
| `SOURCE-01` | Docker Registry v2-compatible registry configuration |
| `SOURCE-02` | Helm HTTP(S) repositories                            |
| `SOURCE-03` | OCI sources/registries                               |
| `SOURCE-04` | Git delivery sources                                 |
| `SOURCE-05` | Admin GitOps configuration sources                   |
| `SOURCE-06` | Custom/application catalogs                          |
| `SOURCE-07` | Disconnected mirrors/bundles                         |

### Backup and restore (13)

**Default lane:** K3D-A/B; MGMT for DR-11..13; specialized CSI/storage lane where required.

| Case    | Offering or contract                                            |
| ------- | --------------------------------------------------------------- |
| `DR-01` | Velero application installation                                 |
| `DR-02` | Cluster snapshots and restore                                   |
| `DR-03` | Cluster snapshot schedules                                      |
| `DR-04` | General Velero backup storage/schedules/restores                |
| `DR-05` | S3 / AWS backup storage                                         |
| `DR-06` | MinIO / compatible S3 backup storage                            |
| `DR-07` | GCS backup storage                                              |
| `DR-08` | Azure Blob backup storage                                       |
| `DR-09` | CSI volume snapshot mode                                        |
| `DR-10` | Node-agent/filesystem volume backup mode                        |
| `DR-11` | Control-plane snapshot creation, inventory and restore guidance |
| `DR-12` | Management restore-drill reporting                              |
| `DR-13` | Management-plane backup lifecycle                               |

### Policy and compliance (8)

**Default lane:** K3D-A/B.

| Case        | Offering or contract                    |
| ----------- | --------------------------------------- |
| `POLICY-01` | PSA privileged, baseline, restricted    |
| `POLICY-02` | Gatekeeper privileged-container policy  |
| `POLICY-03` | Gatekeeper latest-image-tag policy      |
| `POLICY-04` | Gatekeeper required-label policy        |
| `POLICY-05` | Custom Gatekeeper templates/constraints |
| `POLICY-06` | Network-policy templates                |
| `POLICY-07` | API-server allowlist                    |
| `POLICY-08` | Compliance baselines/posture/export     |

### CIS profiles (7)

**Default lane:** K3D for CIS-01/CIS-04; real distributions for CIS-02/03/05/06/07.

| Case     | Offering or contract   |
| -------- | ---------------------- |
| `CIS-01` | Generic Kubernetes CIS |
| `CIS-02` | RKE / RKE1 CIS         |
| `CIS-03` | RKE2 CIS               |
| `CIS-04` | K3s CIS                |
| `CIS-05` | EKS CIS                |
| `CIS-06` | AKS CIS                |
| `CIS-07` | GKE CIS                |

### Service mesh (5)

**Default lane:** K3D-A/B for install/detection; additional preinstalled mesh fixtures for Linkerd/Kuma/Cilium.

| Case      | Offering or contract              |
| --------- | --------------------------------- |
| `MESH-01` | Istio detection/posture/inventory |
| `MESH-02` | Linkerd detection/posture         |
| `MESH-03` | Kuma detection                    |
| `MESH-04` | Cilium detection                  |
| `MESH-05` | Istio managed resource types      |

### Security ingestion (2)

**Default lane:** K3D-A/B workloads and audit ingestion.

| Case     | Offering or contract                    |
| -------- | --------------------------------------- |
| `SEC-01` | Image Scans API / fleet vulnerabilities |
| `SEC-02` | API-server audit ingestion              |

### UI extension platform (13)

**Default lane:** MGMT browser/API host with K3D-A/B scoped data.

| Case     | Offering or contract      |
| -------- | ------------------------- |
| `EXT-01` | Sidebar extension mount   |
| `EXT-02` | Dashboard widget mount    |
| `EXT-03` | Cluster tab mount         |
| `EXT-04` | Settings page mount       |
| `EXT-05` | Declarative table         |
| `EXT-06` | Declarative chart         |
| `EXT-07` | Declarative stat          |
| `EXT-08` | Declarative form          |
| `EXT-09` | Signed bundle/iframe      |
| `EXT-10` | Astronomer API data proxy |
| `EXT-11` | Kubernetes data proxy     |
| `EXT-12` | Prometheus data proxy     |
| `EXT-13` | Extension lifecycle       |

### Charlie assisted operations (4)

**Default lane:** MGMT plus the exact qualified Charlie companion.

| Case    | Offering or contract                    |
| ------- | --------------------------------------- |
| `AI-01` | Charlie connection/onboarding/modes     |
| `AI-02` | Charlie sessions/threads/context        |
| `AI-03` | Charlie commands/capabilities/approvals |
| `AI-04` | Charlie triggers/findings/remediation   |

### Cross-cutting platform workflows (12)

**Default lane:** MGMT and both members; destructive control-plane cases use a disposable management fixture.

| Case          | Offering or contract                                           |
| ------------- | -------------------------------------------------------------- |
| `PLATFORM-01` | Cluster adoption, agent connectivity/profiles and lifecycle    |
| `PLATFORM-02` | Projects, namespaces, membership, RBAC and quotas              |
| `PLATFORM-03` | Cluster templates and groups                                   |
| `PLATFORM-04` | Delivery sources/bundles/versions/targets/rollouts             |
| `PLATFORM-05` | Apps catalog discovery/repositories/installed releases         |
| `PLATFORM-06` | Workloads, resources, CRDs and discovery                       |
| `PLATFORM-07` | Logs, events, exec/shell and kubeconfig                        |
| `PLATFORM-08` | Alert rules/events/silences/inhibition/anomaly views           |
| `PLATFORM-09` | Audit, read-audit policies, retention and compliance export    |
| `PLATFORM-10` | General/platform settings, feature flags, branding/preferences |
| `PLATFORM-11` | Dashboard widgets/templates and support bundles                |
| `PLATFORM-12` | System inventory, compatibility and operation controllers      |

## Offering-specific functional canaries

The 174 rows above are the result denominator. The following canary families
state the minimum real effect; the detailed request, negative, recovery and
cleanup assertions still apply to each row.

| Family                                                        | Minimum real proof                                                                                                                                                                                                              |
| ------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Catalog applications and overlapping Tools                    | Use the dedicated API lifecycle, then prove the application's advertised effect. Test the second entrypoint only after removing the first owner.                                                                                |
| Metrics Server, kube-state-metrics, node exporter, Prometheus | Create/change a workload and retrieve fresh pod, object-state and node series through Astronomer's native metric APIs with correct cluster/namespace scope.                                                                     |
| Grafana, Loki, Tempo, OpenTelemetry                           | Render a real dashboard query; emit and retrieve unique logs and related spans; prove tenant isolation and sample freshness.                                                                                                    |
| Trivy and NeuVector                                           | Scan a registry-reachable pinned canary image and correlate digest, workload, reports and policy/runtime behavior. Never count an unscannable sideloaded image as success.                                                      |
| cert-manager and Ingress NGINX                                | Issue/renew a trusted task-owned certificate and route mapped HTTP/TLS host/path traffic to a canary; prove update and cleanup.                                                                                                 |
| External Secrets and Vault                                    | Materialize a task-owned upstream value into a consuming workload, compare only hashes, rotate it and observe reconciliation.                                                                                                   |
| Kyverno and Gatekeeper                                        | Accept a compliant workload, reject a violating workload, read the real report/audit result, then remove only the run-owned policy.                                                                                             |
| Longhorn and CloudNativePG                                    | Write data, force supported reschedule/failover, read the same checksum, exercise snapshot/backup recovery, and verify retention. K3d proves container-node behavior only; real-host loss belongs to `SPECIAL`.                 |
| ExternalDNS                                                   | Create/update/delete records in a task-owned real zone and verify authoritative answers plus ownership-safe cleanup.                                                                                                            |
| Velero                                                        | Back up manifests and persistent bytes, mutate the source, restore into an isolated destination, and compare pre-backup hashes. Use the API operation receipt; direct Helm/kubectl lifecycle is invalid.                        |
| KEDA                                                          | Drive a real offered scaler trigger and observe replicas rise and return to idle without manual scaling.                                                                                                                        |
| CIS, policy, mesh and audit                                   | Run the applicable real profile, validate enforced traffic/admission behavior, and prove audit/report ingestion. A K3d result applies only to generic/K3s profiles.                                                             |
| Logging, alerts, SIEM and email                               | Emit a unique source event; observe it at the configured real receiver; verify retry/dedup, credential rotation, filtering, disable and removal. A self-hosted receiver qualifies only its actual protocol, not a named vendor. |
| Dex, direct SSO, SCIM and local auth                          | Complete a real login/callback, verify identity/groups/effective RBAC, then logout/revoke/disable. Redirect or configuration readback alone does not pass.                                                                      |
| Extensions                                                    | Install/enable through API, use a fresh browser session, render the declared mount with real scoped data, exercise its action, then disable and prove denial/disappearance.                                                     |
| Charlie                                                       | Connect the exact companion build, enumerate capabilities, produce a real response/action/finding, verify approval/audit/effect, and disconnect cleanly.                                                                        |
| Delivery/platform workflows                                   | Source → immutable version → target → rollout → canary workload, including approvals, pause/resume/abort/retry/rollback, drift, RBAC and durable operation readback.                                                            |

## External and specialized prerequisite manifest

Before starting a family, record its fixture owner, credential-file reference,
expiry, allowed resources and cleanup API. Never place credential values in Git
or the evidence report.

| Fixture                               | Required by                                      | Acceptable proof                                                                  |
| ------------------------------------- | ------------------------------------------------ | --------------------------------------------------------------------------------- |
| Real OIDC/OAuth/SAML/LDAP sandboxes   | ID-01..16                                        | Provider-issued login/assertion and resulting `/auth/me` identity/groups          |
| SCIM client                           | ID-17..18                                        | Standards client calls plus observable RBAC changes                               |
| Vault instance                        | SECRET-01..05                                    | Real token, AppRole and Kubernetes auth; KV v1/v2 reads consumed by a workload    |
| MinIO                                 | DR-06, local S3/logging protocol checks          | Real S3-compatible API and object checksums; does not qualify AWS                 |
| AWS account                           | CLOUD-01, LOG-05, DR-05, ExternalDNS AWS variant | Task-owned IAM scope and destination/API evidence                                 |
| GCP project                           | CLOUD-02, DR-07, GKE CIS                         | Task-owned service account, bucket and GKE fixture                                |
| Azure subscription                    | CLOUD-03, DR-08, AKS CIS                         | Task-owned identity, Blob container and AKS fixture                               |
| DigitalOcean project                  | CLOUD-04                                         | Task-owned credential and real consuming API/workload                             |
| Slack, PagerDuty, Teams               | SEND-01/03/05                                    | Real sandbox destination receipt and lifecycle                                    |
| Datadog and Splunk                    | LOG-04/06, SIEM-04                               | Real intake plus provider-side search/incident evidence                           |
| DNS zone                              | APP-16                                           | Authoritative create/update/delete evidence                                       |
| Registry, Helm, OCI and Git endpoints | SOURCE-01..06                                    | Authenticated source resolution, immutable digest/revision and real rollout       |
| Offline mirror                        | SOURCE-07                                        | Network-isolated resolution from mirrored, verified artifacts                     |
| CSI-capable multi-node cluster        | DR-09 and storage recovery                       | Real VolumeSnapshot/restore using a supported driver                              |
| Multi-host/VM cluster                 | APP/TOOL Longhorn and CloudNativePG failover     | Loss of a real node/VM with data recovery, not another container on the same host |
| RKE/RKE2/EKS/AKS/GKE clusters         | CIS-02/03/05/06/07                               | Actual matching distribution/profile; no K3s substitution                         |
| Exact Charlie companion               | AI-01..04                                        | Immutable qualified version and real capability registry                          |

## Ordered execution

### Phase 0 — obtain and freeze the infrastructure inventory

1. Copy `astronomer-full-test-iac/.env.example` to its ignored `.env`. Select one
   provider, a unique run ID, an explicit operator `/32`, an expiry no more than
   14 days away, and the frozen K3s/K3d versions.
2. Run `scripts/preflight.sh` and `scripts/keygen.sh`. Keep the private key,
   provider credentials, Astronomer token and Terraform state under their
   documented owner-only paths; never add them to Git or evidence.
3. Run `scripts/plan.sh`. Review the saved Terraform plan for exactly three
   native nodes, one K3d host, four data volumes, one private network and SSH-only
   public ingress. Record its SHA-256 digest before apply.
4. Reserve the generated cluster names, loopback API ports, namespaces, projects,
   DNS names and run ID. Nothing may be selected by “first active cluster.”

**Gate:** static Terraform and Ansible validation passes, the exact saved plan is
reviewed, the run-scoped key is mode 0600, and no cloud resource exists yet.

### Phase 1 — build one internally consistent candidate

1. Freeze an Astronomer commit, catalog commit, chart archive and every enabled
   image digest. Build server, worker, agent, migrate, frontend, shell and DR from
   that one commit.
2. Deploy the management chart without `image.allowSchemaSkew`; include bundled
   Dex and only the central features required for the first wave.
3. Run `/healthz`, `/readyz`, schema/version/image readback, static enterprise
   gates and current browser smoke. Record all digests before member adoption.
4. Any product defect gets its own reviewed PR and a completely rebuilt candidate.
   Never patch the live deployment and continue the same evidence run.

**Gate:** every running first-party image maps to the frozen commit, migrations
match the server schema, and no development override is active.

### Phase 2 — create the K3d and native K3s estate

1. Run `astronomer-full-test-iac/scripts/up.sh` to apply only the digest-verified
   Terraform plan, pin first-seen SSH host keys, and execute the reviewed Ansible
   configuration.
2. Create one native three-server K3s embedded-etcd cluster and two independent
   K3d clusters with one server plus two agents each. Keep Kubernetes and K3d API
   ports private; owner-only kubeconfigs use loopback SSH tunnels.
3. Install only declared infrastructure prerequisites. Do not preinstall an
   offering that Astronomer is supposed to install. Terraform/Ansible drift or a
   failed bootstrap requires source correction and estate replay, not an SSH fix.
4. Run `scripts/validate-infra.sh`; retain the nine-node readiness, three distinct
   cluster UIDs, disk mounts, SSH fingerprints and exact provider inventory.
5. Keep `scripts/down.sh` guarded by the Terraform run/provider identity and
   Astronomer decommission result. Expiry labels are evidence and cleanup aids;
   they do not replace explicit teardown.

**Gate:** both clean clusters have three Ready Kubernetes nodes, distinct cluster
UIDs, reachable API servers over SSH, no Astronomer agent, and no offered add-ons.

### Phase 2b — prove the native cluster's infrastructure boundary

1. Verify all three native machines are separate provider instances in a spread
   placement and that each dedicated test disk mounts at `/var/lib/longhorn`.
2. Verify embedded-etcd has three healthy members before any Astronomer workload.
3. Stop/restart K3s on one non-seed node, then perform a real provider-level
   reboot. Prove the same cluster UID and three Ready nodes return without editing
   the host.
4. Reserve destructive node-loss, Longhorn replica relocation and CloudNativePG
   failover for their ordered cases after adoption and baseline evidence.

**Gate:** the native cluster survives a real node restart/reboot, preserves its
identity, and needs no interactive repair.

### Phase 3 — adopt all three clusters through supported APIs

1. Use normal API-token issuance. Create an explicit project and unique namespace
   for K3D-A, K3D-B and K3S-HA through the public API.
2. Create each cluster registration, save its exact ID and registration receipt,
   download the API-produced manifest, and apply it once with the documented
   server-side field manager. This is infrastructure bootstrap, not an install
   workaround.
3. Confirm registration through the API. Poll operation/events until both agents
   are Ready, fresh and full-management capable. Do not trust legacy profile text
   alone; run the supported diagnostics/readiness contract.
4. Run the six BASE combinations across clean rebuilds. Baseline Trivy/exporter
   installation must occur only through the normal registration/delivery path.
5. Assign exact projects/namespaces and run Plan 028 estate preflight.

**Gate:** `estate-preflight.json` reports PASS for three distinct, non-local Ready
members, matching projects/namespaces and current heartbeats.

### Phase 4 — finish the resumable functional runner

Implement `run` mode before broad installation. It must consume the frozen
inventory, build a dependency DAG, lock cluster-scoped/shared fixtures, persist
operation receipts before polling, enforce timeouts, resume after interruption,
run independent cases only on disjoint resources, and clean in reverse dependency
order. Add adapters for public Astronomer APIs and declared receiver/provider
APIs only. Add no hidden kubectl repair path.

Tests must reject: 202 forever, 200 with `ok:false`, success with no effect,
stale generation/sample, wrong cluster, missing pagination, missing credential,
unexpected skip, duplicate mutation, dependency uninstall while consumers exist,
cleanup failure and unknown offering/preset.

**Gate:** runner/unit/schema tests pass and a synthetic failed operation cannot be
reported PASS.

### Phase 5 — execute local waves on K3D-A/B

Run heavy or overlapping owners serially. Keep K3D-B lightweight until a case
needs cross-cluster data.

1. **Foundation:** PLATFORM-01/02/05/06/07/09/12 and BASE-01..06.
2. **Core application plumbing:** APP-02/03/04/09/10 and TOOL-05/07/08/09.
3. **Observability/logging:** APP-05/06/07/15/18/19,
   TOOL-03, OBS-01..08, LOG-03/08..11 and SEND-04/06/07.
4. **Policy/security:** APP-08/12/14, TOOL-01/04/06/11,
   POLICY-01..08, CIS-01/04 and SEC-01/02.
5. **Mesh/scaling:** APP-20, TOOL-12 and MESH-01/05. Run Linkerd/Kuma/Cilium
   detection only in separately declared preinstalled fixtures.
6. **Secrets/sources/delivery:** APP-11, SECRET-01..05, SOURCE-01..06,
   PLATFORM-03/04/08/10/11.
7. **Backup/storage local capability:** APP-17, DR-01..06/10, then APP-13/21
   for K3d-supported behavior. Preserve the `SPECIAL` limitations.
8. **Remaining app-specific cases:** APP-01 and any catalog entry added by runtime
   reconciliation.
9. For every application that overlaps a Tool, uninstall and prove absence before
   exercising the other owner. Never adopt an artifact left by the first path
   unless the specific case is the advertised adoption test.

After each wave, run the comprehensive verifier. Keep FAIL evidence, fix the
product in a separate PR, rebuild the whole candidate and rerun the affected
case plus its dependencies from a clean fixture.

### Phase 6 — execute management-plane cases

1. Run TOOL-02 against a member and prove rejection/no side effect.
2. Run bundled Dex ID-01..10, direct SSO ID-11..16, SCIM ID-17/18 and local auth
   ID-19 using real sandboxes and fresh sessions.
3. Run shared observability management portions, notification/SIEM configuration,
   extensions, Charlie and global/platform settings.
4. Run management backup/restore cases only on a disposable clone of the
   management plane. Do not perform a destructive restore on the user-testing
   installation.

### Phase 7 — execute provider and specialized lanes

Run K3S-HA for CSI snapshots and real multi-host Longhorn/CNPG failure. Run the
remaining cases that neither K3d nor generic native K3s can honestly certify on
their named fixtures: vendor receivers, cloud credentials/storage/DNS,
distribution-specific CIS, disconnected mirror and provider-specific identity.
Use the same candidate, case IDs, evidence schema and verifier.
A missing sandbox stays `BLOCKED` and prevents an “everything works” claim while
unrelated available cases continue.

### Phase 8 — cleanup, repetition and release claim

1. Revoke campaign tokens and credentials through their owner APIs.
2. Remove API-managed applications, Tools, settings, projects and clusters in
   reverse dependency order; verify absence through a new session.
3. Run `astronomer-full-test-iac/scripts/down.sh` after Astronomer decommission
   succeeds. Confirm all four instances, disks, networks, provider key records,
   Docker resources, browser profiles and run secrets are gone.
4. Repeat the complete supported local campaign once from clean K3d clusters to
   detect order/state dependence. High-risk DR/storage/provider cases repeat per
   their release policy.
5. Publish counts by PASS/FAIL/BLOCKED/NOT_RUN/NOT_SUPPORTED and artifact hashes.
   The statement “all offerings work” is permitted only when every required row
   and expanded variant is PASS and cleanup passed.

## Files in scope for implementation

- `scripts/qualify-offerings/**`
- `scripts/testdata/offering-qualification/**`
- `scripts/tests/offering_qualification_test.py`
- `deploy/release/offering-qualification.schema.json`
- `deploy/release/offering-estate-preflight.schema.json`
- `frontend/tests/offering-qualification/**`
- `.github/workflows/offering-api-qualification.yaml`
- `advisor-plans/028-execution-ledger.md`
- a new member-infrastructure fixture under `scripts/qualify-offerings/test-estate/`

Product defects, SQL, OpenAPI, handlers, charts and the sibling catalog are out
of scope for the campaign change. Fix them in separate reviewed increments,
regenerate owned artifacts, deploy a new immutable candidate and rerun.

## Done criteria

- [ ] The frozen runtime inventory contains every exposed application, Tool,
      provider, preset, transport, extension and Charlie capability with no
      unmatched entry.
- [ ] Two independent disposable member clusters pass API estate preflight.
- [ ] All 174 base rows and every runtime-expanded variant have a terminal result.
- [ ] Every required result is PASS; no FAIL, BLOCKED or NOT_RUN remains.
- [ ] NOT_SUPPORTED appears only where an explicit product contract says so and
      its truthful rejection was tested.
- [ ] Lifecycle, canary, authorization, recovery, durable readback and cleanup
      dimensions pass independently for each applicable entrypoint.
- [ ] Both K3d clusters are clean after each ownership-conflicting wave and gone
      after the campaign; management and unrelated resources remain healthy.
- [ ] Evidence contains immutable candidate/catalog/chart/image identities and
      no secrets.
- [ ] `go test ./scripts/qualify-offerings/... -count=1`, offline false-green
      tests, browser qualification and `make verify-enterprise VERIFY_SCOPE=all`
      pass on the final source.

## STOP conditions

Stop mutation and retain evidence if any of these occurs:

- estate preflight does not PASS, or either target resolves to the management cluster;
- candidate images, chart, schema or catalog drift after inventory freeze;
- schema-skew, TLS bypass, security disablement or unsigned/unpinned release override is active;
- host free RAM falls below 25% or free disk below 20%;
- a target, namespace, release, CRD or singleton setting is owned by another run;
- an API cannot expose operation completion, failure recovery or cleanup;
- the only path forward is direct Helm/kubectl repair, database mutation, fake
  provider response, session injection or generated-resource patching;
- the canary result belongs to the wrong cluster/namespace/generation or is stale;
- a cleanup request would affect an unowned resource;
- an extra runtime offering or variant has no manifest case;
- a K3d limitation is being interpreted as proof of a cloud, CSI or physical-host contract.

## Maintenance notes

Reconcile this plan and `scripts/testdata/offering-qualification/cases.json`
whenever catalog entries, Tools seeds, feature flags, connector/provider types,
extension mounts or Charlie capabilities change. Reviewers should focus on
false-green prevention, exact target allowlists, ownership locks, secret
redaction and cleanup. Historical pass evidence never replaces a fresh run for
a new candidate.
