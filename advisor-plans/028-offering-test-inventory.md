# Plan 028 — Offering inventory and required tests

Companion to [the API qualification plan](./028-all-offerings-api-qualification.md). **Every row is NOT_RUN for a fresh Plan 028 campaign.** Historical evidence is not carried forward as a current pass. All rows inherit the plan's API-only, lifecycle, failure/recovery, authorization, durable-readback and cleanup requirements. This document lists the current source inventory; Phase 0 must reconcile it with the deployed API, including all pages and additional configured catalogs.

The baseline has **174 named test rows**, with multi-variant rows expanded before execution. The [source snapshot](./028-offering-source-inventory.json) records catalog entries, seeded tool slugs and source hashes; runtime reconciliation and live qualification are explicitly NOT_RUN.

## A. Curated application catalog — 21 applications

Authority: sibling `astronomer-catalog/catalog.json`, clean HEAD `3e24ab98f0805bd54d6591d9b9b961bbfd1f5db5`, development channel, 17 repositories. Versions below are **local catalog entries, not recommendations or claims about latest upstream versions**. All 21 advertise install, upgrade and uninstall. The rollback column must be honored, not worked around. Generic catalog lifecycle uses `/api/v1/catalog/` discovery/applications, installed releases and operations; Flux deployment/rollout APIs supply the authoritative downstream observation. Compile exact method/request/poll contracts from OpenAPI and mounted routes before running.

| Case | Offered application | Catalog chart version | Support tier | Rollback offered | Required real functional proof |
|---|---|---|---|---|---|
| APP-01 | Constellation (`constellation`) | 0.2.0 | astronomer | yes | Authenticate to its offered API/UI; execute a real configured scan and inspect its report; qualify each enabled admission/runtime feature with safe accepted/rejected canaries. |
| APP-02 | Kube State Metrics (`kube-state-metrics`) | 8.4.0 | astronomer | yes | Create and change a canary workload; query its current object-state series through Astronomer monitoring. |
| APP-03 | Prometheus Node Exporter (`prometheus-node-exporter`) | 4.56.1 | astronomer | yes | Query fresh CPU/memory/network series for the correct cluster and node; distinguish stale data from healthy ingestion. |
| APP-04 | Metrics Server (`metrics-server`) | 3.14.0 | upstream | yes | Retrieve real pod/node resource metrics through the supported Kubernetes metrics API and its Astronomer consumer. |
| APP-05 | Kube Prometheus Stack (`kube-prometheus-stack`) | 88.5.4 | experimental | no — verify rejection | Query canary workload metrics; render Grafana datasource results; fire and resolve an alert and prove destination receipt. |
| APP-06 | Grafana (`grafana`) | 10.5.15 | upstream | yes | Load real browser assets, verify mapped Viewer/Editor/Admin identity and permissions, and execute a datasource query returning canary data. |
| APP-07 | Loki (`loki`) | 7.3.0 | experimental | no — verify rejection | Read a unique container-emitted log through the native offered query path; verify tenant/namespace isolation and configured retention. |
| APP-08 | Trivy Operator (`trivy-operator`) | 0.36.0 | upstream | yes | Scan a registry-reachable pinned canary image; correlate actual report, image digest, workload and Image Scans API; rescan/history/diff/export. |
| APP-09 | cert-manager (`cert-manager`) | v1.21.1 | upstream | yes | Create task-owned issuer/certificate through supported resource APIs; verify certificate trust/hostname/expiry by TLS and successful renewal/rotation. |
| APP-10 | Ingress NGINX (`ingress-nginx`) | 4.15.1 | upstream | yes | Route HTTP/TLS to a canary by host/path and verify routing updates; preserve unrelated routes and ingress classes. |
| APP-11 | External Secrets Operator (`external-secrets`) | 2.9.0 | upstream | yes | Create a reference to a task-owned upstream value; verify consuming workload result by checksum, rotate upstream and observe reconciliation. |
| APP-12 | Kyverno (`kyverno`) | 3.9.0 | upstream | yes | Apply policy; accept compliant workload and reject violating workload; query real PolicyReport and verify policy removal. |
| APP-13 | Longhorn (`longhorn`) | 1.12.1 | upstream | no — verify rejection | Bind PVC; write/checksum bytes from an API-created canary Job; reschedule and read the bytes; qualify advertised snapshot/backup recovery and Retain behavior. |
| APP-14 | OPA Gatekeeper (`gatekeeper`) | 3.23.0 | upstream | yes | Apply ConstraintTemplate/Constraint; reject a violating workload and accept a compliant one; inspect actual audit violations and remove owned policy. |
| APP-15 | Fluent Bit (`fluent-bit`) | 0.58.1 | upstream | yes | Emit unique container log and prove destination receipt, labels and namespace selection; change pipeline through PUT and observe live output change. |
| APP-16 | ExternalDNS (`external-dns`) | 1.21.1 | upstream | yes | Create/update a source in a task-owned DNS zone; confirm authoritative DNS answer and ownership-aware record cleanup. |
| APP-17 | Velero (`velero`) | 12.1.0 | upstream | yes | Back up resource manifests and persistent data; change original data; restore into isolated destination and compare original checksums plus storage artifacts. |
| APP-18 | OpenTelemetry Collector (`opentelemetry-collector`) | 0.171.0 | upstream | yes | Emit unique OTLP traces, metrics and logs from canary workloads through configured receivers/processors/exporters and read them at actual destination. |
| APP-19 | Grafana Tempo (`tempo`) | 1.24.4 | upstream | yes | Send unique trace/span chain and retrieve it through the offered query/Grafana API; verify service identity and span relationships. |
| APP-20 | KEDA (`keda`) | 2.20.2 | upstream | yes | Drive actual advertised trigger; observe replicas increase and return to idle without manually scaling the workload. |
| APP-21 | CloudNativePG (`cloudnative-pg`) | 0.29.0 | upstream | no — verify rejection | Create a database and API-created SQL canary Job; prove write/read, supported failover and backup/restore preserve data. |

