# Astronomer vs Rancher: Kubernetes Management Plane Comparison & Work Plan

_Last updated: 2026-06-22_

This document compares the **Astronomer** Kubernetes management plane to **Rancher** across 18 verified capability dimensions, and sets a prioritized work plan to close the gaps. Astronomer is a **day-2 operations** platform: it adopts and operates clusters that already exist (provisioned externally via Terraform, cloud consoles, RKE2/k3s, EKS/AKS/GKE, bare metal, or CAPI). It deliberately **excludes cluster provisioning, machine drivers, and node-pool lifecycle**. Where Rancher's management plane *is* a Kubernetes control plane built on continuous controllers and uses **Fleet** for GitOps, Astronomer uses **ArgoCD** for baseline delivery, **Prometheus + Thanos** for monitoring, and a **hybrid Postgres + asynq task-queue** reconciliation model with Kubernetes CRDs as a declarative intent surface. Each section cites code, handlers, and migrations as evidence. Where the verification verdict corrected an original claim, the verdict is authoritative here.

---

## Status legend

| Status | Meaning |
|---|---|
| **Ahead** | Astronomer provides materially more capability than Rancher in this area. |
| **At Parity** | Comparable capability; differences are stylistic, not functional. |
| **Behind But Aligned** | Astronomer trails Rancher but the architecture is sound and the gap is closeable without redesign. |
| **Intentionally Excluded** | Out of scope by product strategy; not a defect. |
| **Needs Decision** | Requires a product call before work is scoped. |

---

## Executive summary

**Where Astronomer leads.** Astronomer is **Ahead** in the day-2 governance surface that Rancher treats as an afterthought: per-cluster **monitoring stack lifecycle** (Prometheus+Thanos with reconciler, readiness gates, auto-rollback, drift detection, 6 built-in dashboards), **logging/SIEM** (Fluent Bit multi-output + 5-transport SIEM forwarder + management-plane log tailing), **security scanning & policy** (trivy/CIS ingest, PSA/NetworkPolicy/Gatekeeper templates, apiserver CIDR allowlist), **compliance baselines** (PCI/HIPAA/FedRAMP/SOC2 with atomic apply/revert and posture scoring), **backup/DR** (NIST-compliant pg_dump + weekly restore drills, per-cluster Velero), **service mesh observability** (detection + mTLS coverage + ArgoCD-ownership-aware policy validation across Istio/Linkerd/Kuma/Cilium), **audit logging** (partitioned Postgres, durable task-outbox, read-side policies), and **API/SDK/CLI maturity** (270+ endpoint OpenAPI 3.1 spec, generated Go *and* TypeScript SDKs, 11.6k-LOC `astro` CLI, rate-limit + idempotency middleware, contract CI gate). Rancher exposes none of these as managed, reconciled day-2 features — it ships CRDs/Helm slots and leaves lifecycle to the operator.

**Where Astronomer trails.** Astronomer is **Behind But Aligned** in areas where Rancher's maturity and breadth show: the **cluster explorer** (no live watch streams, blind-PUT YAML edits without server-side apply, curated 33-type resource allowlist vs. Rancher's generic proxy), **GitOps engine** (ArgoCD primitives exist but lack Rancher Fleet's label-based auto-targeting and high-level abstractions), **alerting** (no Alertmanager inhibition rules, no recovery/resolve notifications, in-band silences), **RBAC** (namespace-scoped bindings and role inheritance are spec'd but unimplemented), **auth providers** (Dex brokers everything; no native SCIM, GitHub App, or Azure AD provider), and **catalog/tools** (no readiness probes, no drift sweep on tool installs).

**The one structural theme.** Rancher's edge is **controllers everywhere** — every desired state converges via continuous, etcd-watching, leader-elected reconcile loops. Astronomer chose a **task-driven hybrid**: durable intent in Postgres + CRDs, transient convergence via stateless asynq sweeps (5m/15m/30m/60s cadences) with audit trails and exponential backoff. This is intentional and gives Astronomer auditability and history Rancher lacks — but it means drift is detected on a sweep interval, not instantly. **Monitoring is the proof the pattern works** (full reconciler, readiness gates, auto-rollback, drift tracking); the remaining work is largely *applying that same maturity* — readiness checks, drift reconciliation, per-row backoff — to the other task-driven subsystems (tools, network policy, project reconcile).

---

## Headline scoreboard

