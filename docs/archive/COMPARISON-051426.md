# Rancher vs Astronomer — UI / feature comparison, 2026-05-14

Sources:
- Rancher backend: `../rancher/` (this checkout)
- Rancher UI behavior: documented behavior + the `pkg/features`, `pkg/data/management`, `pkg/settings`, `pkg/auth`, `pkg/catalogv2` subtrees
- Astronomer: `astronomer-go/` (this repo), the 89 page.tsx files under `frontend/src/app/dashboard/`, the 101 Go handlers under `internal/handler/`

Each row uses three flags:
- ✅ shipped
- ⚠️ partial or backend-only
- ❌ not shipped

A "drill-down" annotation in the cells calls out whether the UI lets you go from the list view down to per-resource detail / edit / action.

---

## 0. Headline scoreboard

| Area | Rancher | Astronomer | Gap |
|---|---|---|---|
| Managed-k8s provisioning (EKS/GKE/AKS/OKE/LKE/+8 more) | ✅ | ❌ | Major; user-declined per project scope |
| Local/edge provisioning (RKE2, k3s, custom) | ✅ | ❌ | Major; user-declined |
| **Import & manage existing clusters** | ✅ | ✅ | At parity |
| **Per-cluster workload browser** | ✅ deep | ✅ broad | Less deep YAML/edit fidelity |
| **Built-in roles** | 41 role templates | 8 + open-source RBAC engine | We're at 20 % of the breadth |
| **Auth providers** | 12 (AD/LDAP/Azure/GitHub/Google/Okta/Keycloak/SAML/GenericOIDC/Cognito/Ping/Shib) | 7 (local/JWT/Dex/OIDC/SAML/TOTP/4 SSO presets) | ~60 % coverage |
| **Settings catalog** | 162 platform settings | ~16 platform settings | We expose far less |
| **Catalog / Helm** | OCI + Git + HTTP + cluster-scoped + project-scoped | OCI + Git + HTTP + cluster + project | At parity (we're better on project-catalogs UX) |
| **Monitoring stack** | Curated kube-prometheus-stack + 20+ dashboards | kube-state-metrics + node-exporter + fluent-bit + 6 dashboards | Smaller dashboard library |
| **Logging** | Banzai/fluent | fluent-bit + multi-output + SIEM forwarder | At parity |
| **CIS scans** | cis-operator | cis-operator (same upstream) | At parity |
| **Image vulnerability scans** | Optional (Aqua/Trivy via charts) | ✅ first-class, fleet rollup, top-N drilldown | We're ahead |
| **Fleet (multi-cluster GitOps)** | ✅ fleet.cattle.io | ⚠️ ArgoCD integration + fleet_operations selector | Different model; ours uses ArgoCD |
| **Backups** | Backup operator (Rancher CRD; mgmt-plane only) | Velero per-cluster + management-plane pg_dump + restore drill | We're ahead on per-cluster + drill |
| **Service mesh** | Istio UI ✅ | Detect + install Istio/Linkerd/Kuma/Cilium ✅; no traffic-rule UI | Behind on Istio CRD UI |
| **API server allow-list** | Custom feature | ✅ migration 070 | We have this |
| **In-app kubectl shell** | ✅ | ✅ (cross-pod WS proxy as of today) | At parity |
| **License/entitlement** | License key file/manage | Read-only OSS scaffold | Different model — declined feature parity |
| **Cluster templates** | RKE2 templates (cluster spec only) | Cluster-template binding + tool/PSS/quota/netpol blueprint | We're broader |
| **Compliance baselines (PCI/HIPAA/SOC2/FedRAMP)** | ❌ | ✅ (migration 064) | We're ahead |
| **Compliance posture rollup** | ❌ | ✅ T1.2 | We're ahead |
| **Webhook subscriptions** | Limited | ✅ first-class admin CRUD | We're ahead |
| **Audit log search + export** | Limited | ✅ V1 schema, retention sweeper, SIEM forwarder | We're ahead |
| **Anomaly baselines on metrics** | ❌ | ✅ migration 072 | We're ahead |
| **Telemetry opt-in** | ✅ rancher-telemetry | ✅ migration 046 | At parity |
| **CRD mirror table for fast UI render** | ❌ (live informer) | ✅ migration 069 | Different approach |
| **Maintenance windows** | Limited | ✅ migration 050 | We're ahead |
| **Cluster groups + bulk operations** | ⚠️ via Fleet labels | ✅ first-class cluster_groups + fleet_operations | We're ahead |
| **Pluggable UI extensions** | ✅ UIExtension feature | ❌ | Behind |
| **Marketplace / extension catalog** | ✅ Apps & Extensions catalog | ⚠️ helm catalog only | Different model |

**Net read.** We are a meaningfully thinner cluster-provisioning product (and that's intentional — Astronomer declined EKS/GKE/AKS/RKE), and have a smaller built-in role catalog, fewer auth providers, fewer chart dashboards, and no UI extension SDK. Rancher is meaningfully thinner on first-class security posture (image vulns, compliance baselines, audit-log retention, anomaly baselines), on operational levers (cluster groups, fleet operations, maintenance windows), and on per-cluster operational rollups.

---

## 1. Cluster lifecycle

| Capability | Rancher | Astronomer |
|---|---|---|
| Provision **EKS** | ✅ kontainerdriver `amazonelasticcontainerservice` | ❌ |
| Provision **GKE** | ✅ kontainerdriver `googlekubernetesengine` | ❌ |
| Provision **AKS** | ✅ kontainerdriver `azurekubernetesservice` | ❌ |
| Provision **OKE** (Oracle) | ✅ as a custom driver | ❌ |
| Provision **LKE** (Linode) | ✅ as a custom driver | ❌ |
| Provision **DOKS** (DigitalOcean) | ✅ (`digitalocean` machine driver) | ❌ |
| Provision **RKE2** | ✅ capr/provisioningv2 | ❌ |
| Provision **k3s** | ✅ | ❌ |
| Provision via Harvester | ✅ HarvesterBaremetalContainerWorkload feature | ❌ |
| Custom node bootstrap (token + script) | ✅ | ❌ |
| **Import existing cluster** (curl-able manifest) | ✅ | ✅ Rancher-style 1-liner with short-TTL token (T6.078) |
| Registration wizard (multi-step) | minimal | ✅ migration 078 — full wizard with phase state machine + per-step audit |
| Cluster decommission (graceful, audited) | ✅ | ✅ phased reconciler: managed-side cleanup → revoke token → archive audit → delete deps → tombstone |
| Cluster status conditions | partial | ✅ migration 035 + reconciliation (sprint 086) |
| Per-cluster cluster templates | RKE2 templates (machine pools etc.) | ✅ tool/PSS/quota/netpol blueprint + binding + drift-check + reapply |
| Cluster groups (logical sets) | ⚠️ Fleet labels | ✅ first-class `cluster_groups` table + group-scoped fanout |
| Bulk fleet operations (drain, tool upgrade, template apply across N clusters) | ⚠️ via Fleet bundles | ✅ migration 056 `fleet_operations` + selector evaluator + per-cluster status |
| **Verdict** | Vastly broader provisioning surface | Better registration UX + lifecycle audit for imported clusters |

**Drill-down comparison.** From the cluster list, Rancher → cluster detail → cluster YAML edit + node-pool edit + upgrade k8s version + add/remove node pool. We → cluster detail → template binding + tools + apps + image-scans + network-access + network-policies + provisioning timeline + shell + snapshots. We can't change k8s version because we don't own the underlying control plane.

---

## 2. Cluster Explorer (per-cluster resource browser)

The Rancher Dashboard's flagship feature.

| Tab | Rancher | Astronomer |
|---|---|---|
| **Workloads** (Deployment / DS / SS / Job / CronJob) | ✅ unified table | ✅ `/clusters/[id]/workloads` + per-kind drill-in via `[kind]/[namespace]/[name]` |
| **Pods** | ✅ | ✅ |
| Logs (streaming) | ✅ | ✅ via `/api/v1/workloads/pods/{c}/{ns}/{p}/logs/` + `useLogStream` |
| Exec (in-browser shell) | ✅ | ✅ `astro-shell` pod + WS bridge (cross-pod hand-off fix as of today) |
| Describe | ✅ | ⚠️ resource detail page renders YAML view; no synthesised `kubectl describe` events block |
| Edit YAML in browser | ✅ live editor | ⚠️ raw YAML view on `[resource]` page; PATCH wired but UI edit affordance is thin |
| Restart workload | ✅ | ✅ `Workloads.Restart` handler |
| Scale workload | ✅ slider | ✅ `Workloads.Scale` handler (number input) |
| Cordon / drain node | ✅ | ⚠️ via shell only |
| **Services** | ✅ | ✅ `/resources/services` |
| **Ingresses** | ✅ | ✅ `/resources/ingresses` |
| **HPA** | ✅ | ✅ `/resources/generic/hpa` |
| **NetworkPolicies** | ✅ | ✅ `/resources/generic/networkpolicies` + dedicated templates UI |
| **ConfigMaps / Secrets** | ✅ | ✅ |
| **PVs / PVCs / StorageClasses** | ✅ | ✅ |
| **CRDs** | ✅ discovery + schema-aware forms | ⚠️ list only, no schema-aware editor |
| **RBAC (k8s ClusterRole/RoleBinding)** | ✅ | ✅ `k8s-clusterroles` / `k8s-clusterrolebindings` etc. |
| **Gateway API** | ⚠️ | ✅ Gateways, HTTPRoutes, GatewayClasses, GRPC/TLS/TCP/UDP/ReferenceGrant |
| **Service mesh** (Istio routing) | ✅ VirtualService + DestinationRule UI behind `IstioVirtualServiceUI` feature gate | ⚠️ detection + install; no traffic-rule UI |
| Bulk select + bulk delete | ✅ | ⚠️ per-row actions only |
| Per-resource live-watch | ✅ | ⚠️ polling at 15-30s; CRD-mirror table updates incrementally for some kinds |
| Cross-resource search | ✅ "Ctrl+/" command bar | ⚠️ `/search` page exists but no global keyboard hotkey |

**Drill-down depth.** Rancher's flagship: select a Pod → side drawer with tabs for *Detail / YAML / Conditions / Logs / Exec*, all live-tailing, all editable in place. Ours: workload list → click row → workload detail page with conditions + pods table + apps reference; logs + exec open as separate WindowManager tabs. We have the pieces; Rancher has the polish.

---

## 3. Authentication

### Rancher's 12 providers

| Provider | Rancher | Astronomer |
|---|---|---|
| Local users + bcrypt | ✅ | ✅ |
| Active Directory (LDAP-bind) | ✅ | ⚠️ via generic OIDC / SAML; no LDAP-bind handler |
| OpenLDAP / FreeIPA | ✅ | ⚠️ as above |
| Microsoft Entra ID (Azure AD) | ✅ first-class | ✅ SSO preset (`azure-ad`) over OIDC |
| GitHub | ✅ first-class | ✅ SSO preset over OAuth2 |
| GitHub Apps | ✅ | ⚠️ not as a first-class shape |
| Google OAuth | ✅ first-class | ✅ SSO preset |
| Okta | ✅ first-class | ✅ SSO preset over OIDC |
| Keycloak OIDC + SAML | ✅ | ⚠️ via generic OIDC / SAML |
| PingIdentity | ✅ | ⚠️ via generic OIDC |
| Shibboleth | ✅ SAML | ✅ generic SAML |
| Generic OIDC | ✅ | ✅ |
| Generic SAML | ✅ | ✅ |
| AWS Cognito | ✅ | ⚠️ via generic OIDC |
| SCIM provisioning | ✅ behind `SCIM` feature | ❌ T7.7 deferred (gated on buyer demand) |
| MFA / TOTP | ✅ | ✅ |
| Backup codes | ✅ | ✅ |
| Service account / API tokens | ✅ | ✅ (`api_tokens` + JWT) |
| Per-user token revocation | ✅ | ✅ (migration 039) |
| Account lockout | ✅ | ✅ (migration 039) |
| Per-cluster impersonation (`kubeconfig`) | ✅ proxied + audited | ✅ token-scoped per-cluster kubeconfig generation |

**UI surface.** Rancher: each provider has a branded card with the right fields. We ship 5 first-class SSO presets (github/google/azure-ad/gitlab/okta via the sprint-076 `sso_presets`) — the operator gets the right form per provider — plus a generic OIDC/SAML card for everything else.

---

## 4. RBAC

### Built-in role templates

Rancher's `pkg/data/management/role_data.go` declares **41 role templates** out of the box:

```
Kubernetes cluster-admin, admin, edit, view
Cluster Owner, Cluster Member
Create / View Projects
Manage / View Nodes
Manage Storage
Manage / View Cluster Members
Manage / View Cluster Catalogs
Manage Cluster Backups
Manage Navlinks
Project Owner, Project Member, Read-only
Create Namespaces
Manage / View Workloads
Manage / View Ingress
Manage / View Services
Manage / View Secrets
Manage / View Config Maps
Manage / View Volumes
Manage / View Service Accounts
Manage / View Project Members
View Monitoring (project + cluster scope)
View Navlinks
+ a handful of permission-aggregate roles
```

Ours (sprint T1.1, `internal/rbac/templates/`):

```
platform-admin, compliance-auditor, support-engineer    (global)
cluster-operator, cluster-viewer                         (cluster)
project-owner, project-member, project-viewer            (project)
```

**Gap:** 8 vs 41. The shape is right (RBAC engine, scope semantics, role-template apply path) but our catalog covers ~20 % of Rancher's breadth. Filling the gap is bounded YAML work (`internal/rbac/templates/*.yaml`) but a real cost.

### RBAC features beyond templates

| Capability | Rancher | Astronomer |
|---|---|---|
| Three-tier scope (global / cluster / project) | ✅ | ✅ |
| Role inheritance / aggregation | ✅ AggregatedRoleTemplates feature | ❌ |
| Group sync from IdP | ✅ | ✅ (migration 042) |
| Group mappings (IdP group → role) | ✅ | ✅ |
| Per-cluster role bindings | ✅ | ✅ |
| Project role bindings | ✅ | ✅ |
| RBAC matrix view | ⚠️ | ✅ `/projects/{id}/rbac/` matrix |
| Custom roles UI | ✅ | ✅ |
| Builtin-role protection (refuse edit/delete) | ✅ | ✅ migration sprint 32 + T6.074 |

---

## 5. Projects (multi-tenancy)

Rancher invented "Project" as a label across namespaces with shared quota + RBAC + monitoring.

| Capability | Rancher | Astronomer |
|---|---|---|
| Project as multi-namespace container | ✅ | ✅ |
| Cross-cluster project | ❌ (cluster-scoped) | ✅ `project_namespaces` table spans clusters |
| Project resource quota | ✅ | ✅ CPU/mem/pod-count quota |
| Project PSA / PodSecurityStandards | ⚠️ via labels | ✅ first-class `PodSecurityProfile` enum + reconciler |
| Project NetworkPolicy default | ⚠️ | ✅ `NetworkPolicyMode` enum + reconciler |
| Project members + RBAC | ✅ | ✅ |
| Project monitoring isolation | ✅ Project Monitoring | ⚠️ relies on per-cluster monitoring |
| Project app catalog | ✅ project-scoped catalogs | ✅ `project_catalogs` (sprint 078) — operator can scope a chart repo to a project |
| Project quotas dashboard | ✅ | ✅ `/settings/quotas/usage` |
| Multi-cluster project view UI | ❌ | ✅ T4.3 `/projects/{id}/clusters/` (count + name list per cluster) |

**Verdict.** We're broader on Project semantics — cross-cluster, opinionated PSS/netpol/quota, project-scoped catalogs. Rancher's UI is more polished but operates on a narrower (cluster-bound) model.

---

## 6. Catalog / Apps (Helm)

| Capability | Rancher | Astronomer |
|---|---|---|
| Helm v3 install/upgrade/rollback/uninstall | ✅ | ✅ |
| Repository CRUD | ✅ | ✅ |
| OCI registry support | ✅ catalogv2/oci | ✅ migration sprint 047 `helm_oci_*` |
| Git-as-repo | ✅ catalogv2/git | ✅ via the gitops sources (migration 060) |
| Values editor (form + YAML) | ✅ schema-driven form when JSON schema present | ✅ helm-values-schema form + YAML fallback (`HelmValuesForm`) |
| Per-chart README rendering | ✅ | ✅ |
| Chart-version history | ✅ | ✅ |
| Multi-version rollback | ✅ | ✅ `rollbackChart` mutation |
| Per-cluster install scoping | ✅ | ✅ |
| Per-project install scoping | ⚠️ | ✅ `project_catalogs` + `deploy-to-project` modal (sprint 21) |
| Bundled chart repository (in-cluster, no internet) | ✅ Rancher Apps & Marketplace | ✅ `astronomer-server` serves `/helm-repo/astronomer-v2` |
| Recommended/suggested catalogs | ⚠️ | ✅ `SuggestedCatalogs` widget + chart_recommendations recompute |
| Chart ratings + co-installation matrix | ❌ | ✅ migration 055 |
| Failed-install cleanup | ⚠️ | ✅ `DeleteFailedInstallationsByCluster` + UI CTA |
| Catalog `?cluster_id=` deeplink | ❌ | ✅ T5.2 |
| Helm hardened-images repo | ⚠️ | ✅ migration 089 seeds docker-hardened-images |

**Verdict.** Roughly at parity on Helm install; we're meaningfully ahead on operator UX (failed-install cleanup, deeplink scoping, recommendations, project-scoped catalogs).

---

## 7. Monitoring

| Capability | Rancher | Astronomer |
|---|---|---|
| kube-prometheus-stack | ✅ Monitoring V2 (managed chart) | ⚠️ we install kube-state-metrics + node-exporter + fluent-bit but not the prom-operator itself |
| Grafana auto-import dashboards | ✅ Rancher Monitoring | ✅ `metrics.dashboards.enabled` ConfigMap loader (T4.4) |
| Curated dashboards (count) | ~20 (cluster, workload, etcd, kubelet, scheduler, controller-manager, persistentvolume, node-detail, namespace, traefik, etc.) | 6 (cluster-overview, workload-health, node-usage, image-scan-summary, baseline-tool-health, fleet-cve-rollup) |
| Project-scoped Grafana | ✅ | ⚠️ |
| AlertManager UI | ✅ | ⚠️ `/alerting` page (rules, channels, events, silences) but not AlertManager-config-shaped |
| Prometheus rules editor | ✅ | ✅ `/alerting` rule CRUD |
| Notification channels (Slack/PagerDuty/MS Teams/Email/Webhook) | ✅ | ✅ all 5 native formatters (sprints 15/16 + cleanup T4.1 verify) |
| Alert silences | ✅ | ✅ |
| Anomaly baselines | ❌ | ✅ migration 072 + 5m recompute + cluster-detail panel (T7.2) |
| Per-cluster live metrics summary | ✅ | ✅ `/clusters/{id}/metrics/summary/` |
| Fleet-wide metric rollup | ⚠️ | ✅ image-vuln fleet, baseline coverage, fleet CVE rollup dashboard |
| Logging-pipeline flatline alert | ❌ | ✅ T7.1 (`AstronomerClusterLoggingFlatlined`) |
| Custom dashboards (in-app builder) | ✅ | ✅ `/settings/widgets` + `dashboards` table with per-user / per-project / per-cluster scopes (migration 058) |

---

## 8. Logging

| Capability | Rancher | Astronomer |
|---|---|---|
| Cluster log forwarder | ✅ Banzai Logging Operator | ✅ fluent-bit DaemonSet (managed) |
| Multi-output (Splunk, Elastic, Loki, S3, syslog, Kafka, Datadog, …) | ✅ via Banzai outputs CRDs | ✅ via `logging_outputs` schema + dispatcher (sprints 13-14) |
| SIEM forwarder | ⚠️ via output sink | ✅ first-class `internal/siem` + audit fan-out |
| Pre-defined output templates | ✅ | ✅ `/logging` page CRUD + templates |
| Logs viewer in UI (live tail per pod) | ✅ | ✅ `/api/v1/workloads/pods/{...}/logs/` WS |
| Management-plane log forwarder | ❌ | ✅ `managementLogging` DaemonSet (chart-managed) |
| Management-plane flatline alert | ❌ | ✅ `AstronomerManagementLoggingForwarderDown` prom rule |

---

## 9. Security

| Capability | Rancher | Astronomer |
|---|---|---|
| CIS benchmark scans | ✅ cis-operator chart | ✅ cis-operator (same upstream) |
| CIS scan history | ✅ | ✅ |
| Image vulnerability scans (Trivy) | ⚠️ via Aqua chart | ✅ first-class — fleet rollup, top-N drilldown, per-namespace breakdown, image-vuln-history snapshot (migration 081) |
| Vulnerability severity rollup | ⚠️ | ✅ Critical/High/Med/Low/Unknown per cluster + fleet |
| Image-scan progress UI | ❌ | ✅ `image_vulns_progress.go` |
| Pod Security Standards (PSA) enforcement | ⚠️ via labels | ✅ first-class `pod_security_templates` + per-project profile + per-cluster policy reconciler |
| NetworkPolicy templates | ⚠️ | ✅ `network_policy_templates` + per-cluster apply reconciler + drift check (migration 068) |
| AdmissionPolicy / OPA / Gatekeeper | ⚠️ as a chart | ⚠️ via baseline; no first-class UI |
| Audit log search | ⚠️ external | ✅ V1 audit schema, retention, partitions, export, SIEM forwarder |
| Audit log retention setting | ⚠️ | ✅ `AuditLogRetentionMonths` (default 13) |
| Audit log export (CSV/JSON) | ⚠️ external | ✅ `/admin/compliance/export/` |
| **Compliance baselines** (PCI / HIPAA / SOC2 / FedRAMP) | ❌ | ✅ migration 064 + spec registry + apply/revert |
| Compliance posture rollup | ❌ | ✅ T1.2 (CIS 30 % / Vulns 30 % / NetPol 25 % / Audit 15 %) |
| API server allow-list | ⚠️ | ✅ migration 070 + per-cluster reconciler |
| Read-audit policies (DLP-style "this account read X PHI rows") | ❌ | ✅ migration 062 |

**Verdict.** This is the clearest area where we are *ahead* of Rancher. Audit, compliance, vuln scanning, posture rollup, PSS/netpol enforcement are deeper.

---

## 10. GitOps

| Capability | Rancher | Astronomer |
|---|---|---|
| Multi-cluster GitOps | ✅ Fleet (`fleet.cattle.io`) | ⚠️ via ArgoCD integration |
| ArgoCD instance registration | ⚠️ as a chart | ✅ first-class `argocd_instances` + UI proxy + register-flow |
| ArgoCD Applications UI | ⚠️ | ✅ `/argocd/[instanceId]/applications/[appId]` proxy |
| ApplicationSet fan-out | ⚠️ | ✅ `/argocd/[instanceId]/applicationsets/new` |
| ArgoCD self-manage (chart manages itself via Argo) | ⚠️ | ✅ `astronomer-self-manage` Application |
| GitOps sources (raw Git repos for cluster registration) | ⚠️ via Fleet GitRepo | ✅ migration 060 + Fernet-encrypted auth (T6.060) |
| Sync mode (manual / interval / hook) | ✅ | ✅ |
| On-delete behavior (orphan / sweep / log) | ✅ | ✅ `OnDelete` enum |

**Different model.** Rancher uses Fleet (cattle-native, agent-based, label selectors). We use ArgoCD (Kubernetes-native, sync-based, app-of-apps). Both work; we don't ship Fleet.

---

## 11. Backups + DR

| Capability | Rancher | Astronomer |
|---|---|---|
| Managed-cluster backup (Velero) | ⚠️ via chart | ✅ first-class Velero CRD round-trip + backup-storage CRUD + schedule UI |
| Per-cluster backup schedules | ⚠️ | ✅ `/backups/schedules/new` + cron |
| Restore UI | ⚠️ | ✅ `/backups/restores/[restoreId]` |
| Backup history + status drilldown | ⚠️ | ✅ `/backups/runs/[runId]` |
| Management-plane Postgres backup | ⚠️ (Rancher Backup operator for the Rancher CRDs) | ✅ `management-plane-backup-cronjob.yaml` (nightly pg_dump → S3) |
| Restore drill (test the restore!) | ❌ | ✅ `management-plane-restore-drill-cronjob.yaml` + `/settings/backup-drill` dashboard (T7.3) |
| Snapshot CRDs (per-cluster Velero) | ⚠️ | ✅ `cluster_snapshots_velero.go` |

**Verdict.** We are clearly ahead on backup operability: per-cluster scheduling, restore-drill verification, dashboard cards.

---

## 12. Service mesh

| Capability | Rancher | Astronomer |
|---|---|---|
| Mesh detection (Istio / Linkerd / Cilium-mesh / Kuma) | ⚠️ Istio-only | ✅ all four via `service_mesh.go` detection |
| Mesh install via catalog | ✅ | ✅ |
| Istio VirtualService / DestinationRule UI | ✅ behind `IstioVirtualServiceUI` feature gate | ❌ |
| mTLS posture | ⚠️ | ✅ `/clusters/[id]/service-mesh/mtls` |

**Gap.** Rancher's Istio CRD editor is a real UI; ours is "we know it's there" + per-mesh-quickstart links.

---

## 13. Settings + branding

| Capability | Rancher | Astronomer |
|---|---|---|
| Total platform settings | 162 (in `pkg/settings/setting.go`) | ~16 (in `internal/handler/platform_settings.go`) |
| Branding (product name / logo / primary color) | ✅ | ✅ |
| Pre-login banner | ✅ | ✅ |
| In-app persistent banner (incident message) | ⚠️ | ✅ `banner.global_text` + severity enum |
| Feature flags (per-feature) | ✅ `pkg/features` (~22 features) | ✅ `feature.catalog`, `feature.projects`, `feature.monitoring`, `feature.argocd`, `feature.security`, `feature.backups` |
| Helm version pin | ✅ `helm-version` setting | ⚠️ chart-time |
| Engine-install URL (Docker version) | ✅ | ❌ (we don't provision docker hosts) |
| Telemetry endpoint | ✅ `telemetry-url` | ✅ `telemetry.endpoint` + opt-in |
| Auth-cache TTL | ✅ `authorization-cache-ttl-seconds` | ✅ middleware constant |
| First-login flow | ✅ | ✅ `must_change_password` |

**Verdict.** Rancher exposes 10× more tuning knobs. Many of those are legacy / RKE-specific. Our smaller set covers the common operator levers.

---

## 14. Notifications

| Channel | Rancher | Astronomer |
|---|---|---|
| Email (SMTP) | ✅ | ✅ |
| Slack webhook | ✅ | ✅ T4.1 |
| PagerDuty Events API v2 | ✅ | ✅ T4.1 |
| MS Teams (Power Automate) | ✅ | ✅ T4.1 |
| Generic webhook | ✅ | ✅ |
| Webex / Wechat / Dingtalk | ✅ AlertManager | ❌ |
| Per-rule channel routing | ✅ | ✅ `alert_rule_channels` (sprint 15) |
| Notification test send | ✅ | ✅ `Test channel` button |
| Failed-delivery retry + audit | ⚠️ | ✅ `webhook_deliveries` + retry endpoint |

---

## 15. Cluster Explorer — concrete drill-down examples

These are user-visible features, not just API parity:

| Scenario | Rancher | Astronomer |
|---|---|---|
| "Show me Pods that crashed in the last hour" | ✅ filter by `status.containerStatuses[*].restartCount > 0` | ⚠️ events tab shows restart events; no preset filter |
| "Edit the Deployment image tag in the browser" | ✅ live YAML editor with diff preview | ⚠️ raw PATCH via API; no inline editor UI |
| "Scale this StatefulSet from 1 to 3 replicas" | ✅ slider | ✅ scale input |
| "Restart a Deployment (kubectl rollout restart)" | ✅ button | ✅ button |
| "Watch this workload's events live" | ✅ live tail | ⚠️ 15s poll |
| "Run kubectl on this cluster in my browser" | ✅ kubectl shell | ✅ astronomer-shell (with command audit recording + idle reaper) |
| "Diff two RoleBindings" | ❌ | ❌ |
| "Find all Pods with image X across the fleet" | ⚠️ via Lens-style search | ✅ resources_search + topk image rollup (image-vulns drilldown) |
| "Bulk delete failed Jobs" | ✅ multi-select | ⚠️ per-row delete |
| "Cordon node X and drain its Pods" | ✅ button | ⚠️ via shell only |
| "Open Grafana panel for this Pod" | ✅ deep link | ⚠️ widget grid links but not per-pod-panel |
| "Open Argo Application that owns this workload" | ✅ via Fleet annotations | ✅ ArgoCD UI proxy with deeplinks |
| "Apply a NetworkPolicy template to this namespace" | ⚠️ via YAML | ✅ first-class `Apply template` button |
| "Mark a cluster down for maintenance (suppress alerts)" | ⚠️ via silences | ✅ `MaintenanceWindow` + `MaintenanceGate` middleware (migration 050) |
| "Restore a project's resource quota to defaults" | ⚠️ | ✅ `/settings/quotas/[name]/usage` + revert |

---

## 16. Admin operations

| Capability | Rancher | Astronomer |
|---|---|---|
| DLQ / failed-task retry | ⚠️ via controller logs | ✅ `/dashboard/settings/operations` retry + discard (T28b) |
| Reconciler concurrency tuning | ✅ `cluster-controller-start-count` setting | ✅ `reconciler_concurrency.go` (migration 049) |
| Worker queue depth observability | ⚠️ via Prometheus | ✅ `astronomer_worker_*` metrics + admin queue page |
| Support bundle (k8s logs + state snapshot) | ✅ `kubectl rancher cluster bundle` | ✅ `internal/handler/supportbundle.go` |
| Manage downstream agent images | ✅ `agent-image` setting | ✅ `AgentImageRepo`/`AgentImageTag` config |
| Force-rotate registration tokens | ✅ | ✅ short-TTL (1h) per cleanup T6.078 |
| Cross-pod tunnel proxy (HA mgmt-plane) | ⚠️ via leader election | ✅ HTTP via `forwardToOwnerPod`; WS via `ForwardWSToOwnerPod` (today) |
| Schema-health refusal on startup | ⚠️ | ✅ T8.1 (`db.SchemaHealth`) |
| Migration drift backfill | ⚠️ | ✅ migration 087 (orphan steps) + 088 (decommissioned status) |

---

## 17. API + CLI

| Capability | Rancher | Astronomer |
|---|---|---|
| HTTP/JSON API for everything in the UI | ✅ | ✅ |
| OpenAPI spec | ⚠️ generated from the management API | ✅ `docs/openapi.yaml` (T1 sprint 077) |
| Swagger UI | ⚠️ | ✅ `/docs/openapi/` |
| Official CLI | ✅ `rancher` CLI | ✅ `astro` CLI (`cmd/astro/` + `internal/astrocli/`) |
| `kubectl` impersonation as user | ✅ kubeconfig with proxy | ✅ kubeconfig generation per cluster |
| Direct-access kubeconfig (bypass proxy) | ⚠️ | ✅ `Enable & download direct` flow (audit-aware) |
| Webhook subscriptions for state changes | ⚠️ | ✅ first-class CRUD (migration 048) |
| Event SSE stream | ✅ | ✅ `/events/stream/` |

---

## 18. Extensions / plugins

| Capability | Rancher | Astronomer |
|---|---|---|
| UI extension SDK (drop in custom pages) | ✅ behind `UIExtension` feature gate | ❌ |
| Marketplace for extensions | ✅ | ⚠️ helm catalog only |
| Custom navlinks | ✅ "Manage Navlinks" role + UI | ⚠️ `widgets` table can host links, no dedicated UI |
| Plugin store | ✅ | ❌ |

**Gap.** Rancher ships an extension SDK that lets vendors author Vue pages and drop them under `/dashboard/<ext>`. We don't have an equivalent.

---

## 19. Things we ship that Rancher does not

To be balanced — these are the surfaces where we are clearly ahead:

1. **Per-cluster cluster_template binding** with drift-check + reapply + per-tool install step rows in the registration timeline.
2. **Compliance baselines** (PCI/HIPAA/SOC2/FedRAMP) — apply / revert with audit trail.
3. **Compliance posture rollup** — fleet-wide weighted score across CIS / vulns / netpol / audit.
4. **Image-vulnerability snapshots** + fleet rollup + top-N drilldown + per-image history (migration 081).
5. **Anomaly baselines** on metrics — rolling-window aggregates + alert-rule kind that fires on stddev deviation.
6. **CRD-mirror tables** for fast UI list rendering — incrementally updated by the agent without paying per-request informer cost.
7. **Restore drill** — scheduled CronJob that actually tests the management-plane pg restore + dashboard health card.
8. **Maintenance windows** + a middleware gate that fast-fails write APIs during a window.
9. **Cluster groups + fleet operations** — first-class label-set abstraction + bulk operation tracker with per-cluster status fanout.
10. **Cross-cluster Projects** — our project_namespaces row spans clusters; Rancher Projects are cluster-bound.
11. **PodSecurityStandards + NetworkPolicy templates** as first-class operator surfaces, not raw YAML.
12. **Audit-V1 schema** with partitioning + retention + SIEM forwarder + CSV/JSON export.
13. **Webhook subscriptions** with delivery history + retry + dispatcher.
14. **Reconciler operations admin panel** — retry/discard DLQ items, watch reconciler runs.
15. **Cluster condition remediation** — the reconciler actually acts on False conditions instead of just rendering a red pill (migration 086).

---

## 20. The honest "would we win a buyer comparison" answer

Buyer asks → we win if:

- **Compliance-first shop** (PCI/HIPAA/FedRAMP audit prep, audit-log retention, image vulns, CIS, network policy enforcement) → **we win.**
- **CISO-facing single-pane-of-glass for security posture** → **we win.**
- **"We just want to import our existing EKS/AKS/GKE clusters and manage them"** → **draw.** We support the import flow; Rancher has more polish on import-day UX.
- **Multi-tenant SaaS-style platform with project quotas and PSS enforcement** → **we win** on opinionation; **draw** on UX polish.
- **"We want one dashboard to provision and operate clusters end-to-end"** → **Rancher wins.** Our provisioning surface is zero.
- **"We want to scaffold a Vue plugin on top of the platform"** → **Rancher wins.** We have no extension SDK.
- **"We want a 20-dashboard kube-prometheus-stack experience out of the box"** → **Rancher wins** on breadth (20 vs 6); we ship the toggle but a smaller library.
- **Production-grade backup/DR with restore drills** → **we win.**
- **20+ canned RBAC roles for every persona** → **Rancher wins** (41 vs 8). The fix is straight YAML; gap is real today.

---

## 21. Recommended next moves (ordered by ROI)

1. **Expand the role-template catalog from 8 → 25.** Author the missing project / cluster scoped roles (manage workloads / manage ingress / manage services / manage secrets / view monitoring / read-only / etc.). Pure YAML; would close ~60 % of the buyer "you don't have enough built-in roles" objection.
2. **Inline YAML editor in the cluster explorer.** The backend PATCH path is already there (`/k8s/*` proxy supports it). Wrap CodeMirror + a diff view. This is the single most-cited UX gap vs Rancher.
3. **Curated dashboard library expansion 6 → 15.** Add etcd-health, kubelet, scheduler, controller-manager, ingress (nginx/traefik), per-namespace overview. Pure JSON, ships in `deploy/dashboards/`.
4. **UI extension SDK skeleton.** Even a minimal Vue/React drop-in slot would close the "we want to author plugins" pitch.
5. **Bulk-select on workload tables.** Multi-select + bulk delete / bulk restart. Frontend work, one or two PRs.
6. **Istio CRD UI** (VirtualService + DestinationRule editor) gated behind a per-cluster service-mesh detection. We already detect Istio; we just don't have the YAML-aware editor.
7. **Watch streams** to replace 15s polling on hot pages (Pods, Events). The CRD-mirror v2 table + the existing SSE event-stream are the building blocks.

The first one moves the most buyer-comparison needle for the least engineering cost.
