# Route and scope review matrix — Plan 027

The family-level entries below record static review and selected fresh source browser observations. The generated route inventory is mechanical coverage accounting; it does not mean every route, role, state or action was browser-tested. Generic resource routes cover multiple Kubernetes kinds. No live backend mutations were exercised.

| Family | Intended entry / scope | Second-pass result |
|---|---|---|
| Home / clusters / search | Global Home and cluster switcher; global inventory | Present. Quick-page search lacks pointer trigger; header responsiveness and switching need repair. |
| Cluster overview / nodes / namespaces / events | Cluster group; cluster or namespaced inventory | Present. Summary links incomplete; namespace applicability must be explicit. |
| Workloads / pods | Workloads group; selected namespaces before pagination | Workload kind lists filter after pagination. Rich failure status lost in workload Pods tab. |
| Services / ingress / Gateway | Service Discovery; discovery + scope | Present. Shared server-backed namespace paging already implemented; no blanket missing-pagination claim. |
| Volumes / claims / storage classes / config / secrets | Storage; namespace applicability varies by kind | Present. Preserve server-backed scope and clarify cluster-scoped kinds. |
| Policy / Kubernetes RBAC | Policy / RBAC; cluster or namespace depending on resource | Core entries present, Gatekeeper discovery gating implemented. |
| Custom resources | More Resources; API group/version/type and namespace | CR namespace selector ignored; detail return/delete destination wrong; palette inherits sidebar cap. |
| Delivery | Global + cluster Delivery; estate versus project | Sidebar/tab/palette mismatch; visibility permission differs from destination; project transaction leaves old namespaces. |
| Apps / Catalog | Cluster Apps and global Catalog; mixed cluster/project/global repository scope | Cluster receipt/progress and release inspection gap; installed inventory scope not tied to header. |
| Tools / applied template / adoption | Cluster group / Cluster Management | Template management link disappears after optional tools install; Tools lifecycle itself has progress/error handling. |
| Metrics / monitoring / Grafana | Observability; cluster metrics and installation scope | Summary links absent; global Shared stacks and Metrics both selected. Grafana capability gating present. |
| Alerts | Global and cluster Alerting | No investigation drill-down; Active label starts with all statuses. |
| Logging | Global pipelines + cluster log view | Existing pipelines cannot be fully inspected/edited; configuration/source scope retained in plan. |
| Image scans / security / CIS | Security plus cluster Image Scans | Image Scans local namespace filter disagrees with header. Security/CIS paging exists. |
| Velero workload snapshots | Cluster Management → Snapshots when installed | 403 status is presented as not installed; restore receipt/outcome UI missing. |
| Management backup / control-plane DR | Settings backup / separate cluster DR | Distinct lifecycles intentional; legacy global backup URLs redirect. Do not combine restore identities. |
| Projects / access / RBAC | Users & Access, project details | Substantive member/native grants/scope support present. Project-only navigation visibility needs testing/alignment. |
| Settings / SSO / SCIM / integrations | Settings hub / subnavigation / contextual links | Major families reachable. SMTP failure/unconfigured and saved-vs-draft test semantics wrong. |
| Audit / compliance / shell audit | Audit + settings compliance contextual links | Reachable; rejected claims that shell audit/compliance/SCIM are orphaned. |
| Account / Charlie / extensions | Account menu and capability/activation gates | Entries and gating present; Charlie settings access is not limited solely to sidebar link. |
| Forms / errors / dialogs | Shared primitives plus individual resource flows | Guided create retry reuses old failed manifest. Existing ModalShell focus behavior present; sidebar assessed separately. |
| Mobile / tablet shell | Header + off-canvas sidebar | Fresh geometry verifies clipping/overlap. Closed-sidebar keyboard focus separately reproduced/tested in evidence. |

## Planning snapshot route inventory (144 representative route records)

Source: `frontend/tests/e2e-smoke/route-manifest.generated.json`, read without regeneration. Each row is an existing route fixture, not a claim of a separate UX page or successful live workflow.