| Area | Rancher | Astronomer | Status |
|---|---|---|---|
| Cluster provisioning & node lifecycle | Full provisioning.cattle.io + CAPI/RKE machine pools | Excluded by design (import-only) | **Intentionally Excluded** |
| Cluster import / registration / agent / health | Import via tokens + Fleet/system-agent | Phase state machine + tunnel + condition reconcile | **At Parity** |
| GitOps engine | Fleet (Bundles/ClusterGroups) | Bundled ArgoCD + ApplicationSets | **Behind But Aligned** |
| Cluster explorer / resource browse / workload actions | Thin Steve proxy, native watch/exec | Curated proxy + async ops, no watch streams | **Behind But Aligned** |
| Monitoring stack (Prometheus+Thanos) | Chart-as-app, no lifecycle | Full per-cluster lifecycle + reconciler | **Ahead** |
| Alerting | Helm Alertmanager, no control-plane API | API-driven rules/channels/silences + anomaly | **Behind But Aligned** |
| Logging pipeline / SIEM | Audit-log-only, stdout writer | Fluent Bit multi-output + SIEM + log tail | **Ahead** |
| Security scanning & policy | PSA + catalog cis/gatekeeper | trivy/CIS ingest, PSA/NetPol/Gatekeeper/allowlist | **Ahead** |
| Compliance baselines & posture | Audit policy only | 4 baselines + posture rollup + read-audit | **Ahead** |
| RBAC & tenancy | ~41 role templates, full inheritance | 35 templates, no namespace scope/inheritance yet | **Behind But Aligned** |
| Auth providers & SSO | 17 native providers + SCIM | Local/TOTP/JWT + Dex-brokered SSO, no SCIM | **Behind But Aligned** |
| Audit logging & traceability | In-process middleware, io.Writer | Partitioned Postgres + task-outbox + SIEM | **Ahead** |
| Backup / restore / DR | backup-restore-operator, etcd snapshots | pg_dump + weekly restore drill + Velero | **Ahead** |
| Catalog / apps / tools lifecycle | ClusterRepo + ManagedChart + Fleet rollout | Helm/OCI catalogs + fleet_operations + rollback | **Behind But Aligned** |
| Service mesh | Istio VS/DR edit UI (feature flag) | Detect 4 meshes + mTLS + policy validation | **Ahead** |
| Platform settings & UI extension SDK | 163–172 settings + plugin catalog | 23 settings + manifest-based extensions | **Behind But Aligned** |
| Controller/reconciler maturity | Pure controller-runtime + Wrangler | Hybrid task-queue + CRD intent layer | **Behind But Aligned** |
| API / SDK / CLI maturity | K8s clientset only, no CLI | OpenAPI 3.1 + Go/TS SDK + astro CLI | **Ahead** |

---

## Per-dimension detail

### 1. Cluster provisioning & node lifecycle — Intentionally Excluded
- **Rancher:** Full infrastructure lifecycle via `provisioning.cattle.io/v1` Cluster with `RKEConfig.MachinePools`, machine drivers (EKS/GKE/AKS), autoscaling, and CAPI MachineDeployment orchestration (`pkg/apis/provisioning.cattle.io/v1/cluster_types.go`, `pkg/controllers/provisioningv2/`).
- **Astronomer:** Zero provisioning fields in the Cluster CRD. Adopts pre-existing clusters only; cloud credentials (migration 053) are for workload-scoped materialization (image pulls), not provisioning. Registration wizard (migration 078) is adoption-only. See `README.md` lines 21-33, 169.
- **Gap / note:** Correctly excluded. The only defect is marketing copy: **`MARKETING.md` line 57 falsely claims Astronomer "provisions new ones"** — this must be removed; the README is correct.

### 2. Cluster import & management — At Parity
- **Rancher:** Token-based import, Fleet/system-agent deployment, multi-phase conditions, remotedialer tunnels.
- **Astronomer:** Server-authoritative phase machine `created → awaiting_agent → connected → provisioning → ready/failed` (`internal/registration/phase.go`), 32-byte bearer tokens with hash storage, agent RBAC profiles (admin/operator/viewer/namespace variants, `deploy/agent/template.go`), heartbeat-driven Connected condition on 2-min window (`health_check.go`), remediation reconciler with exponential backoff 60s→64m and 12 attempts/24h cap (`cluster_condition_reconcile.go`), tunnel via `tunnel2/server.go`. Migrations 078/035/086.
- **Gaps:** Cloud-provider discovery/bulk import (out of scope); per-tool install-step granularity in timeline; agent auto-upgrade/version-skew detection.

### 3. GitOps engine: Fleet vs ArgoCD — Behind But Aligned
- **Rancher:** Fleet Bundles to label-selected ClusterGroups; automatic targeting.
- **Astronomer:** Bundled ArgoCD (`astro-argocd`), ApplicationSets with cluster generators (`baseline_appsets.go`), GitOps registration via committed YAML (`gitops_sync.go`, migration 060), auto-register worker iterating clusters into ArgoCD instances, sync waves/phases, multi-instance-per-cluster, ownership metadata (migration 091). **Correction:** resource drift counts *are* exposed publicly via `GET /api/v1/clusters/{id}/` (`ClusterArgoCDDriftSummary`, `summarizeArgoCDDrift`).
- **Gaps:** No high-level ApplicationSet abstraction (operators write raw ClusterGenerator selectors); no label-based lazy auto-targeting like Fleet ClusterGroups; sync-wave order not exposed in ownership API.