## B. Tools — 12 separate entrypoint cases

Authority: `internal/db/migrations/001_initial.up.sql:4633–4641`, `042_curated_cluster_tools.up.sql`, `044_istio_release_plan.up.sql`; reconcile against **paginated** `GET /api/v1/tools/`. Mutation owner: `/api/v1/tools/{slug}/preview/`, `install/`, `upgrade/`, `rollback/`, `adopt/`, `uninstall/` as supported; operation read/retry under `/api/v1/tools/operations/`; actual inventory `/api/v1/clusters/{cluster_id}/tools/status/`. Do not infer support from a generic route alone. Capture actual lifecycle guards per tool.

| Case | Tool | Seeded presets to test separately | Required functional proof / special contract |
|---|---|---|---|
| TOOL-01 | CIS Scanner (`cis-operator`) | no seeded preset | Install using Tools; run actual scan, select installed profile, poll terminal report and inspect real checks/export. Profile variants below. |
| TOOL-02 | Dex (`dex`) | historical `in-cluster` metadata | Tool install must direct to management-chart/Auth workflow. Test bundled Dex through ID rows; reject member-cluster install without side effects. |
| TOOL-03 | Fluent Bit | default | APP-15 outcome through Tools lifecycle, plus each LOG destination. |
| TOOL-04 | Trivy Operator | default | APP-08 outcome through Tools lifecycle; no falsely successful unscannable image. |
| TOOL-05 | cert-manager | default | APP-09 outcome through Tools lifecycle. |
| TOOL-06 | Gatekeeper | default | APP-14 plus POLICY rows. |
| TOOL-07 | kube-state-metrics | default | APP-02 through Tools lifecycle. |
| TOOL-08 | Prometheus Node Exporter | default | APP-03 through Tools lifecycle. |
| TOOL-09 | Ingress NGINX | default | APP-10 through Tools lifecycle. |
| TOOL-10 | Longhorn | default | APP-13; preserve existing default StorageClass and Retain data policy. |
| TOOL-11 | NeuVector | default | Real scanner result and safe runtime/admission/network policy effect for enabled features; internal manager authentication and supported service access. Linux/runtime prerequisites explicit. |
| TOOL-12 | Istio | **default; development** | Ordered base→istiod install, opt-in injection, service traffic and mTLS; recovery between releases and reverse uninstall. No gateway is bundled. |

The application/Tool name union is **25**, not 33: eight Tools overlap applications. The entrypoint cases are deliberately distinct. **Velero is in the application catalog but is not among the 12 seeded Tools**; constants/comments mentioning a Velero tool are not proof it appears in the deployed Tools inventory. If runtime Tools includes it, add that path too. Likewise, the stale governance document saying Istio is unavailable must not override its current migration/API offer.

## C. Fresh adoption baseline — independent of catalog metadata

