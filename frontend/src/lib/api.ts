import api from "@/lib/api/transport";

export type { OwnershipTransferResult } from "@/lib/api/ownership";
export default api;

export {
  createCluster,
  deleteCluster,
  getCluster,
  getClusters,
  updateCluster,
} from "@/lib/api/clusters";
export type { UpdateClusterInput } from "@/lib/api/clusters";
export { getResourceDiscovery, getResourceSchema } from "@/lib/api/resources";
export type {
  ResourceDiscoveryEntryView,
  ResourceDiscoveryView,
  ResourcePolicyView,
  ResourceSchemaView,
  ResourceType,
} from "@/lib/api/resources";

export * from "@/lib/api/auth";
export * from "@/lib/api/public-settings";

export * from "@/lib/api/feature-flags";

// --- Clusters ---

export * from "@/lib/api/cluster-agents";

export * from "@/lib/api/cluster-registration";

export * from "@/lib/api/nodes";

export * from "@/lib/api/workloads";

export * from "@/lib/api/metrics";

// --- RBAC ---

export {
  createClusterRole,
  createClusterRoleBinding,
  createGlobalRole,
  createGlobalRoleBinding,
  createProjectRole,
  createProjectRoleBinding,
  deleteClusterRoleBinding,
  deleteGlobalRoleBinding,
  deleteProjectRoleBinding,
  getClusterRoles,
  getEffectivePermissionsForUser,
  getGlobalRoles,
  getMyEffectivePermissions,
  getProjectRoles,
  listClusterRoleBindings,
  listGlobalRoleBindings,
  listProjectRoleBindings,
  previewPermissions,
} from "@/lib/api/rbac";
export type { EffectivePermissionParams } from "@/lib/api/rbac";

export * from "@/lib/api/user-settings";

export * from "@/lib/api/audit";

// --- Alerting ---
export * from "@/lib/api/alerting";

// Logging is a cohesive domain module. Re-exporting keeps existing callers
// source-compatible while new code imports from `@/lib/api/logging`.
export * from "@/lib/api/logging";

export * from "@/lib/api/kubernetes-resources";

export * from "@/lib/api/projects";

// --- Catalog / Helm ---

export * from "@/lib/api/catalog";

// --- Security ---

export {
  applySecurityPolicy,
  assignSecurityPolicy,
  createPodSecurityTemplate,
  deletePodSecurityTemplate,
  getClusterSecurityPolicies,
  getPodSecurityTemplates,
  removeSecurityPolicy,
  updatePodSecurityTemplate,
} from "@/lib/api/security-policies";

export * from "@/lib/api/resource-search";

export * from "@/lib/api/kubernetes-proxy";

// --- Cluster Tools ---

export * from "@/lib/api/tools";

export * from "@/lib/api/dex";

// CIS assessment operations use generated paths and raw wire contracts.
export * from "./api/security-scans";

// Velero-backed backup operations use generated paths and wire contracts,
// with explicit view-model mapping in the domain module.
export * from "./api/backups";

// ============================================================
// Settings hub (platform, smtp, webhooks, quotas, group mappings,
// compliance, backup drill) — see lib/api/settings.ts.
// ============================================================
export * from "./api/settings";

// ============================================================
// Project detail tabs (policy, cloud credentials, effective
// quota) and the top-level cluster-templates surface — see
// lib/api/project-detail.ts. Consumers can also import directly
// from '@/lib/api/project-detail' to skip the re-export hop.
// ============================================================
export * from "./api/project-detail";

// ============================================================
// Account security (TOTP, password reset, admin user actions,
// logout-with-redirect) — see lib/api/account-security.ts.
// Consumers can also import directly from
// '@/lib/api/account-security' to skip the re-export hop.
// ============================================================
export * from "./api/account-security";

// ============================================================
// Admin security diagnostics (F-05): key-status + shell-session
// audit views. See lib/api/admin-security.ts.
// ============================================================
export * from "./api/admin-security";

// ============================================================
// SIEM forwarders (F-05): external syslog / Splunk HEC / NDJSON
// destinations + per-forwarder status. See lib/api/siem-forwarders.ts.
// ============================================================
export * from "./api/siem-forwarders";

// ============================================================
// SCIM provisioning tokens (F-05): mint / list / revoke. See
// lib/api/scim-tokens.ts.
// ============================================================
export * from "./api/scim-tokens";

// ============================================================
// Alertmanager-style inhibition rules (P-03). See
// lib/api/alerting-inhibitions.ts.
// ============================================================
export * from "./api/alerting-inhibitions";

// ============================================================
// Gatekeeper / OPA constraint authoring (P-04). See
// lib/api/gatekeeper-constraints.ts.
// ============================================================
export * from "./api/gatekeeper-constraints";

// ============================================================
// Cluster groups (migration 066) — operator-defined folder
// hierarchy over clusters. See lib/api/cluster-groups.ts.
// ============================================================
export * from "./api/cluster-groups";

// ============================================================
// UI extensions — manifest validation and registry controls.
// ============================================================
export * from "./api/extensions";

// Charlie is an external intelligence service; this module is the typed local
// gateway boundary and contains no model, RAG, or agent implementation.
export * from "./api/charlie";
export * from "./api/charlie-admin";