### 4. Cluster explorer / resource browse / workload actions — Behind But Aligned
- **Rancher:** Thin Steve proxy with native K8s watch, exec, logs, generic CRUD on all types, service proxy, SAR-backed authz.
- **Astronomer:** Generic browse over **33** curated resource types (`resources.go`), dedicated workload handlers with async `workload_operations` (202 Accepted, DB queue, 20s reconciler, migration 014), pod logs (RFC3339Nano), node cordon/drain/taint, events, in-browser kubectl shell with per-command audit (migration 065), service proxy, Prometheus query endpoints. **Corrections:** pod-container exec **already exists** at `/api/v1/ws/exec/{cluster_id}/{namespace}/{pod}/{container}/` (not a gap); cluster metrics query endpoints are **not** feature-gated (only monitoring-backend CRUD is).
- **Gaps:** No live/watch streams (lists are poll-based); blind-PUT YAML edits with no server-side apply / conflict detection; resource CRUD limited to the curated allowlist (no dynamic CRD discovery).

### 5. Monitoring stack — Ahead
- **Rancher:** Constants + chart-as-app; no per-cluster lifecycle, reconciler, or rollback (`pkg/monitoring/monitoring.go`).
- **Astronomer:** Per-cluster Prometheus+Thanos provisioning (`cluster_monitoring_configs`, migration 002), shared Thanos backend, 30s reconciler, full operation lifecycle with status (migration 006/007), readiness gates (`waitForReleaseReadiness`, 2-min/2-replica) + auto-rollback (`rollbackIfConfigured`, `defaultAutoRollbackOnFailure`), drift detection via spec-hash, health reconciliation, **6 built-in dashboards**, metrics API + real-time UI. Verified with zero corrections.
- **Gaps:** No long-term metrics export/archival; no helm-diff drift visualization; no canary/shadow stack mode.

### 6. Alerting — Behind But Aligned
- **Rancher:** Helm-deployed Alertmanager per cluster; no control-plane alerting API.
- **Astronomer:** Centralized API for rules (PromQL + anomaly), channels (Slack/PagerDuty/MSTeams/Webhook/Email), silences, events; 5m evaluator; routing synced to cluster ConfigMaps; anomaly detection on rolling 24h baselines (migration 072). Handlers in `alerting.go`, workers in `alert_evaluation*.go`.
- **Gaps:** No Alertmanager **inhibition rules**; hard-coded group_wait/group_interval/repeat_interval (30s/5m/3h); in-band (control-plane) silences instead of server-side; **no recovery/resolve notifications**; cross-cluster anomaly baselines deferred; no per-project routing isolation.

### 7. Logging pipeline / SIEM — Ahead
- **Rancher:** Audit-log-only via AuditPolicy CRDs; serializes JSON to a single `io.Writer` (stdout/file) with no external sink (`pkg/auth/audit/writer.go`). No data-plane logging, Fluent Bit, SIEM, or multi-output.
- **Astronomer:** Management-plane Fluent Bit DaemonSet (migration 106), per-cluster logging outputs/pipelines with reconciler (migration 037), **SIEM forwarder with 5 transports** (syslog udp/tcp/tls, splunk_hec, ndjson_https; migration 055, `siem_dispatch.go`), superuser-gated management log tailing, **full dashboard UI** (`frontend/.../logging/page.tsx`). **Correction:** dashboard UI was wrongly flagged missing — it exists and is complete.
- **Gaps:** `QueryOutput` returns 501 (no Loki/ES read client yet); SIEM retention is per-row 7-day sweep, not per-forwarder; operator runbooks for backend setup needed.

### 8. Security scanning & policy — Ahead
- **Rancher:** PSA templates; cis-operator/gatekeeper via catalog; ProjectNetworkPolicy. No image scanning, no apiserver allowlist, no template library.
- **Astronomer:** trivy-operator vuln ingest (migrations 062/081, CVSS + Prometheus metrics), CIS scan ingest with CSV export (migration 022), PSA templates (migration 107), 4 NetworkPolicy templates with SSA reconciler (migration 068), Gatekeeper starter bundle, per-cluster apiserver CIDR allowlist with drift detection + snapshots (migration 070). **Correction:** Gatekeeper bundle is **6 resources** (3 ConstraintTemplates + 3 Constraints), not 8.
- **Gaps:** No real-time CVE/violation webhooks (passive persistence only); no custom Gatekeeper constraint authoring (hardcoded bundle); no auto-remediation; no SBOM/supply-chain attestation or image-signing enforcement; no apiserver/kubelet audit-log centralization.