Authority: `deploy/bundles/catalog.json`, `deploy/bundles/selection.go`, `internal/delivery/builtin/`. Components are **Trivy Operator, kube-state-metrics and Prometheus Node Exporter**. Test registration API choices and resulting Flux-owned resources/metrics/reports, single-workflow namespace/CRD ordering, exact pinned artifacts, retries and restricted agent profiles.

| Case | Metrics baseline | Image scanning | Expected selection |
|---|---|---|---|
| BASE-01 | on | on/default | Both exporters and Trivy; real metrics and report. |
| BASE-02 | on | explicit opt-out | Both exporters; no automatic Trivy installation. |
| BASE-03 | off | on/default | Trivy only; real report. |
| BASE-04 | off | explicit opt-out | None of these optional baseline components. |
| BASE-05 | resumed registration | all four combinations | Retained selection, no duplicate sources/targets/releases; no two-phase manual CRD workaround. |
| BASE-06 | already adopted or read-only/namespace-only agent | applicable choices | No silent retrofit, permission expansion or cluster-wide installation by restricted agent; truthful UI/API applicability. |

Catalog `defaultEnabled` and registration defaults have different meanings. Do not rewrite one to make the other test pass.

## D. Identity — individual provider cases

Dex authority: `internal/dexconfig/validate.go:31`; API `/api/v1/auth/dex/connector-types/`, `connectors/`, `settings/`, `apply/`, `register-as-sso/`, `operations/{operation_id}/`. Direct SSO: `internal/handler/sso_presets.go`, `/api/v1/settings/sso/presets`, `/api/v1/settings/sso`, and `/api/v1/auth/login/{provider}` / callback. For every provider: create/update → apply where applicable → complete real login/callback → `/auth/me/` identity/groups → effective allowed/denied actions → logout/revocation → update/disable and retry. Require fresh credentials/assertions and exact applied generation, preserve unrelated connectors; do not treat redirect alone as success.

| Case | Offered identity path | Additional test |
|---|---|---|
| ID-01 | Dex generic OIDC | Discovery, issuer/audience/state/nonce, actual upstream session. |
| ID-02 | Dex Okta | Actual Okta tenant and group claim mapping. |
| ID-03 | Dex Microsoft / Entra ID | Tenant restriction and group mapping. |
| ID-04 | Dex GitHub | Organization/team restriction. |
| ID-05 | Dex GitLab | GitLab.com and configured self-hosted base URL separately. |
| ID-06 | Dex Bitbucket Cloud | Team restriction and user mapping. |
| ID-07 | Dex Google Workspace | Hosted-domain restriction. |
| ID-08 | Dex SAML | Signed assertions, mapping, invalid/expired/replayed assertion rejection. |
| ID-09 | Dex LDAP / Active Directory | Real directory search/bind, groups, TLS/StartTLS modes actually exposed; reject invalid bind. |
| ID-10 | Dex generic OAuth | Actual authorization/token/user-info endpoints, correct user ID mapping. |
| ID-11 | Direct GitHub SSO preset | Qualify direct OAuth path independently of Dex. |
| ID-12 | Direct Google Workspace preset | Direct login/domain mapping. |
| ID-13 | Direct Entra ID (`azure-ad`) preset | Direct tenant-specific flow. |
| ID-14 | Direct GitLab preset | Hosted/self-hosted direct flow. |
| ID-15 | Direct Okta preset | Direct issuer/client configuration. |
| ID-16 | Direct generic OIDC | Real standards-compliant IdP; cannot qualify the other named presets by substitution. |
| ID-17 | SCIM discovery and Users | `/scim/v2` (outside `/api/v1`): create/filter/page/update/deactivate; prove access changes; issue/revoke via `/api/v1/admin/scim-tokens/`. |
| ID-18 | SCIM Groups and group mappings | Membership add/remove maps to effective RBAC; cross-tenant denial, repeated requests and pagination. |
| ID-19 | Local identity, MFA and API tokens | Login/refresh/logout/password reset, TOTP enrollment/challenge/recovery, legitimate scoped token create/use/revoke; API-backed consumer works with permitted scope only. |

## E. Managed observability — five lifecycle families plus backend modes

Authority: `frontend/src/components/monitoring/stack-spec.ts`, `internal/server/routes_api_entry.go`, `routes_monitoring.go`, `internal/handler/monitoring_operations_readiness.go`. Shared lifecycle API prefix `/api/v1/settings/monitoring/`; cluster prefix `/api/v1/clusters/{id}/monitoring/`; poll `/api/v1/settings/monitoring/operations/{id}`. Each advertised install/preview/configure/replace/upgrade/uninstall/retry needs a case. `vector(1)` and health endpoints are diagnostics, not a real data oracle.

