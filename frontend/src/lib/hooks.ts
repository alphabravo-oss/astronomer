// Query key factory lives in ./query-keys.ts (single source of truth).
// Imported here and re-exported so existing `import { queryKeys } from '@/lib/hooks'`
// call sites keep working.
export { queryKeys } from "./query-keys";

export * from "@/lib/hooks/clusters";

export * from "@/lib/hooks/workloads";

export {
  useClusterRoleBindings,
  useClusterRoles,
  useCreateAccessBinding,
  useCreateRole,
  useDeleteAccessBinding,
  useEffectivePermissions,
  useGlobalRoleBindings,
  useGlobalRoles,
  useMyEffectivePermissions,
  useProjectRoleBindings,
  useProjectRoles,
} from "@/lib/hooks/rbac";
export * from "@/lib/hooks/auth";

export * from "@/lib/hooks/user-settings";

export * from "@/lib/hooks/audit";

// Alerting hooks live in the feature module; this export keeps legacy imports
// source-compatible while screens migrate to the owned boundary.
export * from "@/lib/hooks/alerting";

export * from "@/lib/hooks/logging";

export * from "@/lib/hooks/kubernetes-resources";

export * from "@/lib/hooks/projects";

// ============================================================
// Catalog / Helm Hooks
// ============================================================

export * from "@/lib/hooks/catalog";

export {
  useApplySecurityPolicy,
  useAssignSecurityPolicy,
  useClusterSecurityPolicies,
  useCreatePodSecurityTemplate,
  useDeletePodSecurityTemplate,
  usePodSecurityTemplates,
  useRemoveSecurityPolicy,
  useUpdatePodSecurityTemplate,
} from "@/lib/hooks/security";

// ============================================================
// Cluster Tools Hooks
// ============================================================

export * from "@/lib/hooks/tools";

export * from "@/lib/hooks/kubernetes-proxy";

// Compatibility facade while consumers move to their domain ownership path.
export {
  useCISProfiles,
  useCISScan,
  useCISScans,
  useCreateCISScan,
} from "@/components/security/hooks";