### 9. Compliance baselines & posture — Ahead
- **Rancher:** Mutation audit logging only; no baselines, posture scoring, or read-audit.
- **Astronomer:** 4 baselines (PCI-DSS 4.0, HIPAA, FedRAMP Moderate, SOC2) with atomic apply/revert (migration 064), weighted posture rollup (CIS 30% / vulns 30% / netpol 25% / audit 15%), read-audit policies + middleware (migration 063), audit export bundles. `internal/compliance/baselines.go`, `docs/compliance.md`.
- **Gaps:** **Correction** — read-audit policies are a complete standalone feature but are **not yet wired into baseline apply** (`apply.go` skips them, documented as a no-op). Webhook/SMTP deletion enforcement deferred to v2.

### 10. RBAC & tenancy — Behind But Aligned
- **Rancher:** ~41 role templates across global/cluster/project with binding scopes, group mappings, and full inheritance/aggregation.
- **Astronomer:** 35 embedded YAML templates (**16 global / 9 cluster / 10 project** — original claim miscounted), scope enforcement that fails closed (`engine.go`), group mappings via `identity_group_mappings`, effective-permission/binding-preview APIs (`rbac_effective.go`), permission-aware UI. Migrations 032/098.
- **Gaps / corrections (status downgraded from At Parity):** Namespace-scoped bindings are spec'd in `types.go` and enforced in-memory but have **no DB column or CRUD** — storage/API/UI all pending. Role **inheritance** field exists but the composition engine is **not implemented** (zero templates use it). Migration 098 seeds **24** roles (12/5/7), not 35. Read-token write-path scope enforcement is opt-in per route.

### 11. Auth providers & SSO — Behind But Aligned
- **Rancher:** 17 native providers (Local, AD, LDAP×2, SAML×5, GitHub, GitHub App, Azure, Google, OIDC variants, Cognito) + **SCIM 2.0** provisioning.
- **Astronomer:** Local + TOTP/2FA (migration 043), JWT sessions with jti revocation + lockout (migration 039), IP-scoped API tokens (migration 044), **Dex-brokered** SSO across 10 connector types (OIDC/SAML/LDAP/OAuth), group sync (migration 042), SLO/RP-initiated logout (migration 054).
- **Gaps:** No **SCIM 2.0** auto-provisioning (manual group mappings only); no native AD / Azure AD / GitHub App / Cognito / Ping / ADFS / Shibboleth (all Dex-bridged); no configurable password policy; no MFA enforcement toggle; no session inactivity timeout.

### 12. Audit logging & traceability — Ahead
- **Rancher:** In-process middleware to a pluggable `io.Writer`; no durable queue, partitioning, or SIEM forwarding.
- **Astronomer:** Monthly-partitioned Postgres audit (migration 025), async batched writes (50 events / 250ms), durable **task-outbox** with claim/lock + dead-letter (migration 092), SIEM real-time forwarding, read-side audit policies with configurable sample rate (migration 063). **Corrections:** source filtering, task-outbox `status=dead` filtering, and per-policy sampling **are all implemented** (originally listed as gaps).
- **Gaps (confirmed):** No webhook-dispatch DLQ endpoint (unlike task-outbox); no real-time tail/streaming API; no compliance report builder; no control-plane↔agent audit correlation.

### 13. Backup / restore / DR — Ahead
- **Rancher:** backup-restore-operator + etcd snapshots; focus is cluster bootstrap recovery, not management-plane recoverability validation.
- **Astronomer:** Management-plane nightly pg_dump to S3 with daily/weekly/monthly tiers, **weekly restore drill** validating schema/rows in ephemeral Postgres (NIST CP-9 / ISO 27001 A.12.3.1), audit trail in `backup_drill_results` (migration 041), per-cluster Velero snapshots/schedules/cross-cluster restore (migrations 020/052) with status reconcilers (`backups_reconciler.go`, 15s cadence).
- **Gaps:** Management backup disabled by default in `values.yaml` (enabled in prod values); restore-drill pattern not extended to per-cluster snapshots; no Velero install runbook/prereqs docs.

### 14. Catalog / apps / tools lifecycle — Behind But Aligned
- **Rancher:** ClusterRepo (HTTP/OCI/git) + ManagedChart→Fleet Bundles with RolloutStrategy and catalog operations.
- **Astronomer:** Helm/OCI catalogs (migration 001), project-scoped subscriptions (migration 061), `InstalledChart` + `tool_operations` (migration 009), ClusterTool registry, ratings (073), blessed charts (109), baseline seeding (079). **Corrections:** rollback **is** implemented (`RollbackInstalledChart`, migration 010, helm.Rollback); multi-cluster rollout **is** implemented via `fleet_operations` (migration 056) with parallel/sequential strategies and max-concurrent control.
- **Gaps (confirmed):** No tool **readiness probes** (status reports operation state, not pod readiness); no **drift sweep** on installed tools (unlike ArgoCD apps); no pre-install prerequisite validation; baseline tools split between ApplicationSets and tool_operations paths.