| Case | Offering | Functional test |
|---|---|---|
| OBS-01 | Cluster monitoring stack | Real workload metric via cluster metrics summary, instant/range and workload APIs; cluster Grafana role/query/browser; both member clusters distinct. |
| OBS-02 | Shared Thanos | Federated query returns both clusters correctly; object-store history survives supported restart/recovery; native tenant filtering and fresh sample age. |
| OBS-03 | Shared Alertmanager | Real rule fires/resolves; receiver gets notifications; silence/inhibition suppresses intended alerts and expires correctly. |
| OBS-04 | Shared Grafana | Real datasource query and dashboard rendering with mapped user roles; private proxy auth; permission revocation and fresh session. |
| OBS-05 | Shared Loki / hosted Loki | Workload log ingestion and native query with tenant isolation; attach/rotate token and old-token rejection; configured storage retention. |
| OBS-06 | Bring-your-own monitoring endpoints/backend configuration | Create/update/delete endpoint and prove native routing to selected backend; bad endpoint/auth/CA fail truthfully; no silent fallback to unrelated backend. |
| OBS-07 | Shared vs standalone member routing | Same canary read via the selected supported mode; correct scope after switching and deleting endpoint; zero/empty/stale/error distinguished. |
| OBS-08 | External dashboard links, query widgets and saved views | Real underlying API data/query permissions, correct cluster/project/global scope, refresh and error state. |

Grafana Live and Loki WebSocket live tail are explicitly unsupported by the current HTTP tunnel (see historical validation report). Verify honest availability; timed refresh and ordinary queries must work.

## F. Logging — eight destination cases plus pipeline/query behavior

Authority: `frontend/src/routes/dashboard/logging/-output-modal.tsx`, `internal/server/routes_tools_controlplane.go:214`, `internal/handler/logging_outputs.go:200`. API `/api/v1/logging/outputs/`, `/pipelines/`, `/operations/`, `/saved-searches/`; output test currently applies config. **Every destination requires an actual container-emitted canary**, selected namespace/labels, receiving-side observation, credential failure/rotation and disable/remove. Test each exposed authentication/transport mode rather than one assumed default.

| Case | Offered output | Independent receipt |
|---|---|---|
| LOG-01 | Elasticsearch | Query the intended index for the canary and metadata. |
| LOG-02 | OpenSearch | Query intended index with actual OpenSearch auth/TLS. |
| LOG-03 | Loki | Native Astronomer query plus backend read proves same canary and tenant. |
| LOG-04 | Splunk | Real HEC ingestion and search/receipt correlation. |
| LOG-05 | CloudWatch | Real AWS log group/stream event, scoped credential. |
| LOG-06 | Datadog | Real intake and search/query receipt for unique canary. |
| LOG-07 | S3 | Object API retrieves stored canary with expected prefix/format and checksum. |
| LOG-08 | Syslog | Actual selected protocol receiver captures/parses canary; independent evidence API. |
| LOG-09 | Pipeline filters and namespace selection | Each exposed filter, include/exclude and multiline setting; no data from excluded namespace; preserve unknown supported fields through update. |
| LOG-10 | Pipeline edit/reload/lifecycle | PUT unchanged output association, PUT changed association, enable/disable/remove; actual next log follows updated routing without manual ConfigMap patch. |
| LOG-11 | Native destination query/saved search | Public/private member service transport, auth, tenant/time filters, paging, saved query CRUD; unsupported query destinations explicitly reported. |

## G. Alerts, email, outbound webhooks and SIEM

Authorities: `frontend/src/routes/dashboard/alerting/-channel-modal.tsx`, `internal/worker/tasks/notification_dispatch.go`, `internal/handler/smtp.go`, `webhooks_test_delivery.go`, `internal/siem/transport.go`, `internal/siem/format.go`. API `/api/v1/alerting/`, `/api/v1/admin/smtp`, `/admin/webhooks`, `/admin/siem-forwarders/` with delivery/status/test-operation readback. Test actual triggering event as well as test button; verify filtering, duplicates, retry/backoff, failure/recovery, disable and cleanup for each row.