| Route module | Representative URL | Kind |
|---|---|---|
| `/auth/change-password/index.tsx` | `/auth/change-password` | auth |
| `/auth/login/forgot-password/index.tsx` | `/auth/login/forgot-password` | auth |
| `/auth/login/index.tsx` | `/auth/login` | auth |
| `/auth/login/reset-password/index.tsx` | `/auth/login/reset-password?token=smoke-reset-token` | auth |
| `/dashboard/account/preferences/index.tsx` | `/dashboard/account/preferences` | app |
| `/dashboard/account/security/index.tsx` | `/dashboard/account/security` | app |
| `/dashboard/admin/users/$id/index.tsx` | `/dashboard/admin/users/c-smoke-1` | app |
| `/dashboard/agents/index.tsx` | `/dashboard/agents` | app |
| `/dashboard/alerting/baselines/index.tsx` | `/dashboard/alerting/baselines` | app |
| `/dashboard/alerting/index.tsx` | `/dashboard/alerting` | app |
| `/dashboard/audit/index.tsx` | `/dashboard/audit` | app |
| `/dashboard/audit/shell-sessions/index.tsx` | `/dashboard/audit/shell-sessions` | app |
| `/dashboard/backups/index.tsx` | `/dashboard/backups` | app |
| `/dashboard/backups/restores/$restoreId/index.tsx` | `/dashboard/backups/restores/restore-smoke-1` | app |
| `/dashboard/backups/runs/$runId/index.tsx` | `/dashboard/backups/runs/run-smoke-1` | app |
| `/dashboard/backups/schedules/new/index.tsx` | `/dashboard/backups/schedules/new` | app |
| `/dashboard/backups/storage/new/index.tsx` | `/dashboard/backups/storage/new` | app |
| `/dashboard/catalog/index.tsx` | `/dashboard/catalog` | app |
| `/dashboard/charlie/index.tsx` | `/dashboard/charlie` | app |
| `/dashboard/cluster-templates/$id/edit/index.tsx` | `/dashboard/cluster-templates/c-smoke-1/edit` | app |
| `/dashboard/cluster-templates/$id/index.tsx` | `/dashboard/cluster-templates/c-smoke-1` | app |
| `/dashboard/cluster-templates/index.tsx` | `/dashboard/cluster-templates` | app |
| `/dashboard/cluster-templates/new/index.tsx` | `/dashboard/cluster-templates/new` | app |
| `/dashboard/clusters/$id/$resource/$.tsx` | `/dashboard/clusters/c-smoke-1/deployments/default/smoke-app` | app |
| `/dashboard/clusters/$id/$resource/index.tsx` | `/dashboard/clusters/c-smoke-1/deployments` | app |
| `/dashboard/clusters/$id/adoption/index.tsx` | `/dashboard/clusters/c-smoke-1/adoption` | app |
| `/dashboard/clusters/$id/alerting/index.tsx` | `/dashboard/clusters/c-smoke-1/alerting` | app |
| `/dashboard/clusters/$id/apps/index.tsx` | `/dashboard/clusters/c-smoke-1/apps` | app |
| `/dashboard/clusters/$id/control-plane-snapshots/index.tsx` | `/dashboard/clusters/c-smoke-1/control-plane-snapshots` | app |
| `/dashboard/clusters/$id/custom-resources/$.tsx` | `/dashboard/clusters/c-smoke-1/custom-resources/cert-manager.io/v1/certificates` | app |
| `/dashboard/clusters/$id/custom-resources/index.tsx` | `/dashboard/clusters/c-smoke-1/custom-resources` | app |
| `/dashboard/clusters/$id/delivery/bundles/$bundleId/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/bundles/bundle-smoke-1` | app |
| `/dashboard/clusters/$id/delivery/bundles/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/bundles` | app |
| `/dashboard/clusters/$id/delivery/configuration-templates/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/configuration-templates` | app |
| `/dashboard/clusters/$id/delivery/deployments/$deploymentId/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/deployments/deployment-smoke-1` | app |
| `/dashboard/clusters/$id/delivery/deployments/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/deployments` | app |
| `/dashboard/clusters/$id/delivery/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery` | app |
| `/dashboard/clusters/$id/delivery/override-sets/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/override-sets` | app |
| `/dashboard/clusters/$id/delivery/rollouts/$rolloutId/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/rollouts/rollout-smoke-1` | app |
| `/dashboard/clusters/$id/delivery/rollouts/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/rollouts` | app |
| `/dashboard/clusters/$id/delivery/sources/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/sources` | app |
| `/dashboard/clusters/$id/delivery/system-components/$componentId/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/system-components/component-smoke-1` | app |
| `/dashboard/clusters/$id/delivery/system-components/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/system-components` | app |
| `/dashboard/clusters/$id/delivery/targets/$targetId/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/targets/target-smoke-1` | app |
| `/dashboard/clusters/$id/delivery/targets/index.tsx` | `/dashboard/clusters/c-smoke-1/delivery/targets` | app |
| `/dashboard/clusters/$id/gatekeeper/index.tsx` | `/dashboard/clusters/c-smoke-1/gatekeeper` | app |
| `/dashboard/clusters/$id/image-scans/index.tsx` | `/dashboard/clusters/c-smoke-1/image-scans` | app |
| `/dashboard/clusters/$id/index.tsx` | `/dashboard/clusters/c-smoke-1` | app |
| `/dashboard/clusters/$id/logging/index.tsx` | `/dashboard/clusters/c-smoke-1/logging` | app |
| `/dashboard/clusters/$id/metrics/index.tsx` | `/dashboard/clusters/c-smoke-1/metrics` | app |
| `/dashboard/clusters/$id/monitoring-stack/index.tsx` | `/dashboard/clusters/c-smoke-1/monitoring-stack` | app |
| `/dashboard/clusters/$id/network-access/index.tsx` | `/dashboard/clusters/c-smoke-1/network-access` | app |
| `/dashboard/clusters/$id/network-policies/index.tsx` | `/dashboard/clusters/c-smoke-1/network-policies` | app |
| `/dashboard/clusters/$id/nodes/$nodeName/index.tsx` | `/dashboard/clusters/c-smoke-1/nodes/node-smoke-1` | app |
| `/dashboard/clusters/$id/registries/index.tsx` | `/dashboard/clusters/c-smoke-1/registries` | app |
| `/dashboard/clusters/$id/resources/index.tsx` | `/dashboard/clusters/c-smoke-1/resources` | app |
| `/dashboard/clusters/$id/service-mesh/index.tsx` | `/dashboard/clusters/c-smoke-1/service-mesh` | app |
| `/dashboard/clusters/$id/service-mesh/mtls/index.tsx` | `/dashboard/clusters/c-smoke-1/service-mesh/mtls` | app |
| `/dashboard/clusters/$id/shell/index.tsx` | `/dashboard/clusters/c-smoke-1/shell` | app |
| `/dashboard/clusters/$id/snapshots/index.tsx` | `/dashboard/clusters/c-smoke-1/snapshots` | app |
| `/dashboard/clusters/$id/template/index.tsx` | `/dashboard/clusters/c-smoke-1/template` | app |
| `/dashboard/clusters/$id/tools/index.tsx` | `/dashboard/clusters/c-smoke-1/tools` | app |
| `/dashboard/clusters/$id/workloads/index.tsx` | `/dashboard/clusters/c-smoke-1/workloads` | app |
| `/dashboard/clusters/index.tsx` | `/dashboard/clusters` | app |
| `/dashboard/clusters/register/index.tsx` | `/dashboard/clusters/register` | app |
| `/dashboard/delivery/bundles/$bundleId/index.tsx` | `/dashboard/delivery/bundles/bundle-smoke-1` | app |
| `/dashboard/delivery/bundles/$bundleId/versions/$versionId/index.tsx` | `/dashboard/delivery/bundles/bundle-smoke-1/versions/version-smoke-1` | app |
| `/dashboard/delivery/bundles/index.tsx` | `/dashboard/delivery/bundles` | app |
| `/dashboard/delivery/configuration-templates/index.tsx` | `/dashboard/delivery/configuration-templates` | app |
| `/dashboard/delivery/deployments/$deploymentId/index.tsx` | `/dashboard/delivery/deployments/deployment-smoke-1` | app |
| `/dashboard/delivery/deployments/index.tsx` | `/dashboard/delivery/deployments` | app |
| `/dashboard/delivery/index.tsx` | `/dashboard/delivery` | app |
| `/dashboard/delivery/override-sets/index.tsx` | `/dashboard/delivery/override-sets` | app |
| `/dashboard/delivery/rollouts/$rolloutId/index.tsx` | `/dashboard/delivery/rollouts/rollout-smoke-1` | app |
| `/dashboard/delivery/rollouts/index.tsx` | `/dashboard/delivery/rollouts` | app |
| `/dashboard/delivery/sources/index.tsx` | `/dashboard/delivery/sources` | app |
| `/dashboard/delivery/targets/$targetId/index.tsx` | `/dashboard/delivery/targets/target-smoke-1` | app |
| `/dashboard/delivery/targets/index.tsx` | `/dashboard/delivery/targets` | app |
| `/dashboard/extensions/$name/index.tsx` | `/dashboard/extensions/smoke-app` | app |
| `/dashboard/extensions/index.tsx` | `/dashboard/extensions` | app |
| `/dashboard/index.tsx` | `/dashboard` | app |
| `/dashboard/logging/index.tsx` | `/dashboard/logging` | app |
| `/dashboard/monitoring/index.tsx` | `/dashboard/monitoring` | app |
| `/dashboard/monitoring/stacks/index.tsx` | `/dashboard/monitoring/stacks` | app |
| `/dashboard/projects/$id/catalogs/index.tsx` | `/dashboard/projects/c-smoke-1/catalogs` | app |
| `/dashboard/projects/$id/cloud-credentials/$credId/edit/index.tsx` | `/dashboard/projects/c-smoke-1/cloud-credentials/cred-smoke-1/edit` | app |
| `/dashboard/projects/$id/cloud-credentials/index.tsx` | `/dashboard/projects/c-smoke-1/cloud-credentials` | app |
| `/dashboard/projects/$id/cloud-credentials/new/index.tsx` | `/dashboard/projects/c-smoke-1/cloud-credentials/new` | app |
| `/dashboard/projects/$id/index.tsx` | `/dashboard/projects/c-smoke-1` | app |
| `/dashboard/projects/$id/policy/index.tsx` | `/dashboard/projects/c-smoke-1/policy` | app |
| `/dashboard/projects/$id/quota/index.tsx` | `/dashboard/projects/c-smoke-1/quota` | app |
| `/dashboard/projects/index.tsx` | `/dashboard/projects` | app |
| `/dashboard/rbac/index.tsx` | `/dashboard/rbac` | app |
| `/dashboard/search/index.tsx` | `/dashboard/search` | app |
| `/dashboard/security/index.tsx` | `/dashboard/security` | app |
| `/dashboard/security/scans/$scanId/index.tsx` | `/dashboard/security/scans/scan-smoke-1` | app |
| `/dashboard/security/scans/new/index.tsx` | `/dashboard/security/scans/new` | app |
| `/dashboard/settings/auth/connectors/$id/index.tsx` | `/dashboard/settings/auth/connectors/c-smoke-1` | app |
| `/dashboard/settings/auth/connectors/new/index.tsx` | `/dashboard/settings/auth/connectors/new` | app |
| `/dashboard/settings/auth/index.tsx` | `/dashboard/settings/auth` | app |
| `/dashboard/settings/auth/install/index.tsx` | `/dashboard/settings/auth/install` | app |
| `/dashboard/settings/auth/register-sso/index.tsx` | `/dashboard/settings/auth/register-sso` | app |
| `/dashboard/settings/auth/scim-tokens/index.tsx` | `/dashboard/settings/auth/scim-tokens` | app |
| `/dashboard/settings/auth/scim-tokens/new/index.tsx` | `/dashboard/settings/auth/scim-tokens/new` | app |
| `/dashboard/settings/auth/settings/index.tsx` | `/dashboard/settings/auth/settings` | app |
| `/dashboard/settings/backup-drill/index.tsx` | `/dashboard/settings/backup-drill` | app |
| `/dashboard/settings/backup/destinations/new/index.tsx` | `/dashboard/settings/backup/destinations/new` | app |
| `/dashboard/settings/backup/index.tsx` | `/dashboard/settings/backup` | app |
| `/dashboard/settings/charlie/index.tsx` | `/dashboard/settings/charlie` | app |
| `/dashboard/settings/cluster-groups/index.tsx` | `/dashboard/settings/cluster-groups` | app |
| `/dashboard/settings/cluster-groups/new/index.tsx` | `/dashboard/settings/cluster-groups/new` | app |
| `/dashboard/settings/compliance/baselines/index.tsx` | `/dashboard/settings/compliance/baselines` | app |
| `/dashboard/settings/compliance/index.tsx` | `/dashboard/settings/compliance` | app |
| `/dashboard/settings/general/index.tsx` | `/dashboard/settings/general` | app |
| `/dashboard/settings/general/tokens/new/index.tsx` | `/dashboard/settings/general/tokens/new` | app |
| `/dashboard/settings/gitops/$id/index.tsx` | `/dashboard/settings/gitops/c-smoke-1` | app |
| `/dashboard/settings/gitops/index.tsx` | `/dashboard/settings/gitops` | app |
| `/dashboard/settings/gitops/new/index.tsx` | `/dashboard/settings/gitops/new` | app |
| `/dashboard/settings/group-mappings/index.tsx` | `/dashboard/settings/group-mappings` | app |
| `/dashboard/settings/group-mappings/new/index.tsx` | `/dashboard/settings/group-mappings/new` | app |
| `/dashboard/settings/index.tsx` | `/dashboard/settings` | app |
| `/dashboard/settings/monitoring/index.tsx` | `/dashboard/settings/monitoring` | app |
| `/dashboard/settings/network-policies/index.tsx` | `/dashboard/settings/network-policies` | app |
| `/dashboard/settings/operations/index.tsx` | `/dashboard/settings/operations` | app |
| `/dashboard/settings/platform/index.tsx` | `/dashboard/settings/platform` | app |
| `/dashboard/settings/quotas/$name/index.tsx` | `/dashboard/settings/quotas/smoke-app` | app |
| `/dashboard/settings/quotas/index.tsx` | `/dashboard/settings/quotas` | app |
| `/dashboard/settings/quotas/new/index.tsx` | `/dashboard/settings/quotas/new` | app |
| `/dashboard/settings/quotas/usage/index.tsx` | `/dashboard/settings/quotas/usage` | app |
| `/dashboard/settings/read-audit/index.tsx` | `/dashboard/settings/read-audit` | app |
| `/dashboard/settings/read-audit/new/index.tsx` | `/dashboard/settings/read-audit/new` | app |
| `/dashboard/settings/siem/index.tsx` | `/dashboard/settings/siem` | app |
| `/dashboard/settings/siem/new/index.tsx` | `/dashboard/settings/siem/new` | app |
| `/dashboard/settings/smtp/index.tsx` | `/dashboard/settings/smtp` | app |
| `/dashboard/settings/templates/$key/index.tsx` | `/dashboard/settings/templates/smoke-template` | app |
| `/dashboard/settings/templates/index.tsx` | `/dashboard/settings/templates` | app |
| `/dashboard/settings/vault/index.tsx` | `/dashboard/settings/vault` | app |
| `/dashboard/settings/webhooks/$id/index.tsx` | `/dashboard/settings/webhooks/c-smoke-1` | app |
| `/dashboard/settings/webhooks/index.tsx` | `/dashboard/settings/webhooks` | app |
| `/dashboard/settings/webhooks/new/index.tsx` | `/dashboard/settings/webhooks/new` | app |
| `/dashboard/settings/widgets/index.tsx` | `/dashboard/settings/widgets` | app |
| `/dashboard/tools/index.tsx` | `/dashboard/tools` | app |
| `/dashboard/workloads/index.tsx` | `/dashboard/workloads` | app |
| `/index.tsx` | `/` | app |