### 15. Service mesh — Ahead
- **Rancher:** A single `istio-virtual-service-ui` feature flag gating VS/DR edit UI + Istio RBAC. No detection, health, mTLS tracking, validation, or ownership safety.
- **Astronomer:** Detects Istio/Linkerd/Kuma/Cilium with version extraction and CRD counts (`internal/mesh/detect.go`), per-namespace mTLS coverage %, ArgoCD-ownership flagging, policy validation including canary weight-sum checks (`service_mesh.go`), 5m periodic detection (`mesh_detect.go`, migration 071), full dashboard UI. **Correction:** status upgraded from "Behind But Aligned" to **Ahead** — Astronomer ships the full suite vs. Rancher's feature flag.
- **Gaps:** None vs. Rancher. (Astronomer never modifies the cluster — read-only by design.)

### 16. Platform settings & UI extension SDK — Behind But Aligned
- **Rancher:** **163–172** settings (originally cited as 165) spanning agent/branding/telemetry/registry/UI customization; UI plugin catalog with caching, versioning, and remote proxy.
- **Astronomer:** **23** settings (originally cited as 18 — migration 046 seeds 18 but the registry has 23) covering branding/banner/feature-flags/argocd/tokens/telemetry/dashboard/registration TLS; extensions handler with manifest validation, install/enable/disable lifecycle, semver compatibility, extension points, CSP, audit (migration 100).
- **Gaps:** Most missing settings are intentional (provisioning/agent-image pins excluded). Real gap: no extension **registry/discovery UI**, no signed-bundle code execution (gated until signed asset loading lands), no marketplace.

### 17. Controller / reconciler maturity — Behind But Aligned
- **Rancher:** Fully controller-driven (Lasso/Wrangler v3, controller-runtime); continuous etcd-watching reconcile; Fleet as native GitOps controller.
- **Astronomer:** Hybrid by design — CRD intent layer via controller-runtime (60s poll, `internal/crd/controller.go`) syncing into Postgres, plus stateless asynq sweeps for monitoring/network-policy/project/cluster-condition/apiserver-allowlist/crd-ownership reconciliation. Durable intent in Postgres+CRDs; transient convergence in tasks with audit + backoff. **Corrections:** network-policy apply does **not** batch-stall (per-row failures continue the loop); backup execution is split task→server-reconciler (`backups_reconciler.go`, 15s), not a no-op.
- **Gaps:** Drift detected on sweep intervals, not instantly; no per-row backoff in some sweeps; PSA labels can drift silently between 30s leases; cluster_condition remediation mints tokens without verifying the tunnel is still down.

### 18. API / SDK / CLI maturity — Ahead
- **Rancher:** K8s clientset only; no public REST contract, no CLI, no explicit rate-limit/idempotency, K8s-autogenerated OpenAPI.
- **Astronomer:** OpenAPI 3.1 with **270+ endpoints** (`docs/openapi.yaml`), generated Go SDK (`pkg/astroclient`) **and** TypeScript SDK (`frontend/src/types/openapi.generated.ts` — originally claimed missing), 11,681-LOC `astro` CLI, per-(user,class) token-bucket rate limiting, in-memory + DB-backed idempotency (migration 097), CRD-native management API (`management.astronomer.io/v1alpha1`), contract CI gate (`.github/workflows/api-contract.yaml`).
- **Gaps:** CRD APIs are v1alpha1; **53** permissive `additionalProperties:true` schemas (originally "~30") weaken Go SDK typing; SSE/octet/yaml endpoints only via low-level client; no breaking-change CI detector.

---

## Prioritized work plan

All `recommendedWork` items across dimensions, sorted by priority. Effort: S/M/L/XL.