| Case | Offering | Required proof |
|---|---|---|
| SEND-01 | Slack notification channel | Unique fired/resolved message observed in real sandbox channel. |
| SEND-02 | Email notification channel | Actual alert email observed by recipient-side evidence service. |
| SEND-03 | PagerDuty channel | Real sandbox event creates/deduplicates/resolves the corresponding incident. |
| SEND-04 | Generic webhook notification channel | Receiver validates configured method/headers/payload for fired/resolved alert. |
| SEND-05 | Microsoft Teams channel | Unique message observed in configured supported Teams workflow/channel. |
| SEND-06 | SMTP / transactional email | Saved configuration test and actual reset/transactional flow reach recipient; all exposed TLS/auth modes; unsaved draft cannot be mistaken for saved config. |
| SEND-07 | Platform event webhooks | Real subscribed event, signature validation, delivery ID/state, retry and endpoint rotation; verify no unrelated event is delivered. |
| SIEM-01 | Syslog UDP | Real audit event received and parsed, not merely successful datagram send. |
| SIEM-02 | Syslog TCP | Receiver-side audit identity/time/format plus disconnect/recovery. |
| SIEM-03 | Syslog TLS | Trusted TLS and receiver receipt; invalid trust fails without bypass. |
| SIEM-04 | Splunk HEC | Real destination receipt/search for actual audit event. |
| SIEM-05 | NDJSON HTTPS | Receiver parses event with correct metadata and TLS; retry/backoff evidence. |
| SIEM-06 | RFC5424 format | Parse severity/identity/timestamp/escaping on each supported transport combination. |
| SIEM-07 | RFC3164 format | Parse intended field mapping and supported timestamp behavior. |
| SIEM-08 | CEF format | Correct header/extension escaping and source identity. |
| SIEM-09 | NDJSON format and automatic format selection | Valid event JSON, source metadata, correct automatic selection; enumerate all combinations offered by API. |

## H. Secrets, credentials, registries and source integrations

Authorities: `internal/vault/client.go`, `internal/cloudcreds/registry.go`, `internal/handler/cloud_credentials_crud.go`, `cluster_registries.go`, `catalog/source.go`, `internal/server/routes_delivery.go`. API `/api/v1/admin/vault-connections`, project default Vault and cloud credentials, `/cloud-credentials/providers`, cluster `registries`, catalog repositories and delivery sources. CRUD/test alone is insufficient: prove the real consuming operation, rotation, invalid credentials, scope and deletion.

| Case | Offering | Functional test |
|---|---|---|
| SECRET-01 | Vault token auth | Resolve task-owned reference in real install/consumer; revoke/rotate token, verify failure/recovery without secret leakage. |
| SECRET-02 | Vault AppRole auth | Actual login and secret use; rotate SecretID and re-run consumer. |
| SECRET-03 | Vault Kubernetes auth | Legitimate service-account login and consumer; role/audience/permission denial. |
| SECRET-04 | Vault KV v1 | Correct path/version semantics, denied/missing path, project default vs explicit connection. |
| SECRET-05 | Vault KV v2 | Versioned read/rotation and missing/deleted version behavior; every supported auth combination. |
| CLOUD-01 | AWS credentials | Real identity/probe and task-owned consumer, session/assume-role variants exposed by provider registry. |
| CLOUD-02 | GCP credentials | Service-account probe and intended project consumer, rotation/revocation. |
| CLOUD-03 | Azure credentials | Tenant/subscription probe and scoped consumer, rotation/revocation. |
| CLOUD-04 | DigitalOcean credentials | Real supported probe and consumer; unavailable tester must not return qualified. |
| CLOUD-05 | Generic credentials | Materialization and actual consumer only; connection test intentionally reports unavailable/ok=false. |
| SOURCE-01 | Docker Registry v2-compatible registry configuration | Real authenticated image pull on intended namespace; wrong credentials then API rotation recovery; registry mirror/CA modes exposed by form. |
| SOURCE-02 | Helm HTTP(S) repositories | Synchronize actual index/chart; verify pinned artifact, install and lifecycle; auth/CA/proxy/digest and failure preserve last-good inventory. |
| SOURCE-03 | OCI sources/registries | Resolve immutable digest/signature and install; private auth and invalid/revoked artifact rejection; no mutable substitute. |
| SOURCE-04 | Git delivery sources | Supported transports/auth variants, immutable commit, reconcile actual workload; credential rotation and signed webhook-driven update where offered. |
| SOURCE-05 | Admin GitOps configuration sources | Preview/sync actual scoped configuration; webhook signature, invalid update, last-good state, scoped policy and retry. |
| SOURCE-06 | Custom/application catalogs | Every published application/version/profile becomes its own APP case; source trust, schema/compatibility, revocation, visibility and dependency ownership. |
| SOURCE-07 | Disconnected mirrors/bundles | Exact signed mapping/digests, actual no-egress install and upgrade in declared air-gap profile; separate Plan 016 evidence. |