## Implementation route and scope update

The planning observations above remain historical evidence. The current generator deliberately expects **146** route fixtures: the 144 preserved routes plus `/dashboard/logging/pipelines/$pipelineId/` and `/dashboard/logging/pipelines/$pipelineId/edit/`. Logging pipeline rows link to inspect; authorized users can enter edit. Detail and edit use exact-ID reads and do not require the row to appear on the current collection page. Existing parameterized detail routes remain contextual destinations, not duplicate sidebar entries.

| Surface | Implemented scope and entry contract | Acceptance evidence / remaining check |
|---|---|---|
| Workloads and pods | Shared all/one/multiple/empty namespace selection reaches the server before page/count; URL and query identity include selection | Handler tests pass; final serialized-query browser verification pending |
| Custom resources | Complete paged discovery index; sidebar stays bounded; visible resource search reaches the full index; namespaced collections enforce authorized selection and opaque continuation | Proxy, discovery and cross-owner tests pass; final browser acceptance pending |
| Project selection | API provides primary/secondary cluster membership and exact per-cluster namespaces; all pickers apply a shared transaction | Project SQL/handler proof passes; latest frontend acceptance pending |
| Cluster switching | Destination model retains supported collection context and drops object identity; target capabilities and scope resolve before inventory loads | Focused transaction tests pass; final race/recovery browser acceptance pending |
| Delivery | Common eight-destination registry; each destination uses its actual permission; explicit estate versus project scope | Desktop/mobile destination journey passes; project-only duplicate picker and summary-link correction pending |
| Resource investigation | URL tabs/log choices and bounded contextual return retain the originating workload; cluster-local identities remain separate | Focused navigation tests pass; final browser history acceptance pending |
| Apps | Shared project scope; exact release readback; owner-aware inspection; durable Catalog operation URL and exact-rollout outcome | Desktop/mobile release inspection, failure and lifecycle browser journeys pass |
| Alerts | Filter-preserving investigation dialog with permission-gated cluster/rule links and honest mutation state | Desktop/mobile browser journeys pass; root reviewed mobile overlay screenshot |
| Logging pipelines | Global server paging; row inspect and edit routes; exact-ID PUT preserves opaque filters and output associations | Desktop/mobile success/failure/permission browser journeys and real SQL association test pass |
| Snapshot restores | Source operation returns target-cluster receipt; target list/detail tracks durable restore identity and observation errors | Desktop/mobile cross-cluster/reload/denied/stale browser journeys and SQL visibility tests pass |
| Metrics | Native node/namespace drill-down must be reachable alongside existing Grafana integration | Browser exposed unreachable native component; route correction pending |
| Shell and search | Pointer-accessible search, unique active destination, responsive controls and inert closed mobile sidebar | Initial five-width evidence retained; final keyboard/accessibility and latest-source screenshots pending |
| Other unchanged families | Existing Services, Storage, Policy, Tools, Settings, Audit, Account and security entries retain their established scope/capability gates | No new standalone sidebar row required; final route-smoke check pending |

These entries track fixture/contract acceptance. They do not certify real extension installation, reconciliation, backup recovery, or protected production qualifications; those remain in Plan 028 and the existing release plans.