| Priority | Area | Work item | Effort | Why |
|---|---|---|---|---|
| **P1** | Cluster import | Hardened token revocation + short-TTL signed manifest URLs | M | Tokens persist indefinitely and embed in URLs; HMAC-signed 15-min URLs enable immediate revocation + audit. |
| **P1** | GitOps engine | Label-based cluster auto-registration (lazy registration) | L | Removes Git-commit + sync-tick friction; matches Fleet ClusterGroup auto-targeting. |
| **P1** | Cluster explorer | Kubernetes watch/streaming API for real-time state | M | Closes the biggest UX gap vs. Rancher; ends polling for live dashboards. |
| **P1** | Cluster explorer | Make Prometheus metrics non-optional for cluster overview | M | Cluster explorer is far weaker without baseline metrics; wiring already exists. |
| **P1** | Alerting | Cross-cluster anomaly baselines | L | Deferred from sprint 072; enables fleet-wide outlier detection. |
| **P1** | Alerting | Recovery/resolved notification templates | S | On-call expects auto-resolve; today channels only fire, never clear. |
| **P1** | Security | Audit-log collection from apiserver + kubelet | L | Allowlist defends ingress but logs no access; closes a compliance/forensics gap. |
| **P1** | Compliance | RBAC-gated webhook/SMTP deletion guard when baseline requires it | M | Operators can currently break a baseline-mandated control; v2 deferral. |
| **P1** | RBAC | Implement namespace-scoped role binding CRUD + enforcement | M | Spec'd but storage/API/UI pending; required for true Rancher parity + fine-grained delegation. |
| **P1** | RBAC | Harden scope-enforcement middleware for all routes | M | Read-scoped tokens can mutate/exec on opt-in routes; make write-scope the default. |
| **P1** | Auth | Add SCIM 2.0 user/group provisioning | L | Procurement blocker for regulated buyers; replaces manual group mappings. |
| **P1** | Auth | MFA enforcement (mandate TOTP per role/project) | M | TOTP is opt-in; PCI-DSS buyers need an admin enforcement toggle. |
| **P1** | Audit | Expose task-outbox DLQ as admin endpoint | M | Operators need visibility into permanently-failed operations (note: status=dead filter exists; add dedicated view). |
| **P1** | Backup/DR | Enable management backup + restore drill by default | S | Production should be NIST-compliant out of the box (opt-out for dev). |
| **P1** | Catalog/tools | Add tool readiness checks via release status probes | M | Today installs are fire-and-forget; needed for day-2 convergence confidence. |
| **P1** | Catalog/tools | Implement tool drift reconciliation sweep | M | "Clusters never drift" promise; apply the monitoring pattern to tools. |
| **P1** | Catalog/tools | Unify baseline delivery into one tool lifecycle path | L | Two paths (AppSet vs tool_operations) split ownership/reconciliation semantics. |
| **P1** | Platform/SDK | Signed extension bundle validation (Ed25519 + checksum + CSP) | M | Unblocks executable extensions, currently gated. |
| **P1** | API/SDK | Promote CRD APIs to v1beta1 + versioning policy docs | M | v1alpha1 signals instability to operators building on the CRD layer. |
| **P1** | Controller model | cluster_condition remediation: pre-condition checks before token reissue | M | Avoids spamming tokens at briefly-disconnected agents; reduces tickets. |
| **P1** | Controller model | Document the day-2 reconciliation model for platform teams | S | Rancher operators expect controllers; the task-driven model is intentional but non-obvious. |
| **P1** | Provisioning | Fix marketing copy (`MARKETING.md` line 57); clarify day-2 boundary | S | "Provisions new ones" contradicts product scope; align with README. |
| **P2** | Cluster import | Formalize registration SDK/CLI automation | M | Replaces curl flow with `astro clusters import/adopt-baseline`. |
| **P2** | Cluster import | Agent-version-skew + CA-expiry condition probes | M | Dispatch table exists; sketched in comments but unimplemented. |
| **P2** | Cluster import | Per-tool install-step granularity in timeline | S | UI can show per-tool install progress during baseline apply. |
| **P2** | GitOps engine | ApplicationSet factory/builder API | M | Abstracts raw ClusterGenerator YAML to match Fleet's abstraction level. |
| **P2** | GitOps engine | Expose sync wave/phase order in ownership endpoint | S | Makes multi-component rollout coordination visible. |
| **P2** | Cluster explorer | Pod-container exec UX (endpoint already exists) | M | Wire existing exec WS into per-container debug; supplement to kubectl shell. |
| **P2** | Cluster explorer | Server-side apply / conflict detection on resource edits | L | Replaces blind PUT; prevents concurrent-edit overwrites. |
| **P2** | Monitoring | Metrics history export (S3/GCS archival) | M | Audit/compliance trail independent of Thanos retention. |
| **P2** | Monitoring | Stack auto-discovery + helm-chart-diff drift view | M | Surfaces actual drift beyond readiness probes. |
| **P2** | Alerting | Alertmanager inhibition rules | M | Auto-suppress low-severity when high-severity fires; native AM feature. |
| **P2** | Alerting | Externalize routing/grouping params to rule level | M | Replace hard-coded 30s/5m/3h with per-rule timing. |
| **P2** | Alerting | Alertmanager native silence integration | L | Server-side suppression instead of in-band control-plane filtering. |
| **P2** | Alerting | Multi-tenant routing scopes per project | M | Prevents alerting privilege escalation on shared clusters. |
| **P2** | Logging | QueryOutput backend client for Loki/Elasticsearch | L | `QueryOutput` is 501; unblocks dashboard log search. |
| **P2** | Logging | Per-forwarder log retention policy + age-out warnings | S | Down forwarders age out queue rows before dispatch. |
| **P2** | Logging | Document management-plane log backend setup | S | Default `loki` fails in air-gapped deploys; needs operator docs. |
| **P2** | Security | Custom Gatekeeper constraint authoring UI | M | Operators stuck with 3 hardcoded constraints; unlock Gatekeeper's value. |
| **P2** | Security | CVE alert webhooks on new critical/high vulns | M | Ingest is passive; webhook dispatch enables incident integration. |
| **P2** | Compliance | Surface audit-retention sweep status in compliance export | S | Auditors need to verify the cleanup job runs. |
| **P2** | Compliance | Baseline-revert safety check for stale (>90d) baselines | M | Long-lived baseline revert can silently downgrade controls. |
| **P2** | RBAC | Catalog drift detection + versioning (hash on platform config) | S | Embedded YAML and DB seed are separate truths; prevent silent loss. |
| **P2** | RBAC | Role inheritance/composition via `inherits` field | M | Field exists; engine not implemented; reduces duplication, eases Rancher transition. |
| **P2** | RBAC | Expose effective-permission preview in binding UI | M | "What will user X get?" before commit; reduces over-grant. |
| **P2** | Auth | Password policy enforcement (length/complexity/expiry/history) | M | PCI-DSS 8.2.2 / ISO 27001 compliance knobs. |
| **P2** | Auth | Session timeout + inactivity logout (per-role) | M | Fixed JWT expiry only; needs absolute + inactivity cutoffs. |
| **P2** | Auth | Native Azure AD provider | L | Removes Dex latency/ops for hybrid AD shops. |
| **P2** | Auth | OAuth device code flow (RFC 8628) | S | Headless/CLI auth matching `kubectl login`. |
| **P2** | Auth | Auth-event audit table (login/token/provider changes) | M | SOC 2 evidence. |
| **P2** | Audit | Webhook dispatch retry/DLQ tracking | L | Webhook delivery is fire-and-forget; mirror SIEM queue pattern. |
| **P2** | Audit | Control-plane + agent audit correlation view | L | Cluster-agent and management events share no thread today. |
| **P2** | Backup/DR | Document per-cluster Velero setup + prerequisites | M | Snapshot API assumes Velero is installed. |
| **P2** | Backup/DR | Extend restore-drill pattern to per-cluster snapshots | L | Catches silent Velero/CSI failures. |
| **P2** | Catalog/tools | Rollout strategy enhancements for cross-cluster tool deploys | L | Canary/serial/parallelism for fleet-wide chart rollouts. |
| **P2** | Catalog/tools | Expose helm release history + rollback API | S | Inspect revisions/values and revert on failure. |
| **P2** | Catalog/tools | Tool pre-install readiness validation | M | Validate node count/resources/CRDs before install. |
| **P2** | Platform/SDK | UI extension registry + discovery UI | L | Enables browsing/versioning/one-click install. |
| **P2** | Platform/SDK | Document extension SDK + best practices | S | Well-built but undocumented; partner enablement. |
| **P2** | API/SDK | Reduce permissive OpenAPI stubs (53 schemas) | L | Improves Go SDK type safety. |
| **P2** | API/SDK | Auto-generate + CI-test TypeScript SDK (TS gen exists; harden) | M | Frontend/automation typed client. |
| **P2** | API/SDK | API stability tiers + breaking-change CI detector | M | Catch removed fields/changed methods; enforce deprecation windows. |
| **P2** | Controller model | Per-row retry/backoff within reconcile sweeps | M | Transient failures shouldn't delay unrelated rows. |
| **P2** | Controller model | PSA validator in project:reconcile for out-of-band label drift | S | Catch manual kubectl namespace edits. |
| **P2** | Controller model | Drift-check → high-priority apply fan-out (network policy) | M | Shrinks inconsistency window from 5m to <1m. |
| **P3** | Provisioning | "Bring-your-own-cluster" quick-start (Terraform/CAPI refs) | M | Guides external provisioning then import. |
| **P3** | Cluster import | Cloud-account discovery / bulk import | L | Out of day-2 scope; defer unless multi-cloud at scale. |
| **P3** | Cluster import | Agent auto-upgrade + version reconciliation | M | Detect stale agents, enqueue upgrade. |
| **P3** | GitOps engine | Resource-level drift drill-down in orphan-report | M | Per-resource drift reasons beyond stale-destination. |
| **P3** | GitOps engine | ApplicationSet-to-Helm factory for operator-supplied baselines | L | Removes YAML authoring for custom baseline components. |
| **P3** | Cluster explorer | Dynamic CRD discovery for arbitrary resource CRUD | M | Match Rancher genericity beyond the 33-type allowlist. |
| **P3** | Cluster explorer | Three-way diff preview before resource updates | S | Show impact before confirming edits. |
| **P3** | Monitoring | Per-operation rollback audit trail + canary gates | L | Track rollback reasons; optional manual-approval gate. |
| **P3** | Monitoring | Cost/compliance dashboards | S | Round out the 6 operational dashboards. |
| **P3** | Monitoring | Canary/shadow monitoring stack mode | L | Test major Prometheus/Thanos upgrades before cutover. |
| **P3** | Alerting | Inhibition/silence matcher UI | S | Surfaces backend inhibition work to operators. |
| **P3** | Alerting | Rule composition / includes | L | Organize rules into logical groups; path-traversal care. |
| **P3** | Alerting | Alert-event metrics for Prometheus scrape | S | Observe alerting-system health in operator dashboards. |
| **P3** | Security | Registry authentication posture scanner | L | Detect weak/public registry auth. |
| **P3** | Security | Policy-violation auto-remediation workflows | XL | Auto-eviction/quarantine/PSA escalation; high complexity. |
| **P3** | Compliance | Sensitivity-based posture weighting per workload tier | L | HIPAA projects weight audit higher than flat default. |
| **P3** | Auth | Conditional/risk-based auth (geo/IP anomaly) | XL | Tier-1 fintech; defer. |
| **P3** | Audit | Audit log sampling + tail API (sampling done; add tail) | M | Real-time ops dashboards (sampling already implemented). |
| **P3** | Audit | HIPAA/PCI compliance report builder | M | Templated audit reports + CSV export. |
| **P3** | Backup/DR | Integrate backup-restore-operator for multi-cluster governance | XL | Deferred pending Fleet-adoption clarity. |
| **P3** | Catalog/tools | Catalog health aggregation dashboard | M | Fleet-wide tool install/ready %. |
| **P3** | Catalog/tools | Surface project-owned source repos in tool browsing | S | Schema-ready (`owner_project_id`) but not exposed. |
| **P3** | Platform/SDK | Expand settings registry for day-2 observability | S | 3–5 governance settings (sampling, retention, signing key). |
| **P3** | Controller model | crd_ownership_drift spec-divergence detection | L | Detect stale/invalid CR specs, not just missing refs. |
| **P3** | Controller model | API-server allowlist cloud-drift change webhook | L | Cut snapshot-to-remediation latency from 15m to <1s. |
| **P3** | API/SDK | Document idempotency guarantees + recovery patterns | S | Helps SDK users know when retries are safe. |