## I. Backup and restore surfaces — distinct owners

Authorities: `internal/server/routes_tools_controlplane.go`, `internal/handler/cluster_snapshots_restore.go`, `backups_velero.go`, `docs/openapi.yaml`; [restore identity checkpoint](./027-ux-audit/restore-tracking-contract.md). Do not equate general Velero backup/restore IDs, member snapshot restore IDs, management-plane backup operations and control-plane snapshots.

| Case | Offering | Required proof |
|---|---|---|
| DR-01 | Velero application installation | APP-17 through offered app lifecycle, plugin configuration and availability; add Tools path only if runtime advertises it. |
| DR-02 | Cluster snapshots and restore | `/clusters/{cluster_id}/snapshots`, restore receipt/progress for same identity; restore resource and PVC checksums into isolated destination, namespace mapping/filtering. Missing public status contract = BLOCKED. |
| DR-03 | Cluster snapshot schedules | `/snapshot-schedules`: actual scheduled and trigger-now backup where offered, pause/update/resume/retention, clock/timezone and deletion. |
| DR-04 | General Velero backup storage/schedules/restores | `/api/v1/backups/storage/`, `/backups/schedules/`, `/backups/restores/`: cluster-resource backup contents and isolated restore, durable tracking and retention. This BackupHandler domain is distinct from cluster snapshots and management-plane pg_dump. |
| DR-05 | S3 / AWS backup storage | Real bucket artifacts plus successful restore; IAM permission failure/rotation and prefix isolation. |
| DR-06 | MinIO / compatible S3 backup storage | Actual compatible API and restore; not evidence for cloud-specific IAM/CSI behavior. |
| DR-07 | GCS backup storage | Provider plugin/auth, object evidence and successful restore. |
| DR-08 | Azure Blob backup storage | Provider plugin/auth, object evidence and successful restore. |
| DR-09 | CSI volume snapshot mode | Real supported CSI snapshot/restore with byte comparison; plugin/driver prerequisites and unsupported-driver errors. |
| DR-10 | Node-agent/filesystem volume backup mode | If offered by actual values/schema: real file backup/restore and checksum; no claim from metadata-only backup. |
| DR-11 | Control-plane snapshot creation, inventory and restore guidance | `POST /api/v1/clusters/{cluster_id}/control-plane-snapshots/` on a supported disposable profile: actual snapshot creation and GET inventory/detail readback; verify feature-off and insufficient nodes:manage/pods:exec denial. GET restore guidance remains read-only, not automated restore. Physical disaster recovery remains under Plan 016. |
| DR-12 | Management restore-drill reporting | `GET /api/v1/admin/backup-drill/` and `history/`: correlate fresh real scheduled CronJob restore evidence with latest/history/age. These are read APIs, not a drill trigger. Await normal scheduled execution in the declared fixture; if unavailable, BLOCKED rather than invent POST or insert result rows. |
| DR-13 | Management-plane backup lifecycle | `/api/v1/admin/management-backup/`: destination create/read/update/delete, schedule/wrapping configuration, test/run receipts and `/operations/{id}/`; exact observed generation, real pg_dump artifact and independently restored data. Pair with DR-12 or Plan 016 supported recovery procedure; no invented restore API or restore over shared management. |

Expand every UI/API-exposed backup plugin, storage provider, include/exclude mode and lifecycle capability from the runtime schema. An alias for a provider is not evidence of another independently working backend.

## J. Policy, security and service mesh

Authorities: `internal/handler/security_profiles.go`, `internal/gatekeeperpolicy/bundle/`, `internal/server/routes_security.go`, `routes_gatekeeper.go`, `routes_cluster_addons.go`, `internal/handler/service_mesh.go`. Test accepted and rejected workloads/traffic through supported resource/policy APIs; read actual reports. Unsupported distribution/hardware cases remain BLOCKED.

| Case | Offering / variants | Functional test |
|---|---|---|
| POLICY-01 | PSA privileged, baseline, restricted | Each enforce/audit/warn mode and version setting; effective labels and actual admit/reject/warning/audit outcome; unassign restores intended state. |
| POLICY-02 | Gatekeeper privileged-container policy | Deny violating workload and accept safe one; audit violation and removal. |
| POLICY-03 | Gatekeeper latest-image-tag policy | Reject forbidden tag and accept pinned image; actual admission/audit. |
| POLICY-04 | Gatekeeper required-label policy | Missing/present label outcomes; update required keys and retest. |
| POLICY-05 | Custom Gatekeeper templates/constraints | Validate/apply actual custom policy; invalid schema/ownership error; remove owned policy. |
| POLICY-06 | Network-policy templates | Every built-in and offered custom template: connectivity before/after, selected namespace/pod allow/deny, update/reapply/remove. No pass on CNI that ignores policy. |
| POLICY-07 | API-server allowlist | Preview/apply/reconcile/rollback behavior only on disposable API-reachable fixture; allowed and denied connection evidence; prevent self-lockout. |
| POLICY-08 | Compliance baselines/posture/export | Enumerate every shipped baseline and version, apply/diff/revert and prove effective settings; export accurate real evidence. |
| CIS-01 | Generic Kubernetes CIS | Installed profile, actual check results, report/export, cancel/failure/retry. |
| CIS-02 | RKE / RKE1 CIS | Actual matching distribution/profile; do not reuse a K3s result. |
| CIS-03 | RKE2 CIS | Actual matching distribution/profile. |
| CIS-04 | K3s CIS | Actual matching distribution/profile. |
| CIS-05 | EKS CIS | Real supported target/profile and provider-specific checks. |
| CIS-06 | AKS CIS | Real supported target/profile and provider-specific checks. |
| CIS-07 | GKE CIS | Real supported target/profile and provider-specific checks. |
| MESH-01 | Istio detection/posture/inventory | Detect real version/namespace; accurate mTLS inventory; detect absence after supported removal. |
| MESH-02 | Linkerd detection/posture | Real Server-level aggregate; namespace breakdown explicitly unavailable. |
| MESH-03 | Kuma detection | Actual detection contract and disappearance; no invented Istio management parity. |
| MESH-04 | Cilium detection | Actual detection contract and disappearance; distinguish CNI from mesh capabilities. |
| MESH-05 | Istio managed resource types | Gateway, VirtualService, DestinationRule, AuthorizationPolicy, PeerAuthentication, Sidecar, ServiceEntry: each inventory/validate and offered mutation path, traffic/mTLS outcome; external ownership preserved. |
| SEC-01 | Image Scans API / fleet vulnerabilities | Real Trivy report linkage, summary/top images/history/diff/export/rescan/progress, namespace/project scoping and unsupported image errors. |
| SEC-02 | API-server audit ingestion | Actual task-owned Kubernetes operation appears once with correct actor/cluster/time via agent ingestion; retry/dedup and read scopes. |

## K. UI extension platform and assisted operations

Authorities: `internal/handler/extensions_manifest.go`, `internal/server/routes_cluster_addons.go`, `routes_charlie.go`, `docs/openapi.yaml`. Extension APIs `/api/v1/extensions/`, validate/verify-bundle/mounts/enable/disable/data/token. These are opt-in product features, distinct from installing Kubernetes add-ons. API qualification is necessary but cannot prove browser rendering; real browser follow-through is mandatory. Enumerate Charlie's actual qualification discovery/capability registry instead of assuming each command works because chat replies.

| Case | Offering | Functional test |
|---|---|---|
| EXT-01 | Sidebar extension mount | API-installed enabled extension navigates and fetches actual scoped data; disabled feature/extension disappears and denies access. |
| EXT-02 | Dashboard widget mount | Actual data in correct global/project/cluster context and refreshed results. |
| EXT-03 | Cluster tab mount | Correct cluster identity, switch/deep link/reload and denied other-cluster data. |
| EXT-04 | Settings page mount | API-backed form/reads and expected permissions, reload and disabled state. |
| EXT-05 | Declarative table | Rows from declared API fields; loading/error/empty and row cap. |
| EXT-06 | Declarative chart | Each line/bar/area variant with real time-series and proper scope. |
| EXT-07 | Declarative stat | Correct value/units and no false zero on failure. |
| EXT-08 | Declarative form | Each text/number/select/toggle field; supported submit causes a real server change; validation and permission denial. |
| EXT-09 | Signed bundle/iframe | Verify real bundle, render host bridge, origin/CSP and short-lived ticket behavior; wrong/replayed/expired ticket fails; no session credential exposure. |
| EXT-10 | Astronomer API data proxy | Actual allowlisted source, projection and RBAC on every call. |
| EXT-11 | Kubernetes data proxy | Correct cluster/namespace context and denied escape. |
| EXT-12 | Prometheus data proxy | Real scoped samples; disallowed query/data source rejected. |
| EXT-13 | Extension lifecycle | Install/enable/disable/reinstall and version compatibility/signature changes; follow actual deletion contract if present, otherwise record removal API gap. |
| AI-01 | Charlie connection/onboarding/modes | Feature off/on, genuine connection/activation/status/disconnect; failing backend does not appear ready. |
| AI-02 | Charlie sessions/threads/context | Actual assistant response, history/events/abort, correct tenant context and unavailable-backend errors. |
| AI-03 | Charlie commands/capabilities/approvals | Enumerate discovery and every offered capability; approved harmless action has real API effect/audit, denied action no effect; approval identity and operation readback. |
| AI-04 | Charlie triggers/findings/remediation | Actual trigger delivered, finding lifecycle and approved remediation/verification effect, retry/dedup and policy restrictions. |