### Recommended sequencing

The P0/P1 work clusters into **three themes**, best executed in parallel by separate workstreams:

1. **Apply the monitoring pattern to the rest of the task-driven plane (controller-pattern rollout).** Monitoring already proves readiness gates + auto-rollback + drift detection. Port that maturity outward: tool readiness probes, tool drift reconciliation, and the unified tool-lifecycle path (P1, Catalog); per-row backoff and the cluster_condition pre-condition fix (P1/P2, Controller model); the live-watch/streaming API and default-on metrics (P1, Cluster explorer). Ship the reconciliation-model doc first so platform teams understand the intentional task-driven design.

2. **Close the enterprise identity & access gap (RBAC + auth providers).** This is the cluster most tied to procurement: namespace-scoped bindings, default write-scope enforcement, and role inheritance (RBAC); SCIM 2.0 and MFA enforcement (Auth). These unblock regulated buyers and bring RBAC from "Behind But Aligned" toward parity.

3. **Harden governance & GitOps trust surfaces.** Signed-manifest registration tokens, label-based ArgoCD auto-registration, apiserver/kubelet audit collection, the webhook/SMTP compliance guard, default-on backup+restore drills, and v1beta1 CRD promotion. These are smaller, mostly independent items that collectively raise the "trust the platform" bar and remove the few self-inflicted defaults (backup disabled, marketing copy).