## L. Cross-cutting offered workflows — coverage closure

These platform surfaces enable the integrations above and must not become an omission just because they are not charts. Owner APIs are defined in `internal/server/routes*.go` and `docs/openapi.yaml`; presentation work belongs to Plan 027. Expand each exposed subtype/provider/widget/baseline/command into a named runtime manifest case before the comprehensive gate.

| Case | Offering | Required API-backed test |
|---|---|---|
| PLATFORM-01 | Cluster adoption, agent connectivity/profiles and lifecycle | Real adoption handshake/inventory, profile enforcement, token rotate/revoke, disconnect/reconnect/upgrade as offered, accurate status; no out-of-band bootstrap workaround. |
| PLATFORM-02 | Projects, namespaces, membership, RBAC and quotas | Create/update/scoped enumeration and enforcement; namespace assignments, role/group changes and usage limits affect real canary operations. |
| PLATFORM-03 | Cluster templates and groups | Create/apply/reapply/detach and membership changes produce intended owned configuration; preserve independent objects and external ownership. |
| PLATFORM-04 | Delivery sources/bundles/versions/targets/rollouts | Immutable source → preview → controlled rollout → real workload; dependency, override, cohort, approval, pause/resume/abort/retry/rollback and drift modes. |
| PLATFORM-05 | Apps catalog discovery/repositories/installed releases | Every exposed application mapped; pagination, scope, installation receipt, lifecycle and uninstall ownership; no hidden failed generation. |
| PLATFORM-06 | Workloads, resources, CRDs and discovery | Actual API CRUD/apply/dry-run, YAML/import, clone, scale/restart, related resources, custom-resource discovery and scoped pagination; live object result. |
| PLATFORM-07 | Logs, events, exec/shell and kubeconfig | Real permitted workload streams/session/download access, commands' actual effect, expiry/revocation/audit, denied wrong tenant; no shell used to repair integration tests. |
| PLATFORM-08 | Alert rules/events/silences/inhibition/anomaly views | Real source condition produces scoped event, acknowledge/resolve behavior and appropriate delivery suppression; summaries agree with lists. |
| PLATFORM-09 | Audit, read-audit policies, retention and compliance export | Correlate actual mutation and sampled/full read evidence; configured retention/expiry semantics and export jobs/receipts with proper permissions. |
| PLATFORM-10 | General/platform settings, feature flags, branding/preferences | API save/read and observable supported effect; enabled flag adds required inventory cases, disabled state denied honestly; cache propagation bounded. |
| PLATFORM-11 | Dashboard widgets/templates and support bundles | Enumerate every shipped widget/template; real scoped datasource rendering or delivery; support export completes and excludes credentials. |
| PLATFORM-12 | System inventory, compatibility and operation controllers | Correct versions/ownership/observation age; ongoing/failed operations survive refresh and are actionable; no green status merely from controller liveness. |

## Coverage gate and current limits

No live provider, new installation, upgrade, restore or destination send was executed for this plan. This is a **test specification**, not a passing results table. The known 21 applications/12 Tools/25-name union is source-confirmed; deployed catalog differences and additional configured providers remain an explicit Phase 0 reconciliation requirement.

Every table row and every semicolon/comma-separated advertised variant above must become an individual manifest case where behavior differs. Catalog versions, provider presets, storage drivers, trigger/scaler types, policies, notification templates, widgets and Charlie capabilities may grow: the runner must enumerate and reject unmatched entries. Do not replace this with representative sampling. Add a concrete functional contract for each extra offering before declaring it covered.

Report lifecycle result, functional result, authorization result, recovery result and cleanup result separately, then derive overall PASS only when all applicable assertions pass. Publish full counts and blockers. Any FAIL/BLOCKED/NOT_RUN or omitted required case prevents the statement that everything offered works.