---

## Intentionally excluded — what Astronomer is NOT building

Astronomer is a day-2 operations platform. The following are out of scope **by design**, not gaps:

| Excluded | Why |
|---|---|
| **Cluster provisioning & node lifecycle** | No EKS/GKE/AKS/RKE2 creation, no machine drivers, no node pools, no infrastructure autoscaling. Clusters are provisioned externally (Terraform, cloud consoles, CAPI) and adopted. See `README.md` lines 21-33, 169. (`MARKETING.md` line 57 must be corrected to drop the false "provisions new ones" claim.) |
| **Fleet as GitOps engine** | Astronomer uses **bundled ArgoCD** (ApplicationSets, sync waves, ownership tracking), not `fleet.cattle.io`. No Fleet Bundles, GitRepo, or ClusterGroup CRDs. |
| **Pure controller-runtime everywhere** | Astronomer is a **hybrid**: Postgres + asynq tasks for durable, auditable, history-bearing convergence, with CRDs as an *intent surface* (not a DB mirror). This is deliberate — it trades instant convergence for auditability and durability. |
| **Cloud-account cluster discovery / bulk import** | No AWS/GCP/Azure account scanning to auto-enumerate clusters. Adoption is explicit per cluster. |
| **License / entitlement parity with Rancher Prime** | Not a goal; Astronomer's value is the day-2 governance surface, not feature-for-feature commercial parity. |
| **Signed-bundle extension code execution (today)** | Extension manifests validate and register, but executable bundles stay disabled until Ed25519 signed-asset loading ships (tracked as P1). |
